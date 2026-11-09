// Command evidencebuilder is the step between a new case and a decision the
// merchant can act on.
//
// It consumes cs.evidence.requested, assembles the evidence bundle for the
// dispute (internal/evidence), then asks the AI service to score the case and
// draft a grounded rebuttal, and persists all of it. The case service that
// requested the evidence serves the result back to the console.
//
// Evidence, scoring and drafting are one worker rather than three because the
// steps are strictly sequential for a case — a draft cites the evidence, and the
// score reads it — and folding them removes two consumer groups and two network
// hops for no loss of fidelity: the stages are still distinct topics on the way
// in (cs.evidence.requested) and out (cs.case.ready, cs.decision.pending).
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
	"strconv"
	"syscall"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/aiclient"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/bus"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/evidence"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

const (
	consumerName = "evidence-builder"
	closeTimeout = 5 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxyURL := bus.ProxyURLFromEnv("http://redpanda:8082")
	addr := getenv("EVIDENCE_BUILDER_ADDR", ":8085")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	ai := aiclient.New(aiURLFromEnv())
	if ai == nil {
		// Not fatal: the bundle is still assembled and the case still reaches
		// the board. The merchant simply sees evidence without a score or draft,
		// which is the honest state when no scorer is configured.
		log.Printf("%s: AI_SERVICE_URL unset — evidence will be built but not scored or drafted", consumerName)
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
	consumer, err := bus.NewConsumer(ctx, proxyURL, consumerName+"-group", []string{bus.TopicEvidenceRequested})
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

	log.Printf("%s consuming %s", consumerName, bus.TopicEvidenceRequested)
	err = bus.Run(ctx, consumer, publisher, consumerName, handler(db, ai, publisher), log.Printf)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%s stopped: %v", consumerName, err)
	}
	log.Printf("%s stopped", consumerName)
}

type evidenceRequest struct {
	CaseID    int64  `json:"case_id"`
	DisputeID string `json:"dispute_id"`
}

func handler(db *store.Store, ai *aiclient.Client, publisher *bus.Publisher) bus.Handler {
	return func(ctx context.Context, rec bus.Record) error {
		var req evidenceRequest
		if err := json.Unmarshal(rec.Value, &req); err != nil {
			return err
		}
		if req.CaseID == 0 {
			return errors.New("evidence request has no case_id")
		}

		c, err := db.GetCase(ctx, req.CaseID)
		if err != nil {
			return fmt.Errorf("load case %d: %w", req.CaseID, err)
		}

		var mandates []store.StoredMandate
		if c.TransactionID != "" {
			if mandates, err = db.MandatesForOrder(ctx, c.TransactionID); err != nil {
				return fmt.Errorf("load mandates for %s: %w", c.TransactionID, err)
			}
		}

		records, signals := evidence.Build(c, mandates, time.Now().UTC())
		if err := db.ReplaceEvidence(ctx, c.ID, records); err != nil {
			return err
		}
		if err := db.SetCaseStatus(ctx, c.ID, "evidence_ready", consumerName,
			"Case evidence ready", map[string]any{"evidence_count": len(records)}); err != nil {
			return err
		}
		ready, _ := json.Marshal(map[string]any{
			"case_id": c.ID, "dispute_id": c.PayPalDisputeID, "evidence_count": len(records),
		})
		if err := publisher.Publish(ctx, bus.TopicCaseReady, c.PayPalDisputeID, ready); err != nil {
			return err
		}
		log.Printf("%s: case %d evidence assembled (%d records)", consumerName, c.ID, len(records))

		// Scoring and drafting need the AI service. When it is absent or
		// unreachable the case stays at evidence_ready rather than being
		// dead-lettered: the evidence is real and persisted, and losing it over a
		// transient scorer outage would be the wrong trade.
		if ai == nil {
			return nil
		}
		if err := scoreAndDraft(ctx, db, ai, publisher, c, signals); err != nil {
			log.Printf("%s: case %d scored/drafted partially: %v", consumerName, c.ID, err)
		}
		return nil
	}
}

