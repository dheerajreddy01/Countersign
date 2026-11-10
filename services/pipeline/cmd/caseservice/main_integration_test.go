//go:build integration

// Integration tests for the case service's handlers: the dispute consumer, the
// outcome consumer and the action endpoint, run against a real Postgres with
// the files from infra/postgres/migrations applied, and a fake pandaproxy that
// records what would have been published.
//
//	TEST_DATABASE_URL=... go test -p 1 -tags integration ./cmd/caseservice/
//
// -p 1 because internal/store's integration tests empty every table between
// tests; these use unique dispute ids instead, but would lose their rows if the
// two packages ran against the same database at once.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/bus"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/events"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

func openStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL is required in CI: the integration job must not pass vacuously")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(db.Close)
	return db, ctx
}

// fakeProxy stands in for pandaproxy's produce endpoint and counts records per
// topic. fail makes it answer 500, as a broker outage would.
type fakeProxy struct {
	mu     sync.Mutex
	counts map[string]int
	fail   atomic.Bool
}

func newFakeProxy(t *testing.T) (*fakeProxy, *bus.Publisher) {
	t.Helper()
	p := &fakeProxy{counts: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.fail.Load() {
			http.Error(w, "broker unavailable", http.StatusInternalServerError)
			return
		}
		topic := strings.TrimPrefix(r.URL.Path, "/topics/")
		p.mu.Lock()
		p.counts[topic]++
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offsets":[{"partition":0,"offset":0}]}`))
	}))
	t.Cleanup(srv.Close)
	return p, bus.NewPublisher(srv.URL)
}

func (p *fakeProxy) published(topic string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[topic]
}

// uniqueDispute returns a dispute id no other test run has used, so these
// tests need not empty the shared tables.
func uniqueDispute(t *testing.T) string {
	return fmt.Sprintf("PP-D-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
}

// webhook builds a raw PayPal dispute webhook.
func webhook(eventID, eventType, disputeID, status, outcomeCode string) []byte {
	resource := map[string]any{
		"dispute_id":               disputeID,
		"reason":                   "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
		"status":                   status,
		"dispute_amount":           map[string]string{"currency_code": "USD", "value": "149.00"},
		"dispute_life_cycle_stage": "INQUIRY",
		"seller_response_due_date": "2026-12-03T11:00:00Z",
		"disputed_transactions": []map[string]any{{
			"seller_transaction_id": "TX-" + disputeID,
			"item_name":             "Trail Runner GTX",
			"buyer":                 map[string]string{"name": "A. Buyer", "email": "buyer@example.com"},
		}},
	}
	if outcomeCode != "" {
		resource["dispute_outcome"] = map[string]any{"outcome_code": outcomeCode}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": eventID, "event_type": eventType, "create_time": "2026-11-09T10:00:00Z", "resource": resource,
	})
	return raw
}

// deliver runs a raw webhook through the normaliser and into the case
// service's dispute handler, the same path the bus takes.
func deliver(t *testing.T, ctx context.Context, db *store.Store, pub *bus.Publisher, raw []byte) {
	t.Helper()
	d, err := events.Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	value, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal dispute: %v", err)
	}
	rec := bus.Record{Topic: bus.TopicDisputeEvents, Key: d.DisputeID, Value: value}
	if err := handleDispute(ctx, db, pub, rec); err != nil {
		t.Fatalf("handleDispute: %v", err)
	}
}

