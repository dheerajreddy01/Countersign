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
	"encoding/json"
	"errors"
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

	proxyURL := getenv("REDPANDA_PROXY_URL", "http://redpanda:8082")
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
		Handler:           newRouter(db),
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

	consumer, err := bus.NewConsumer(ctx, proxyURL, consumerName+"-group", []string{bus.TopicDisputeEvents})
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

	log.Printf("%s consuming %s", consumerName, bus.TopicDisputeEvents)
	err = bus.Run(ctx, consumer, publisher, consumerName, handler(db, publisher), log.Printf)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%s stopped: %v", consumerName, err)
	}
	log.Printf("%s stopped", consumerName)
}

func handler(db *store.Store, publisher *bus.Publisher) bus.Handler {
	return func(ctx context.Context, rec bus.Record) error {
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

// --- read API ---------------------------------------------------------------

func newRouter(db *store.Store) http.Handler {
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

		writeJSON(w, http.StatusOK, map[string]any{
			"case":     c,
			"timeline": timeline,
			"mandates": verified,
		})
	})

	return mux
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
