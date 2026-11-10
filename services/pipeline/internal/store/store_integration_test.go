//go:build integration

// Integration tests for the Postgres layer. They need a real database, so they
// are behind the `integration` build tag and skip unless TEST_DATABASE_URL is
// set. CI runs them against a postgres service container with the files from
// infra/postgres/migrations already applied:
//
//	go test -tags integration ./internal/store/
package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/events"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		// Skipping locally is a convenience. Skipping in CI would let the
		// integration job pass green having run nothing at all, which is
		// worse than no job, so fail there instead.
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL is required in CI: the integration job must not pass vacuously")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)

	// Each test starts from a clean slate; order matters for the foreign keys.
	for _, table := range []string{"audit_log", "outcomes", "actions", "drafts",
		"decisions", "evidence_records", "mandates", "cases", "webhook_events"} {
		if _, err := s.pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return s, ctx
}

func testDispute(disputeID string) events.Dispute {
	due := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	return events.Dispute{
		EventID:       "WH-" + disputeID,
		EventType:     events.TypeDisputeCreated,
		DisputeID:     disputeID,
		Reason:        events.ReasonItemNotReceived,
		RawReason:     "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
		Supported:     true,
		Stage:         "inquiry",
		PayPalStatus:  "WAITING_FOR_SELLER_RESPONSE",
		Amount:        "149.00",
		Currency:      "USD",
		RespondBy:     &due,
		BuyerName:     "A. Buyer",
		BuyerEmail:    "buyer@example.com",
		ProductName:   "Trail Runner GTX",
		TransactionID: "7YH29301",
		OccurredAt:    time.Now().UTC(),
	}
}

