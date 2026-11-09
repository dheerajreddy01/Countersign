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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	{
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

		// Ask for evidence only when the case is new. An UPDATED webhook for a
		// dispute already on the board should not rebuild the evidence bundle
		// behind the merchant's back while they are reading it.
		if inserted {
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
}

// outcomeEvent is what the executor publishes on cs.outcomes: the ruling or
// settlement for a case, and whether it is a real PayPal result or a labeled
// simulation.
type outcomeEvent struct {
	CaseID    int64  `json:"case_id"`
	DisputeID string `json:"dispute_id"`
	Result    string `json:"result"`
	Simulated bool   `json:"simulated"`
}

// handleOutcome records a ruling and advances the case to its terminal status.
// The raw event is kept as the outcome payload so the audit trail shows exactly
// what the executor reported, simulation flag included.
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

	if _, err := db.InsertOutcome(ctx, caseID, ev.Result, rec.Value); err != nil {
		return err
	}
	status := statusForOutcome(ev.Result)
	if err := db.SetCaseStatus(ctx, caseID, status, consumerName,
		"Outcome: "+ev.Result, map[string]any{"result": ev.Result, "simulated": ev.Simulated}); err != nil {
		return err
	}
	log.Printf("%s: case %d outcome %q (simulated=%v) -> %s", consumerName, caseID, ev.Result, ev.Simulated, status)
	return nil
}

// statusForOutcome maps an executor result onto the case lifecycle. An
// unrecognised result still moves the case off decision_pending to action_taken
// rather than leaving it looking un-acted-upon.
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
		decision := optionalDecision(r.Context(), db, c.ID, w)
		draft := optionalDraft(r.Context(), db, c.ID, w)

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
	mux.HandleFunc("POST /cases/{id}/actions", actionHandler(db, publisher))

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

type actionBody struct {
	Kind           string `json:"kind"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func actionHandler(db *store.Store, publisher *bus.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		key := strings.TrimSpace(body.IdempotencyKey)
		if key == "" {
			key = newIdempotencyKey()
		}
		inserted, err := db.InsertAction(r.Context(), store.Action{
			IdempotencyKey: key, CaseID: id, Kind: body.Kind, Note: body.Note,
		})
		if err != nil {
			serverError(w, "insert action", err)
			return
		}

		// Publish to the executor only on first insert. A repeat is a retry of an
		// action already in flight; re-publishing would ask PayPal to do it twice.
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
		status := http.StatusAccepted
		if !inserted {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{"action": action})
	}
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

// optionalDecision returns the decision as a value or nil, treating "not scored
// yet" as nil rather than an error. A server error is reported and returns nil.
func optionalDecision(ctx context.Context, db *store.Store, caseID int64, w http.ResponseWriter) any {
	d, err := db.GetDecision(ctx, caseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		log.Printf("%s: decision for case %d: %v", consumerName, caseID, err)
		return nil
	}
	return d
}

// optionalDraft returns the draft as a value or nil when none has been written.
func optionalDraft(ctx context.Context, db *store.Store, caseID int64, w http.ResponseWriter) any {
	d, err := db.GetDraft(ctx, caseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		log.Printf("%s: draft for case %d: %v", consumerName, caseID, err)
		return nil
	}
	return d
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
