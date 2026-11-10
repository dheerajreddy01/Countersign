// Command caseservice turns normalised dispute events into case rows and asks
// for evidence to be gathered.
//
// It also serves a read-only HTTP API over those rows. That API is what the
// merchant console will read once the evidence builder and decision/draft
// persistence exist; today it returns the case, its audit trail and any
// mandate matching the disputed transaction, which is what the pipeline
// actually has. The console still serves its own fixtures by default.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/bus"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/events"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

const (
	consumerName = "case-service"
	closeTimeout = 5 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxyURL := bus.ProxyURLFromEnv("http://redpanda:8082")
	addr := getenv("CASE_SERVICE_ADDR", ":8081")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	publisher := bus.NewPublisher(proxyURL)

	srv := &http.Server{
		Addr:              addr,
		Handler:           newRouter(db, publisher),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("%s read API listening on %s", consumerName, addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// The case service both opens cases (cs.dispute.events) and closes them
	// (cs.outcomes). Outcome capture is folded in here rather than being its own
	// worker: it writes the same cases rows and the same audit trail, and nothing
	// routes traffic to it, so a separate service would add a consumer group and a
	// deployment for one INSERT and a status change.
	consumer, err := bus.NewConsumer(ctx, proxyURL, consumerName+"-group",
		[]string{bus.TopicDisputeEvents, bus.TopicOutcomes})
	if err != nil {
		log.Fatalf("consumer: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		if err := consumer.Close(closeCtx); err != nil {
			log.Printf("closing consumer: %v", err)
		}
	}()

	log.Printf("%s consuming %s, %s", consumerName, bus.TopicDisputeEvents, bus.TopicOutcomes)
	err = bus.Run(ctx, consumer, publisher, consumerName, handler(db, publisher), log.Printf)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%s stopped: %v", consumerName, err)
	}
	log.Printf("%s stopped", consumerName)
}

func handler(db *store.Store, publisher *bus.Publisher) bus.Handler {
	return func(ctx context.Context, rec bus.Record) error {
		switch rec.Topic {
		case bus.TopicOutcomes:
			return handleOutcome(ctx, db, rec)
		default:
			return handleDispute(ctx, db, publisher, rec)
		}
	}
}

func handleDispute(ctx context.Context, db *store.Store, publisher *bus.Publisher, rec bus.Record) error {
	var dispute events.Dispute
	if err := json.Unmarshal(rec.Value, &dispute); err != nil {
		return err
	}
	if dispute.DisputeID == "" {
		return errors.New("dispute event has no dispute_id")
	}

	c, inserted, err := db.UpsertCase(ctx, dispute)
	if err != nil {
		return err
	}

	// UpsertCase records PayPal's status but never touches cases.status, so a
	// dispute PayPal has closed would otherwise sit on the board waiting for a
	// merchant decision that can no longer be made.
	if err := applyPayPalResolution(ctx, db, dispute, c); err != nil {
		return err
	}

	// Ask for evidence only when the case is new. An UPDATED webhook for a
	// dispute already on the board should not rebuild the evidence bundle
	// behind the merchant's back while they are reading it. Nor is there any
	// point gathering evidence for a dispute that is already over when
	// Countersign first hears of it.
	if inserted && !isPayPalResolution(dispute) {
		request, err := json.Marshal(map[string]any{
			"case_id":        c.ID,
			"dispute_id":     c.PayPalDisputeID,
			"reason":         c.Reason,
			"transaction_id": c.TransactionID,
			"requested_at":   time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		if err := publisher.Publish(ctx, bus.TopicEvidenceRequested, c.PayPalDisputeID, request); err != nil {
			return err
		}
	}

	log.Printf("%s: case %d for dispute %s (inserted=%v)",
		consumerName, c.ID, c.PayPalDisputeID, inserted)
	return nil
}

// terminalStatuses are the case statuses that mean the dispute is over. Nothing
// is left for the merchant to do on such a case, and nothing more may be sent
// to PayPal for it.
var terminalStatuses = map[string]bool{
	"won":      true,
	"lost":     true,
	"settled":  true,
	"refunded": true,
}

// isPayPalResolution reports whether PayPal is telling us the dispute is over.
// The RESOLVED event type is the documented signal; the status check also
// catches an UPDATED event that happens to carry the resolved dispute.
func isPayPalResolution(d events.Dispute) bool {
	return d.EventType == events.TypeDisputeResolved || d.PayPalStatus == "RESOLVED"
}

// statusForPayPalOutcome maps PayPal's resource.dispute_outcome.outcome_code
// onto a terminal case status, read from the merchant's side. The second result
// is false when the code is not a ruling Countersign can stand behind.
//
// The codes and their meanings are dispute_outcome_code in PayPal's Customer
// Disputes v1 spec (developer.paypal.com/docs/api/customer-disputes/v1/):
//
//   - RESOLVED_SELLER_FAVOUR, "resolved in the merchant's favor": won.
//   - RESOLVED_BUYER_FAVOUR, "resolved in the customer's favor": lost. The
//     merchant has been debited. A merchant who conceded with accept_claim is
//     already 'refunded', which is the same result said more precisely, so
//     applyPayPalResolution keeps that rather than relabelling it a loss.
//   - CANCELED_BY_BUYER, "the customer canceled the dispute": won. The buyer
//     withdrew and the merchant keeps the money, which is the result a fight
//     is for, even though no adjudicator ruled.
//   - RESOLVED_WITH_PAYOUT, "PayPal provided the merchant or customer with
//     protection": settled. PayPal paid out of its own protection programme;
//     the spec does not say to whom, so calling it won or lost would invent a
//     ruling. It is closed, and closed without a verdict on the merits is what
//     settled means here.
//   - ACCEPTED (deprecated), "PayPal accepted the dispute": lost. Accepting the
//     buyer's dispute is finding for the buyer.
//   - DENIED (deprecated), "PayPal denied the dispute": won.
//   - NONE, "a dispute was created for the same transaction ID, and the
//     previous dispute was closed without any decision": no status. There was
//     no outcome to record, and the money is now argued over in the newer
//     dispute, which arrives as its own case.
//
// Anything else, including an empty code, also returns false: a code this
// table does not know is not evidence of who won.
func statusForPayPalOutcome(code string) (string, bool) {
	switch code {
	case "RESOLVED_SELLER_FAVOUR", "CANCELED_BY_BUYER", "DENIED":
		return "won", true
	case "RESOLVED_BUYER_FAVOUR", "ACCEPTED":
		return "lost", true
	case "RESOLVED_WITH_PAYOUT":
		return "settled", true
	default: // NONE, empty, or a code PayPal added after this was written
		return "", false
	}
}

// applyPayPalResolution closes a case PayPal reports resolved, by recording the
// outcome and moving the case to the matching terminal status.
//
// PayPal's resolution is the real one, so it overrides whatever the case says,
// including a simulated ruling the executor already recorded. It goes through
// RecordOutcome keyed on the dispute and the outcome code, so a redelivered
// webhook (or PayPal sending RESOLVED twice under different event ids) is a
// no-op, while a genuinely different outcome for the same dispute still lands.
func applyPayPalResolution(ctx context.Context, db *store.Store, d events.Dispute, c store.Case) error {
	if !isPayPalResolution(d) {
		return nil
	}
	status, ok := statusForPayPalOutcome(d.OutcomeCode)
	if !ok {
		// Do not guess. The case stays where it is, PayPal's status is already
		// on the row and in the audit trail, and the log says why it was not
		// closed.
		log.Printf("%s: dispute %s resolved by PayPal with outcome code %q, which maps to no ruling; case %d left %s",
			consumerName, d.DisputeID, d.OutcomeCode, c.ID, c.Status)
		return nil
	}
	if status == "lost" && c.Status == "refunded" {
		status = "refunded"
	}

	payload, err := json.Marshal(map[string]any{
		"source":          "paypal",
		"simulated":       false,
		"outcome_code":    d.OutcomeCode,
		"paypal_event_id": d.EventID,
		"previous_status": c.Status,
	})
	if err != nil {
		return err
	}
	inserted, err := db.RecordOutcome(ctx, store.OutcomeInput{
		CaseID:     c.ID,
		Result:     status,
		Status:     status,
		EventKey:   "paypal:" + d.DisputeID + ":resolved:" + d.OutcomeCode,
		Payload:    payload,
		AuditEvent: "PayPal resolved the dispute: " + d.OutcomeCode,
	})
	if err != nil {
		return err
	}
	if !inserted {
		log.Printf("%s: PayPal resolution %s for case %d already recorded, skipping", consumerName, d.OutcomeCode, c.ID)
		return nil
	}
	log.Printf("%s: case %d resolved by PayPal (%s) -> %s", consumerName, c.ID, d.OutcomeCode, status)
	return nil
}

// outcomeEvent is what the executor publishes on cs.outcomes: the ruling or
// settlement for a case, whether it is a real PayPal result or a labeled
// simulation, and the action it came from so the record can be authenticated.
type outcomeEvent struct {
	CaseID         int64  `json:"case_id"`
	DisputeID      string `json:"dispute_id"`
	Result         string `json:"result"`
	Simulated      bool   `json:"simulated"`
	IdempotencyKey string `json:"idempotency_key"`
	EventKey       string `json:"event_key"`
}

// handleOutcome records a ruling and advances the case to its terminal status,
// atomically and idempotently.
//
// Two guards matter here. The outcome must reference a real, recorded action on
// this case (IdempotencyKey), so a spoofed cs.outcomes event cannot drive an
// arbitrary case to a terminal state. And the write is keyed on the event so a
// redelivered outcome is a no-op rather than a second ruling and a second status
// flip.
func handleOutcome(ctx context.Context, db *store.Store, rec bus.Record) error {
	var ev outcomeEvent
	if err := json.Unmarshal(rec.Value, &ev); err != nil {
		return err
	}
	if ev.Result == "" {
		return errors.New("outcome event has no result")
	}

	caseID := ev.CaseID
	if caseID == 0 {
		if ev.DisputeID == "" {
			return errors.New("outcome event has neither case_id nor dispute_id")
		}
		c, err := db.GetCaseByDisputeID(ctx, ev.DisputeID)
		if err != nil {
			return fmt.Errorf("resolve dispute %s: %w", ev.DisputeID, err)
		}
		caseID = c.ID
	}

	// Authenticate against a recorded action for this case. A legitimate outcome
	// always carries the key of an action the executor has already marked
	// executed; one that references no such action is not trusted.
	if ev.IdempotencyKey != "" {
		act, err := db.GetAction(ctx, ev.IdempotencyKey)
		if errors.Is(err, store.ErrNotFound) || (err == nil && act.CaseID != caseID) {
			return fmt.Errorf("outcome references no action for case %d (key %q)", caseID, ev.IdempotencyKey)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	eventKey := ev.EventKey
	if eventKey == "" && ev.IdempotencyKey != "" {
		eventKey = ev.IdempotencyKey + ":" + ev.Result
	}
	status := statusForOutcome(ev.Result)

	// Once PayPal itself has closed the case, its ruling stands. The executor's
	// outcomes travel on a different topic from PayPal's webhooks, so one can
	// arrive after the other: a late "submitted" or simulated "won" is still
	// recorded, but it must not reopen or relabel a case PayPal has decided.
	current, err := db.GetCase(ctx, caseID)
	if err != nil {
		return fmt.Errorf("load case %d: %w", caseID, err)
	}
	if current.PayPalStatus == "RESOLVED" && terminalStatuses[current.Status] {
		status = ""
	}

	inserted, err := db.RecordOutcome(ctx, store.OutcomeInput{
		CaseID:     caseID,
		Result:     ev.Result,
		Status:     status,
		EventKey:   eventKey,
		Payload:    rec.Value,
		AuditEvent: "Outcome: " + ev.Result + simulatedSuffix(ev.Simulated),
	})
	if err != nil {
		return err
	}
	if !inserted {
		log.Printf("%s: outcome %q for case %d already recorded, skipping", consumerName, ev.Result, caseID)
		return nil
	}
	if status == "" {
		log.Printf("%s: case %d outcome %q (simulated=%v) recorded; status left %s, PayPal already resolved it",
			consumerName, caseID, ev.Result, ev.Simulated, current.Status)
		return nil
	}
	log.Printf("%s: case %d outcome %q (simulated=%v) -> %s", consumerName, caseID, ev.Result, ev.Simulated, status)
	return nil
}

func simulatedSuffix(simulated bool) string {
	if simulated {
		return " (simulated)"
	}
	return ""
}

// statusForOutcome maps an executor result onto the case lifecycle. A failed
// submission returns the case to decision_pending so it can be acted on again,
// rather than looking acted-upon; any other unrecognised result moves it to
// action_taken rather than leaving it looking untouched.
func statusForOutcome(result string) string {
	switch result {
	case "won":
		return "won"
	case "lost":
		return "lost"
	case "refunded":
		return "refunded"
	case "settled":
		return "settled"
	case "submit_failed":
		return "decision_pending"
	default: // submitted, under_review, acknowledged, ...
		return "action_taken"
	}
}

// --- read API ---------------------------------------------------------------

func newRouter(db *store.Store, publisher *bus.Publisher) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /cases", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		cases, err := db.ListCases(r.Context(), limit)
		if err != nil {
			serverError(w, "list cases", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cases": cases})
	})

	mux.HandleFunc("GET /cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be an integer"})
			return
		}
		c, err := db.GetCase(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "case not found"})
			return
		}
		if err != nil {
			serverError(w, "get case", err)
			return
		}

		timeline, err := db.Timeline(r.Context(), c.ID)
		if err != nil {
			serverError(w, "timeline", err)
			return
		}

		// The mandate is matched on the disputed transaction id: the demo
		// agent records its mandate against the same order id PayPal reports
		// as the seller transaction.
		var mandates []store.StoredMandate
		if c.TransactionID != "" {
			mandates, err = db.MandatesForOrder(r.Context(), c.TransactionID)
			if err != nil {
				serverError(w, "mandates", err)
				return
			}
		}
		// Say whether each mandate still verifies, rather than leaving the
		// reader to assume that a stored mandate is a valid one.
		verified := make([]map[string]any, 0, len(mandates))
		for _, sm := range mandates {
			entry := map[string]any{"mandate": sm, "verified": true}
			if err := mandate.Verify(sm.Mandate, sm.Mandate.PublicKey); err != nil {
				entry["verified"] = false
				entry["verification_error"] = err.Error()
			}
			verified = append(verified, entry)
		}

		// The list endpoint computes this flag in SQL; here the mandates are
		// already in hand, so set it from them rather than leaving the detail
		// payload claiming there is none.
		c.HasMandate = len(mandates) > 0

		// The write-path artifacts: the evidence bundle, the decision, the draft,
		// and any actions and outcomes. Each is returned as it stands — an empty
		// evidence list and a null draft are the true state of a case the evidence
		// builder has not reached yet, not something to paper over.
		evidence, err := db.EvidenceForCase(r.Context(), c.ID)
		if err != nil {
			serverError(w, "evidence", err)
			return
		}
		actions, err := db.ActionsForCase(r.Context(), c.ID)
		if err != nil {
			serverError(w, "actions", err)
			return
		}
		outcomes, err := db.OutcomesForCase(r.Context(), c.ID)
		if err != nil {
			serverError(w, "outcomes", err)
			return
		}
		decision, err := optionalDecision(r.Context(), db, c.ID)
		if err != nil {
			serverError(w, "decision", err)
			return
		}
		draft, err := optionalDraft(r.Context(), db, c.ID)
		if err != nil {
			serverError(w, "draft", err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"case":     c,
			"timeline": timeline,
			"mandates": verified,
			"evidence": evidence,
			"decision": decision,
			"draft":    draft,
			"actions":  actions,
			"outcomes": outcomes,
		})
	})

	// POST /cases/{id}/actions records a merchant action and hands it to the
	// executor on cs.action.requested. This is the console's write path: in live
	// mode the console forwards here instead of refusing with a 501.
	//
	// When INTERNAL_API_TOKEN is set, the endpoint requires it: the case service
	// is an internal service that moves money (accept_claim refunds the buyer),
	// so on a shared network it should not be callable by anything that can reach
	// its port. Unset, the check is off, which keeps the local demo (compose)
	// working without a secret.
	mux.HandleFunc("POST /cases/{id}/actions", actionHandler(db, publisher, getenv("INTERNAL_API_TOKEN", "")))

	return mux
}