// openCase creates a case through a CREATED webhook and moves it to status.
func openCase(t *testing.T, ctx context.Context, db *store.Store, pub *bus.Publisher, status string) store.Case {
	t.Helper()
	id := uniqueDispute(t)
	deliver(t, ctx, db, pub, webhook("WH-C-"+id, events.TypeDisputeCreated, id, "WAITING_FOR_SELLER_RESPONSE", ""))
	c, err := db.GetCaseByDisputeID(ctx, id)
	if err != nil {
		t.Fatalf("GetCaseByDisputeID: %v", err)
	}
	if status != "" && status != c.Status {
		if _, err := db.RecordOutcome(ctx, store.OutcomeInput{CaseID: c.ID, Result: "setup", Status: status}); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
	}
	c, err = db.GetCase(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	return c
}

func resolve(t *testing.T, ctx context.Context, db *store.Store, pub *bus.Publisher, c store.Case, eventID, code string) store.Case {
	t.Helper()
	deliver(t, ctx, db, pub, webhook(eventID, events.TypeDisputeResolved, c.PayPalDisputeID, "RESOLVED", code))
	got, err := db.GetCase(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	return got
}

func outcomeResults(t *testing.T, ctx context.Context, db *store.Store, caseID int64) []string {
	t.Helper()
	outs, err := db.OutcomesForCase(ctx, caseID)
	if err != nil {
		t.Fatalf("OutcomesForCase: %v", err)
	}
	var results []string
	for _, o := range outs {
		if o.Result != "setup" {
			results = append(results, o.Result)
		}
	}
	return results
}

// --- F1: a dispute PayPal resolves leaves the board ------------------------

func TestPayPalResolutionClosesTheCase(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)

	for code, want := range map[string]string{
		"RESOLVED_SELLER_FAVOUR": "won",
		"RESOLVED_BUYER_FAVOUR":  "lost",
		"CANCELED_BY_BUYER":      "won",
		"RESOLVED_WITH_PAYOUT":   "settled",
		"ACCEPTED":               "lost",
		"DENIED":                 "won",
	} {
		t.Run(code, func(t *testing.T) {
			c := openCase(t, ctx, db, pub, "decision_pending")

			got := resolve(t, ctx, db, pub, c, "WH-R-"+c.PayPalDisputeID, code)
			if got.Status != want {
				t.Errorf("status = %q, want %q", got.Status, want)
			}
			if got.PayPalStatus != "RESOLVED" {
				t.Errorf("paypal_status = %q, want RESOLVED", got.PayPalStatus)
			}
			if r := outcomeResults(t, ctx, db, c.ID); len(r) != 1 || r[0] != want {
				t.Errorf("outcomes = %v, want exactly [%s]", r, want)
			}

			// PayPal retries webhooks: the same resolution again, even under a
			// new event id, records nothing more.
			resolve(t, ctx, db, pub, c, "WH-R-"+c.PayPalDisputeID, code)
			resolve(t, ctx, db, pub, c, "WH-R2-"+c.PayPalDisputeID, code)
			if r := outcomeResults(t, ctx, db, c.ID); len(r) != 1 {
				t.Errorf("after redelivery outcomes = %v, want exactly one", r)
			}
		})
	}
}

func TestPayPalResolutionWithoutARulingLeavesTheStatusAlone(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)

	for _, code := range []string{"", "NONE", "SOMETHING_PAYPAL_ADDS_LATER"} {
		t.Run("code="+code, func(t *testing.T) {
			c := openCase(t, ctx, db, pub, "decision_pending")
			got := resolve(t, ctx, db, pub, c, "WH-R-"+c.PayPalDisputeID, code)
			if got.Status != "decision_pending" {
				t.Errorf("status = %q, want decision_pending: no ruling to apply", got.Status)
			}
			if r := outcomeResults(t, ctx, db, c.ID); len(r) != 0 {
				t.Errorf("outcomes = %v, want none", r)
			}
		})
	}
}

