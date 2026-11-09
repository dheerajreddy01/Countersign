// Command executor carries out the action a merchant approved.
//
// It consumes cs.action.requested, performs the matching call against the
// PayPal Disputes API — send a message, provide evidence, make an offer, accept
// the claim — records what PayPal returned on the action row, and publishes the
// result on cs.outcomes for the case service to fold into the case's lifecycle.
//
// Two honesty boundaries are built in:
//
//   - When no PayPal credentials are configured (or EXECUTOR_SIMULATE=true), the
//     executor does not pretend to call PayPal. It records the action as
//     executed-in-simulation and marks every outcome simulated:true, so a
//     reviewer is never shown a staged result dressed as a real one.
//
//   - A real submission moves a sandbox dispute to "under review"; it does not
//     return a won/lost ruling, which can take days. So the terminal ruling the
//     board needs to reach a final state is emitted separately and always marked
//     simulated:true (EXECUTOR_SIMULATE_RULING, on by default). The submission is
//     real; the verdict is labeled.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/bus"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/paypal"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

const (
	consumerName = "executor"
	closeTimeout = 5 * time.Second
)

type config struct {
	simulate     bool
	simulateRule bool
	ruling       string
	evidenceType string
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxyURL := bus.ProxyURLFromEnv("http://redpanda:8082")
	addr := getenv("EXECUTOR_ADDR", ":8086")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	pp := paypal.New(os.Getenv("PAYPAL_API_BASE"), os.Getenv("PAYPAL_CLIENT_ID"), os.Getenv("PAYPAL_CLIENT_SECRET"))
	cfg := loadConfig()
	// Simulate when explicitly asked, or when there is no PayPal client to call.
	simulate := cfg.simulate || pp == nil
	if simulate {
		log.Printf("%s: running in simulation mode (no real PayPal calls)", consumerName)
	} else {
		log.Printf("%s: PayPal Disputes API configured; submissions are real", consumerName)
	}

	srv := &http.Server{Addr: addr, Handler: healthRouter(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("%s health endpoint listening on %s", consumerName, addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	publisher := bus.NewPublisher(proxyURL)
	consumer, err := bus.NewConsumer(ctx, proxyURL, consumerName+"-group", []string{bus.TopicActionRequested})
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

	log.Printf("%s consuming %s", consumerName, bus.TopicActionRequested)
	err = bus.Run(ctx, consumer, publisher, consumerName, handler(db, pp, publisher, cfg, simulate), log.Printf)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%s stopped: %v", consumerName, err)
	}
	log.Printf("%s stopped", consumerName)
}

type actionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	CaseID         int64  `json:"case_id"`
	DisputeID      string `json:"dispute_id"`
	TransactionID  string `json:"transaction_id"`
	Kind           string `json:"kind"`
	Note           string `json:"note"`
	Amount         string `json:"amount"`
	Currency       string `json:"currency"`
}

// plan describes, per action kind, the immediate outcome of the submission and
// the terminal ruling (if any) to simulate afterwards.
type plan struct {
	immediate string // outcome result right after the submission
	terminal  string // simulated ruling to follow, or "" when the action is itself terminal
}

func plans() map[string]plan {
	return map[string]plan{
		"provide_evidence": {immediate: "submitted", terminal: "won"},
		"send_message":     {immediate: "submitted", terminal: "won"},
		"make_offer":       {immediate: "submitted", terminal: "settled"},
		"accept_claim":     {immediate: "refunded", terminal: ""}, // terminal already
	}
}

func handler(db *store.Store, pp *paypal.Client, publisher *bus.Publisher, cfg config, simulate bool) bus.Handler {
	return func(ctx context.Context, rec bus.Record) error {
		var req actionRequest
		if err := json.Unmarshal(rec.Value, &req); err != nil {
			return err
		}
		if req.Kind == "" || req.IdempotencyKey == "" {
			return errors.New("action request missing kind or idempotency_key")
		}
		p, known := plans()[req.Kind]
		if !known {
			return fmt.Errorf("unknown action kind %q", req.Kind)
		}

		// Redelivery guard: a PayPal action is not idempotent, so a record
		// redelivered after a crash (or a failed offset commit) must not re-run
		// the real call — a second accept_claim refunds the buyer again. The
		// console marks the action 'requested'; once this executor has run it,
		// the row is 'executed' or 'failed', and seeing either means the work is
		// already done.
		if existing, err := db.GetAction(ctx, req.IdempotencyKey); err == nil &&
			(existing.Status == "executed" || existing.Status == "failed") {
			log.Printf("%s: action %s already %s, skipping redelivery", consumerName, req.IdempotencyKey, existing.Status)
			return nil
		}

		// The text that actually goes to PayPal is the approved draft, falling
		// back to the merchant's note when there is no draft (e.g. accept_claim).
		text := req.Note
		if draft, err := db.GetDraft(ctx, req.CaseID); err == nil && strings.TrimSpace(draft.Text) != "" {
			text = draft.Text
		}

		result, callErr := perform(ctx, pp, cfg, simulate, req, text)
		if callErr != nil {
			// A failed submission is recorded, not retried forever: the dispute
			// stage may simply not accept this action. The merchant sees the
			// failure on the case rather than a silent stall.
			detail, _ := json.Marshal(map[string]any{"error": callErr.Error(), "simulated": simulate})
			_ = db.UpdateActionResult(ctx, req.IdempotencyKey, "failed", detail)
			if err := publishOutcome(ctx, publisher, req, "submit_failed", simulate, map[string]any{"error": callErr.Error()}); err != nil {
				return err
			}
			log.Printf("%s: action %s (%s) failed: %v", consumerName, req.IdempotencyKey, req.Kind, callErr)
			return nil
		}

		// Persist only the dispute status, not PayPal's full response body: that
		// body embeds buyer PII (name, email, dispute internals) and the action
		// detail is served back to the console. The full response is not kept.
		detail, _ := json.Marshal(map[string]any{
			"simulated":      simulate,
			"dispute_status": result.DisputeStatus,
		})
		if err := db.UpdateActionResult(ctx, req.IdempotencyKey, "executed", detail); err != nil {
			return err
		}
		if err := publishOutcome(ctx, publisher, req, p.immediate, simulate, map[string]any{"dispute_status": result.DisputeStatus}); err != nil {
			return err
		}
		log.Printf("%s: action %s (%s) executed (simulated=%v, dispute_status=%q)",
			consumerName, req.IdempotencyKey, req.Kind, simulate, result.DisputeStatus)

		// The simulated terminal ruling, so the board can reach a final state in
		// the demo. Always labeled simulated. Skipped for actions that are already
		// terminal (accept_claim) and when the operator turns it off. Published
		// inline, not after a timed sleep: sleeping here would hold the offset
		// commit and head-of-line-block every later action on the partition.
		if cfg.simulateRule && p.terminal != "" {
			// A won/lost verdict is the operator's to choose (EXECUTOR_RULING);
			// a settlement follows from making an offer, so it is not overridden.
			terminal := p.terminal
			if terminal == "won" {
				terminal = cfg.ruling
			}
			if err := publishOutcome(ctx, publisher, req, terminal, true,
				map[string]any{"note": "simulated adjudication for the demo; a real sandbox ruling can take days"}); err != nil {
				return err
			}
			log.Printf("%s: case %d simulated ruling %q published", consumerName, req.CaseID, terminal)
		}
		return nil
	}
}

// perform makes the real PayPal call for the action, or simulates it. In
// simulation it returns a synthetic Result without touching the network.
func perform(ctx context.Context, pp *paypal.Client, cfg config, simulate bool, req actionRequest, text string) (paypal.Result, error) {
	if simulate {
		return paypal.Result{DisputeStatus: "SIMULATED", Raw: json.RawMessage(`{"simulated":true}`)}, nil
	}
	switch req.Kind {
	case "provide_evidence":
		return pp.ProvideEvidence(ctx, req.DisputeID, cfg.evidenceType, text)
	case "send_message":
		return pp.SendMessage(ctx, req.DisputeID, text)
	case "make_offer":
		return pp.MakeOffer(ctx, req.DisputeID, req.Amount, req.Currency, text, "REFUND")
	case "accept_claim":
		return pp.AcceptClaim(ctx, req.DisputeID, text)
	default:
		return paypal.Result{}, fmt.Errorf("unknown action kind %q", req.Kind)
	}
}

func publishOutcome(ctx context.Context, publisher *bus.Publisher, req actionRequest, result string, simulated bool, extra map[string]any) error {
	payload := map[string]any{
		"case_id":         req.CaseID,
		"dispute_id":      req.DisputeID,
		"result":          result,
		"simulated":       simulated,
		"action_kind":     req.Kind,
		"idempotency_key": req.IdempotencyKey,
		// Unique per (action, result), so the case service can dedupe a
		// redelivered outcome without re-recording it or re-flipping the status.
		"event_key": req.IdempotencyKey + ":" + result,
	}
	for k, v := range extra {
		payload[k] = v
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return publisher.Publish(ctx, bus.TopicOutcomes, req.DisputeID, encoded)
}

func loadConfig() config {
	return config{
		simulate:     boolEnv("EXECUTOR_SIMULATE", false),
		simulateRule: boolEnv("EXECUTOR_SIMULATE_RULING", true),
		ruling:       getenv("EXECUTOR_RULING", "won"),
		evidenceType: getenv("EXECUTOR_EVIDENCE_TYPE", "PROOF_OF_FULFILLMENT"),
	}
}

func healthRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