// validActions are the kinds the executor knows how to carry out.
var validActions = map[string]bool{
	"provide_evidence": true,
	"make_offer":       true,
	"send_message":     true,
	"accept_claim":     true,
}

// needsGrounding are the kinds that put the draft in front of PayPal or the
// buyer. They are refused until the grounding guard has passed, so an ungrounded
// sentence cannot be submitted — the same gate the console enforces, enforced
// again server-side so the UI cannot be the only thing holding the line.
var needsGrounding = map[string]bool{
	"provide_evidence": true,
	"send_message":     true,
}

// actionBody is the request the console posts. It deliberately does not accept
// an idempotency key: the key is the primary key of the actions table and is
// not case-scoped, so honouring a client-supplied one would let a caller read
// back or suppress another case's action by colliding on it. The key is minted
// server-side instead.
type actionBody struct {
	Kind string `json:"kind"`
	Note string `json:"note"`
}

func actionHandler(db *store.Store, publisher *bus.Publisher, internalToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if internalToken != "" && subtleCompare(r.Header.Get("X-Internal-Token"), internalToken) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be an integer"})
			return
		}

		var body actionBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if !validActions[body.Kind] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action kind"})
			return
		}

		c, err := db.GetCase(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "case not found"})
			return
		}
		if err != nil {
			serverError(w, "get case for action", err)
			return
		}

		// The grounding gate: an action that submits the draft needs a draft that
		// passed the guard. No draft at all is as disqualifying as a failing one.
		if needsGrounding[body.Kind] {
			draft, err := db.GetDraft(r.Context(), id)
			if errors.Is(err, store.ErrNotFound) || (err == nil && !draft.GroundingPassed) {
				writeJSON(w, http.StatusConflict,
					map[string]string{"error": "grounding guard must pass before the draft can be sent"})
				return
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				serverError(w, "get draft for action", err)
				return
			}
		}

		// Minted server-side, so the key is always unique and the action always
		// inserts: there is no path where a caller's key collides with an existing
		// row and the read-back returns a different case's action.
		key := newIdempotencyKey()
		inserted, err := db.InsertAction(r.Context(), store.Action{
			IdempotencyKey: key, CaseID: id, Kind: body.Kind, Note: body.Note,
		})
		if err != nil {
			serverError(w, "insert action", err)
			return
		}

		if inserted {
			req, _ := json.Marshal(map[string]any{
				"idempotency_key": key,
				"case_id":         c.ID,
				"dispute_id":      c.PayPalDisputeID,
				"transaction_id":  c.TransactionID,
				"kind":            body.Kind,
				"note":            body.Note,
				"amount":          c.Amount,
				"currency":        c.Currency,
			})
			if err := publisher.Publish(r.Context(), bus.TopicActionRequested, c.PayPalDisputeID, req); err != nil {
				serverError(w, "publish action", err)
				return
			}
		}

		action, err := db.GetAction(r.Context(), key)
		if err != nil {
			serverError(w, "read back action", err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"action": action})
	}
}

// subtleCompare is a constant-time string compare returning 1 on match, so the
// token check does not leak length or content through timing.
func subtleCompare(a, b string) int {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}

// newIdempotencyKey mints a key for an action the console did not supply one
// for. A console that wants double-click safety sends its own; this is the
// fallback so the column is never empty.
func newIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("act-%d", time.Now().UnixNano())
	}
	return "act-" + hex.EncodeToString(b[:])
}

// optionalDecision returns the decision, or nil when the case has not been
// scored yet. A real database error is returned so the caller can fail the
// request with a 500 rather than serving a 200 with a null decision that the
// console would silently render as the mirror's floor estimate — masking the
// outage as "not scored yet".
func optionalDecision(ctx context.Context, db *store.Store, caseID int64) (any, error) {
	d, err := db.GetDecision(ctx, caseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// optionalDraft returns the draft, or nil when none has been written. A real
// error is returned for the same reason as optionalDecision.
func optionalDraft(ctx context.Context, db *store.Store, caseID int64) (any, error) {
	d, err := db.GetDraft(ctx, caseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write response: %v", err)
	}
}

// serverError logs the cause and returns a generic message: a database error
// string is not something to hand to a caller.
func serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %s: %v", consumerName, what, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