func TestPayPalResolutionOverridesASimulatedRuling(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)

	// The executor's simulated ruling said won; PayPal says otherwise.
	c := openCase(t, ctx, db, pub, "action_taken")
	if _, err := db.RecordOutcome(ctx, store.OutcomeInput{
		CaseID: c.ID, Result: "won", Status: "won", EventKey: "act-sim:won",
		Payload: json.RawMessage(`{"simulated":true}`),
	}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if got := resolve(t, ctx, db, pub, c, "WH-R-"+c.PayPalDisputeID, "RESOLVED_BUYER_FAVOUR"); got.Status != "lost" {
		t.Fatalf("status = %q, want lost: PayPal's real ruling is authoritative", got.Status)
	}

	// The executor's outcomes travel on another topic and can land after
	// PayPal's. A late simulated ruling is recorded but must not undo PayPal.
	late, _ := json.Marshal(map[string]any{
		"case_id": c.ID, "result": "won", "simulated": true, "event_key": "act-late:won",
	})
	if err := handleOutcome(ctx, db, bus.Record{Topic: bus.TopicOutcomes, Value: late}); err != nil {
		t.Fatalf("handleOutcome: %v", err)
	}
	got, _ := db.GetCase(ctx, c.ID)
	if got.Status != "lost" {
		t.Errorf("status = %q after a late simulated ruling, want PayPal's lost to stand", got.Status)
	}
}

func TestPayPalBuyerFavourKeepsAMerchantRefundAsRefunded(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)

	// The merchant accepted the claim; PayPal then closes it for the buyer.
	// That is the refund confirmed, not a separate loss.
	c := openCase(t, ctx, db, pub, "refunded")
	if got := resolve(t, ctx, db, pub, c, "WH-R-"+c.PayPalDisputeID, "RESOLVED_BUYER_FAVOUR"); got.Status != "refunded" {
		t.Errorf("status = %q, want refunded", got.Status)
	}
}

func TestADisputeFirstSeenResolvedDoesNotRequestEvidence(t *testing.T) {
	db, ctx := openStore(t)
	proxy, pub := newFakeProxy(t)

	id := uniqueDispute(t)
	deliver(t, ctx, db, pub, webhook("WH-R-"+id, events.TypeDisputeResolved, id, "RESOLVED", "RESOLVED_SELLER_FAVOUR"))
	c, err := db.GetCaseByDisputeID(ctx, id)
	if err != nil {
		t.Fatalf("GetCaseByDisputeID: %v", err)
	}
	if c.Status != "won" {
		t.Errorf("status = %q, want won", c.Status)
	}
	if n := proxy.published(bus.TopicEvidenceRequested); n != 0 {
		t.Errorf("evidence requested %d times for a dispute that is already over, want 0", n)
	}
}

// --- F2 and F3: the action endpoint ----------------------------------------

func postAction(t *testing.T, h http.HandlerFunc, caseID int64, kind string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"kind": kind, "note": "test"})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/cases/%d/actions", caseID), bytes.NewReader(body))
	req.SetPathValue("id", fmt.Sprint(caseID))
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func actionCount(t *testing.T, ctx context.Context, db *store.Store, caseID int64) int {
	t.Helper()
	acts, err := db.ActionsForCase(ctx, caseID)
	if err != nil {
		t.Fatalf("ActionsForCase: %v", err)
	}
	return len(acts)
}

func TestDoubleClickedActionRunsOnce(t *testing.T) {
	db, ctx := openStore(t)
	proxy, pub := newFakeProxy(t)
	h := actionHandler(db, pub, "")
	c := openCase(t, ctx, db, pub, "decision_pending")

	// Several approvals at once, as a double- or triple-click sends them.
	const clicks = 5
	codes := make([]int, clicks)
	bodies := make([]map[string]any, clicks)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < clicks; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i], bodies[i] = postAction(t, h, c.ID, "accept_claim")
		}(i)
	}
	close(start)
	wg.Wait()

	accepted, refused := 0, 0
	for i, code := range codes {
		switch code {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			refused++
			if msg, _ := bodies[i]["error"].(string); !strings.Contains(msg, "still being processed") {
				t.Errorf("409 body = %v, want an error saying an action is in progress", bodies[i])
			}
		default:
			t.Errorf("click %d: status %d (%v)", i, code, bodies[i])
		}
	}
	if accepted != 1 || refused != clicks-1 {
		t.Errorf("accepted=%d refused=%d, want 1 and %d (codes %v)", accepted, refused, clicks-1, codes)
	}
	if n := proxy.published(bus.TopicActionRequested); n != 1 {
		t.Errorf("published %d actions to the executor, want 1", n)
	}
	if n := actionCount(t, ctx, db, c.ID); n != 1 {
		t.Errorf("recorded %d actions, want 1", n)
	}
}