func scoreAndDraft(ctx context.Context, db *store.Store, ai *aiclient.Client, publisher *bus.Publisher, c store.Case, sig evidence.Signals) error {
	amount, _ := strconv.ParseFloat(c.Amount, 64)

	score, err := ai.Score(ctx, aiclient.ScoreRequest{
		Amount:               amount,
		Reason:               c.Reason,
		DeliveryConfirmed:    sig.DeliveryConfirmed,
		SignatureCaptured:    sig.SignatureCaptured,
		MandateMatching:      sig.MandateMatching,
		PriorCustomerContact: sig.PriorCustomerContact,
		DaysToDeadline:       sig.DaysToDeadline,
	})
	if err != nil {
		return fmt.Errorf("score: %w", err)
	}
	factors, _ := json.Marshal(map[string]any{
		"p_win":            score.PWin,
		"ev_fight":         score.EVFight,
		"refund_cost":      score.RefundCost,
		"recommendation":   score.Recommendation,
		"top_factors":      topFactors(score.TopFactors),
		"fraud_likelihood": score.FraudLikelihood,
		"signals": map[string]any{
			"delivery_confirmed":     sig.DeliveryConfirmed,
			"signature_captured":     sig.SignatureCaptured,
			"mandate_matching":       sig.MandateMatching,
			"prior_customer_contact": sig.PriorCustomerContact,
			"days_to_deadline":       sig.DaysToDeadline,
		},
	})
	if err := db.SaveDecision(ctx, c.ID, factors, strconv.FormatFloat(score.EVFight, 'f', 2, 64), score.Recommendation); err != nil {
		return err
	}

	records, err := db.EvidenceForCase(ctx, c.ID)
	if err != nil {
		return err
	}
	summaries := evidence.Summaries(records)
	items := make([]aiclient.Evidence, 0, len(summaries))
	for _, s := range summaries {
		items = append(items, aiclient.Evidence{ID: s.ID, Type: s.Type, Summary: s.Summary})
	}
	draft, err := ai.Draft(ctx, aiclient.DraftRequest{Reason: c.Reason, Evidence: items})
	if err != nil {
		return fmt.Errorf("draft: %w", err)
	}
	citations, _ := json.Marshal(draft.Citations)
	uncited, _ := json.Marshal(draft.UncitedSentences)
	if err := db.SaveDraft(ctx, store.Draft{
		CaseID:           c.ID,
		Text:             draft.Text,
		Citations:        citations,
		GroundingPassed:  draft.GroundingPassed,
		UncitedSentences: uncited,
		Source:           draft.Source,
	}); err != nil {
		return err
	}

	if err := db.SetCaseStatus(ctx, c.ID, "decision_pending", consumerName,
		"Scored and drafted: "+score.Recommendation,
		map[string]any{"recommendation": score.Recommendation, "grounding_passed": draft.GroundingPassed, "draft_source": draft.Source}); err != nil {
		return err
	}
	pending, _ := json.Marshal(map[string]any{
		"case_id": c.ID, "dispute_id": c.PayPalDisputeID,
		"recommendation": score.Recommendation, "grounding_passed": draft.GroundingPassed,
	})
	if err := publisher.Publish(ctx, bus.TopicDecisionPending, c.PayPalDisputeID, pending); err != nil {
		return err
	}
	log.Printf("%s: case %d scored (%s, p_win=%.2f) and drafted (grounded=%v, source=%s)",
		consumerName, c.ID, score.Recommendation, score.PWin, draft.GroundingPassed, draft.Source)
	return nil
}

type factor struct {
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
}

// topFactors flattens the AI service's [name, weight] pairs into named objects
// the console can render without positional indexing.
func topFactors(pairs [][]any) []factor {
	out := make([]factor, 0, len(pairs))
	for _, p := range pairs {
		if len(p) != 2 {
			continue
		}
		name, _ := p[0].(string)
		weight, _ := p[1].(float64)
		out = append(out, factor{Name: name, Weight: weight})
	}
	return out
}

func healthRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func aiURLFromEnv() string {
	if u := os.Getenv("AI_SERVICE_URL"); u != "" {
		return u
	}
	if hp := os.Getenv("AI_SERVICE_HOSTPORT"); hp != "" {
		return "http://" + hp
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