func TestUpsertCaseInsertsThenUpdates(t *testing.T) {
	s, ctx := newTestStore(t)

	c, inserted, err := s.UpsertCase(ctx, testDispute("PP-D-1"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	if !inserted {
		t.Fatal("first upsert should report an insert")
	}
	if c.ID == 0 || c.PayPalDisputeID != "PP-D-1" {
		t.Fatalf("case = %+v", c)
	}
	if c.Amount != "149.00" {
		t.Errorf("Amount = %q, want 149.00 (NUMERIC must not round)", c.Amount)
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want open", c.Status)
	}
	if c.RespondBy == nil {
		t.Error("RespondBy should be stored")
	}

	// PayPal retries webhooks: a second delivery must update, not duplicate.
	d := testDispute("PP-D-1")
	d.EventType = events.TypeDisputeUpdated
	d.Stage = "chargeback"
	d.Amount = "151.50"
	again, inserted, err := s.UpsertCase(ctx, d)
	if err != nil {
		t.Fatalf("second UpsertCase: %v", err)
	}
	if inserted {
		t.Error("second upsert should not report an insert")
	}
	if again.ID != c.ID {
		t.Errorf("id changed from %d to %d: the case was duplicated", c.ID, again.ID)
	}
	if again.Stage != "chargeback" || again.Amount != "151.50" {
		t.Errorf("update did not apply: %+v", again)
	}

	list, err := s.ListCases(ctx, 10)
	if err != nil {
		t.Fatalf("ListCases: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListCases returned %d rows, want 1", len(list))
	}
}

func TestUpsertCaseDoesNotResetTheInternalLifecycle(t *testing.T) {
	// cases.status is Countersign's own lifecycle. A repeat webhook must not
	// drag a case the merchant has already acted on back to 'open'.
	s, ctx := newTestStore(t)

	c, _, err := s.UpsertCase(ctx, testDispute("PP-D-2"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE cases SET status = 'action_taken' WHERE id = $1`, c.ID); err != nil {
		t.Fatalf("set status: %v", err)
	}

	again, _, err := s.UpsertCase(ctx, testDispute("PP-D-2"))
	if err != nil {
		t.Fatalf("second UpsertCase: %v", err)
	}
	if again.Status != "action_taken" {
		t.Errorf("Status = %q, want action_taken to survive the webhook", again.Status)
	}
}

func TestUpsertCaseWritesTheAuditTrail(t *testing.T) {
	s, ctx := newTestStore(t)

	c, _, err := s.UpsertCase(ctx, testDispute("PP-D-3"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	if _, _, err := s.UpsertCase(ctx, testDispute("PP-D-3")); err != nil {
		t.Fatalf("second UpsertCase: %v", err)
	}

	entries, err := s.Timeline(ctx, c.ID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("timeline has %d entries, want 2", len(entries))
	}
	if entries[0].Event != "Case opened from PayPal dispute webhook" {
		t.Errorf("first event = %q", entries[0].Event)
	}
	if entries[1].Event != "Dispute updated from PayPal webhook" {
		t.Errorf("second event = %q", entries[1].Event)
	}
	var detail map[string]any
	if err := json.Unmarshal(entries[0].Detail, &detail); err != nil {
		t.Fatalf("detail is not JSON: %v", err)
	}
	if detail["raw_reason"] != "MERCHANDISE_OR_SERVICE_NOT_RECEIVED" {
		t.Errorf("audit detail lost the raw reason: %v", detail)
	}
}

func TestGetCaseLookups(t *testing.T) {
	s, ctx := newTestStore(t)
	c, _, err := s.UpsertCase(ctx, testDispute("PP-D-4"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}

	byID, err := s.GetCase(ctx, c.ID)
	if err != nil || byID.PayPalDisputeID != "PP-D-4" {
		t.Fatalf("GetCase = %+v, %v", byID, err)
	}
	byDispute, err := s.GetCaseByDisputeID(ctx, "PP-D-4")
	if err != nil || byDispute.ID != c.ID {
		t.Fatalf("GetCaseByDisputeID = %+v, %v", byDispute, err)
	}
	if _, err := s.GetCase(ctx, 999999); err != ErrNotFound {
		t.Errorf("missing case err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetCaseByDisputeID(ctx, "nope"); err != ErrNotFound {
		t.Errorf("missing dispute err = %v, want ErrNotFound", err)
	}
}

func TestListCasesOrdersByDeadlineWithUndatedLast(t *testing.T) {
	s, ctx := newTestStore(t)

	late := testDispute("PP-D-late")
	lateDue := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	late.RespondBy = &lateDue

	soon := testDispute("PP-D-soon")
	soonDue := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	soon.RespondBy = &soonDue

	undated := testDispute("PP-D-undated")
	undated.RespondBy = nil

	for _, d := range []events.Dispute{late, undated, soon} {
		if _, _, err := s.UpsertCase(ctx, d); err != nil {
			t.Fatalf("UpsertCase %s: %v", d.DisputeID, err)
		}
	}

	list, err := s.ListCases(ctx, 10)
	if err != nil {
		t.Fatalf("ListCases: %v", err)
	}
	got := []string{list[0].PayPalDisputeID, list[1].PayPalDisputeID, list[2].PayPalDisputeID}
	want := []string{"PP-D-soon", "PP-D-late", "PP-D-undated"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestRecordWebhookEventDedupes(t *testing.T) {
	s, ctx := newTestStore(t)

	first, err := s.RecordWebhookEvent(ctx, "WH-dedupe", events.TypeDisputeCreated, "normalizer")
	if err != nil {
		t.Fatalf("RecordWebhookEvent: %v", err)
	}
	if !first {
		t.Fatal("the first delivery should be claimed")
	}
	second, err := s.RecordWebhookEvent(ctx, "WH-dedupe", events.TypeDisputeCreated, "normalizer")
	if err != nil {
		t.Fatalf("RecordWebhookEvent: %v", err)
	}
	if second {
		t.Error("a repeat delivery must not be claimed twice")
	}
	// An event with no id is processed rather than dropped: there is nothing
	// to dedupe on, and discarding it would lose a real dispute.
	claimed, err := s.RecordWebhookEvent(ctx, "", "", "normalizer")
	if err != nil || !claimed {
		t.Errorf("empty event id: %v, %v", claimed, err)
	}
}

func TestMandateLedgerChainsAndVerifies(t *testing.T) {
	s, ctx := newTestStore(t)
	signer, _, err := mandate.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	pub := signer.PublicKey()

	// An empty ledger starts at genesis.
	head, err := s.LatestMandateHash(ctx, pub)
	if err != nil {
		t.Fatalf("LatestMandateHash: %v", err)
	}
	if head != mandate.GenesisHash {
		t.Fatalf("head = %q, want %q", head, mandate.GenesisHash)
	}

	for _, orderID := range []string{"ORD-1", "ORD-2", "ORD-3"} {
		head, err = s.LatestMandateHash(ctx, pub)
		if err != nil {
			t.Fatalf("LatestMandateHash: %v", err)
		}
		m, err := signer.Sign(mandate.Payload{
			User:             "user_8812",
			Agent:            "countersign-demo-agent/1",
			Instruction:      "Buy the Trail Runner GTX in size 10, under $200.",
			Constraints:      []string{"under $200"},
			AmountAuthorized: "200.00",
			Currency:         "USD",
			OrderID:          orderID,
		}, head)
		if err != nil {
			t.Fatalf("Sign %s: %v", orderID, err)
		}
		if _, err := s.InsertMandate(ctx, m); err != nil {
			t.Fatalf("InsertMandate %s: %v", orderID, err)
		}
	}

	chain, err := s.MandateChain(ctx, pub)
	if err != nil {
		t.Fatalf("MandateChain: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain has %d mandates, want 3", len(chain))
	}

	// The round trip through JSONB must preserve the payload exactly, or the
	// signature no longer verifies.
	plain := make([]mandate.Mandate, 0, len(chain))
	for _, sm := range chain {
		plain = append(plain, sm.Mandate)
	}
	if err := mandate.VerifyChain(plain, pub); err != nil {
		t.Fatalf("VerifyChain after a database round trip: %v", err)
	}

	forOrder, err := s.MandatesForOrder(ctx, "ORD-2")
	if err != nil {
		t.Fatalf("MandatesForOrder: %v", err)
	}
	if len(forOrder) != 1 || forOrder[0].Mandate.Payload.OrderID != "ORD-2" {
		t.Fatalf("MandatesForOrder = %+v", forOrder)
	}
}

func TestInsertMandateRefusesAnUnverifiableRecord(t *testing.T) {
	s, ctx := newTestStore(t)
	signer, _, err := mandate.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	m, err := signer.Sign(mandate.Payload{
		Instruction:      "Buy the shoes.",
		AmountAuthorized: "200.00",
		Currency:         "USD",
		OrderID:          "ORD-1",
	}, mandate.GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Tamper after signing: this must never reach the ledger, or a later
	// VerifyChain fails for every record behind it.
	m.Payload.AmountAuthorized = "9999.00"
	if _, err := s.InsertMandate(ctx, m); err == nil {
		t.Fatal("want a refusal for a mandate whose signature does not verify")
	}

	chain, err := s.MandateChain(ctx, m.PublicKey)
	if err != nil {
		t.Fatalf("MandateChain: %v", err)
	}
	if len(chain) != 0 {
		t.Fatalf("ledger has %d rows, want none", len(chain))
	}
}

func TestTwoMandatesCannotClaimTheSamePredecessor(t *testing.T) {
	// This is the database half of the chain guarantee: splicing a
	// back-dated record in means reusing a prev_hash, which the unique index
	// rejects.
	s, ctx := newTestStore(t)
	signer, _, err := mandate.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	first, err := signer.Sign(mandate.Payload{
		Instruction: "Buy the shoes.", AmountAuthorized: "200.00",
		Currency: "USD", OrderID: "ORD-1",
	}, mandate.GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.InsertMandate(ctx, first); err != nil {
		t.Fatalf("InsertMandate: %v", err)
	}

	// A second mandate also linking to genesis: a fork, not an append.
	fork, err := signer.Sign(mandate.Payload{
		Instruction: "Buy a different thing.", AmountAuthorized: "200.00",
		Currency: "USD", OrderID: "ORD-FORK",
	}, mandate.GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.InsertMandate(ctx, fork); err == nil {
		t.Fatal("want a unique-violation: two mandates cannot share a prev_hash")
	}
}

func TestAppendAuditWithoutACase(t *testing.T) {
	s, ctx := newTestStore(t)
	// Mandates are recorded before any dispute exists, so the audit row has
	// no case to attach to.
	if err := s.AppendAudit(ctx, nil, "demo-agent", "Agent placed an order", map[string]any{
		"order_id": "ORD-1",
	}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
}

// RecordOutcome is the only path by which a case reaches a final state, and it
// went untested here while its insert could never succeed: ON CONFLICT against
// a partial unique index needs the index predicate repeated, and without it
// Postgres rejects every call with 42P10. The executor publishes a "submitted"
// outcome and then a terminal ruling, and the case service can redeliver
// either, so the property that matters is: each event key lands exactly once,
// and a redelivery neither duplicates the row nor re-advances the status.
func TestRecordOutcomeIsIdempotentPerEventKey(t *testing.T) {
	s, ctx := newTestStore(t)

	c, _, err := s.UpsertCase(ctx, testDispute("PP-D-OUTCOME"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}

	first := OutcomeInput{
		CaseID:   c.ID,
		Result:   "won",
		Status:   "won",
		EventKey: "act-1:ruling",
		Payload:  json.RawMessage(`{"simulated":true}`),
	}
	recorded, err := s.RecordOutcome(ctx, first)
	if err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if !recorded {
		t.Fatal("first delivery should record the outcome")
	}

	// Redelivery of the same event: a no-op, and it must not move the status
	// even if the redelivered copy disagrees.
	again := first
	again.Status = "lost"
	recorded, err = s.RecordOutcome(ctx, again)
	if err != nil {
		t.Fatalf("RecordOutcome redelivery: %v", err)
	}
	if recorded {
		t.Error("redelivery with the same event key should be a no-op")
	}

	outs, err := s.OutcomesForCase(ctx, c.ID)
	if err != nil {
		t.Fatalf("OutcomesForCase: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outs))
	}
	got, err := s.GetCase(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	if got.Status != "won" {
		t.Errorf("Status = %q, want won (redelivery must not re-advance it)", got.Status)
	}

	// Outcomes without an event key are not deduplicated against each other:
	// the index is partial precisely so they never collide.
	for i := 0; i < 2; i++ {
		if _, err := s.RecordOutcome(ctx, OutcomeInput{CaseID: c.ID, Result: "note"}); err != nil {
			t.Fatalf("RecordOutcome without key #%d: %v", i, err)
		}
	}
	if outs, _ = s.OutcomesForCase(ctx, c.ID); len(outs) != 3 {
		t.Errorf("outcomes = %d, want 3 (keyless outcomes must not dedupe)", len(outs))
	}
}

// The list carries each case's persisted decision, so the console's table shows
// the verdict the pipeline reached instead of re-scoring the case blind. A case
// not yet scored lists with no decision rather than an invented one.
func TestListCasesCarriesThePersistedDecision(t *testing.T) {
	s, ctx := newTestStore(t)

	scored, _, err := s.UpsertCase(ctx, testDispute("PP-D-SCORED"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	if _, _, err := s.UpsertCase(ctx, testDispute("PP-D-UNSCORED")); err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	factors := json.RawMessage(`{"p_win":0.93,"recommendation":"fight"}`)
	if err := s.SaveDecision(ctx, scored.ID, factors, "113.57", "fight"); err != nil {
		t.Fatalf("SaveDecision: %v", err)
	}

	list, err := s.ListCases(ctx, 10)
	if err != nil {
		t.Fatalf("ListCases: %v", err)
	}
	byDispute := map[string]ListedCase{}
	for _, lc := range list {
		byDispute[lc.PayPalDisputeID] = lc
	}

	got := byDispute["PP-D-SCORED"].Decision
	if got == nil {
		t.Fatal("scored case listed without its decision")
	}
	if got.Recommendation != "fight" || got.EV != "113.57" || got.CaseID != scored.ID {
		t.Errorf("decision = %+v", got)
	}
	var f struct {
		PWin float64 `json:"p_win"`
	}
	if err := json.Unmarshal(got.Factors, &f); err != nil || f.PWin != 0.93 {
		t.Errorf("factors = %s (p_win %v, err %v)", got.Factors, f.PWin, err)
	}
	if d := byDispute["PP-D-UNSCORED"].Decision; d != nil {
		t.Errorf("unscored case listed with a decision: %+v", d)
	}
}

// The evidence builder advances a case from a request published when it
// opened. If PayPal resolved the dispute in the meantime, that late step must
// not drag the closed case back to decision_pending.
func TestSetCaseStatusDoesNotReopenAClosedCase(t *testing.T) {
	s, ctx := newTestStore(t)
	c, _, err := s.UpsertCase(ctx, testDispute("PP-D-CLOSED"))
	if err != nil {
		t.Fatalf("UpsertCase: %v", err)
	}
	if _, err := s.RecordOutcome(ctx, OutcomeInput{CaseID: c.ID, Result: "won", Status: "won", EventKey: "paypal:PP-D-CLOSED"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := s.SetCaseStatus(ctx, c.ID, "decision_pending", "evidence-builder", "Scored and drafted", nil); err != nil {
		t.Fatalf("SetCaseStatus on a closed case should be a no-op, got %v", err)
	}
	got, _ := s.GetCase(ctx, c.ID)
	if got.Status != "won" {
		t.Errorf("status = %q, want won to survive a late lifecycle step", got.Status)
	}

	// An open case still advances, and a missing one is still reported.
	open, _, _ := s.UpsertCase(ctx, testDispute("PP-D-OPEN"))
	if err := s.SetCaseStatus(ctx, open.ID, "evidence_ready", "evidence-builder", "ready", nil); err != nil {
		t.Fatalf("SetCaseStatus: %v", err)
	}
	if got, _ := s.GetCase(ctx, open.ID); got.Status != "evidence_ready" {
		t.Errorf("open case status = %q, want evidence_ready", got.Status)
	}
	if err := s.SetCaseStatus(ctx, 1<<40, "evidence_ready", "x", "y", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing case: err = %v, want ErrNotFound", err)
	}
}