func TestANewActionIsAllowedOnceThePreviousOneFinished(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)
	h := actionHandler(db, pub, "")
	c := openCase(t, ctx, db, pub, "decision_pending")

	code, body := postAction(t, h, c.ID, "make_offer")
	if code != http.StatusAccepted {
		t.Fatalf("first action: %d %v", code, body)
	}
	action, _ := body["action"].(map[string]any)
	key, _ := action["idempotency_key"].(string)
	if err := db.UpdateActionResult(ctx, key, "executed", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("UpdateActionResult: %v", err)
	}
	if code, body := postAction(t, h, c.ID, "make_offer"); code != http.StatusAccepted {
		t.Errorf("action after the first executed: %d %v, want 202", code, body)
	}
}

func TestAnUnpublishedActionDoesNotLockTheCase(t *testing.T) {
	db, ctx := openStore(t)
	proxy, pub := newFakeProxy(t)
	h := actionHandler(db, pub, "")
	c := openCase(t, ctx, db, pub, "decision_pending")

	proxy.fail.Store(true)
	if code, body := postAction(t, h, c.ID, "make_offer"); code != http.StatusInternalServerError {
		t.Fatalf("with the broker down: %d %v, want 500", code, body)
	}
	proxy.fail.Store(false)
	if code, body := postAction(t, h, c.ID, "make_offer"); code != http.StatusAccepted {
		t.Errorf("retry after the broker recovered: %d %v, want 202", code, body)
	}
}

func TestActionsOnAClosedCaseAreRefused(t *testing.T) {
	db, ctx := openStore(t)
	proxy, pub := newFakeProxy(t)
	h := actionHandler(db, pub, "")

	for _, status := range []string{"won", "lost", "settled", "refunded"} {
		t.Run(status, func(t *testing.T) {
			c := openCase(t, ctx, db, pub, status)
			before := proxy.published(bus.TopicActionRequested)

			code, body := postAction(t, h, c.ID, "accept_claim")
			if code != http.StatusConflict {
				t.Fatalf("accept_claim on a %s case: %d %v, want 409", status, code, body)
			}
			if msg, _ := body["error"].(string); !strings.Contains(msg, "already closed") {
				t.Errorf("409 body = %v, want an error saying the case is closed", body)
			}
			if proxy.published(bus.TopicActionRequested) != before {
				t.Error("a refused action was still published to the executor")
			}
			if n := actionCount(t, ctx, db, c.ID); n != 0 {
				t.Errorf("recorded %d actions, want 0", n)
			}
			if got, _ := db.GetCase(ctx, c.ID); got.Status != status {
				t.Errorf("status = %q, want %q unchanged", got.Status, status)
			}
		})
	}
}

func TestAFollowUpOnASubmittedCaseIsAllowed(t *testing.T) {
	db, ctx := openStore(t)
	_, pub := newFakeProxy(t)
	h := actionHandler(db, pub, "")

	// action_taken means submitted and awaiting a ruling: still open, and a
	// message to the buyer after providing evidence is legitimate.
	c := openCase(t, ctx, db, pub, "action_taken")
	if err := db.SaveDraft(ctx, store.Draft{CaseID: c.ID, Text: "cited [1]", GroundingPassed: true}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if code, body := postAction(t, h, c.ID, "send_message"); code != http.StatusAccepted {
		t.Errorf("send_message on an action_taken case: %d %v, want 202", code, body)
	}
}
