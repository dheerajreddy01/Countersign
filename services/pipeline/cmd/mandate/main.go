// Command mandate is the agent-order mandate ledger.
//
// POST /mandates records what a user asked an agent to buy, signs it, and
// links it to the previous record. GET /mandates and GET /chain read it back,
// each answer saying whether the records still verify.
//
// This is the evidence behind Countersign's answer to an "unauthorised" claim
// on an agent-placed order: the user's own instruction, captured at order time
// and signed, rather than a merchant's later account of it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

const (
	serviceName  = "mandate-service"
	closeTimeout = 5 * time.Second
	maxBody      = 1 << 20
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := getenv("MANDATE_SERVICE_ADDR", ":8082")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	signer, err := loadSigner()
	if err != nil {
		log.Fatalf("signing key: %v", err)
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           newRouter(db, signer),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("%s listening on %s (public key %s)", serviceName, addr, signer.PublicKey())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}
	log.Printf("%s stopped", serviceName)
}

// loadSigner reads MANDATE_SIGNING_KEY, generating a key only if it is unset.
//
// A generated key is logged loudly because it is not a working setup: every
// restart would start a new chain, and mandates signed by the old key could no
// longer be verified against the new one. It exists so `docker compose up`
// works out of the box for the demo.
func loadSigner() (*mandate.Signer, error) {
	if encoded := strings.TrimSpace(os.Getenv("MANDATE_SIGNING_KEY")); encoded != "" {
		return mandate.NewSignerFromBase64(encoded)
	}
	signer, encoded, err := mandate.GenerateSigner()
	if err != nil {
		return nil, err
	}
	log.Printf("WARNING: MANDATE_SIGNING_KEY is unset, so a throwaway key was generated. "+
		"Mandates signed now cannot be verified after a restart. Set this to keep one chain:\n"+
		"  MANDATE_SIGNING_KEY=%s", encoded)
	return signer, nil
}

func newRouter(db *store.Store, signer *mandate.Signer) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// The public key is served so a reviewer can verify the ledger without
	// being able to sign anything.
	mux.HandleFunc("GET /public-key", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"public_key": signer.PublicKey()})
	})

	mux.HandleFunc("POST /mandates", func(w http.ResponseWriter, r *http.Request) {
		var payload mandate.Payload
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		// Link to the current head of this key's chain.
		head, err := db.LatestMandateHash(r.Context(), signer.PublicKey())
		if err != nil {
			serverError(w, "read chain head", err)
			return
		}
		m, err := signer.Sign(payload, head)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		stored, err := db.InsertMandate(r.Context(), m)
		if err != nil {
			serverError(w, "store mandate", err)
			return
		}

		log.Printf("%s: mandate %d recorded for order %s", serviceName, stored.ID, stored.OrderID)
		writeJSON(w, http.StatusCreated, stored)
	})

	mux.HandleFunc("GET /mandates", func(w http.ResponseWriter, r *http.Request) {
		orderID := strings.TrimSpace(r.URL.Query().Get("order_id"))
		if orderID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "order_id is required"})
			return
		}
		stored, err := db.MandatesForOrder(r.Context(), orderID)
		if err != nil {
			serverError(w, "read mandates", err)
			return
		}
		out := make([]map[string]any, 0, len(stored))
		for _, sm := range stored {
			entry := map[string]any{"mandate": sm, "verified": true}
			if err := mandate.Verify(sm.Mandate, sm.Mandate.PublicKey); err != nil {
				entry["verified"] = false
				entry["verification_error"] = err.Error()
			}
			out = append(out, entry)
		}
		writeJSON(w, http.StatusOK, map[string]any{"mandates": out})
	})

	// GET /chain verifies the whole ledger: every signature, and every link.
	// This is what demonstrates that a record cannot be removed, reordered or
	// back-dated into the ledger after a dispute is opened.
	mux.HandleFunc("GET /chain", func(w http.ResponseWriter, r *http.Request) {
		publicKey := strings.TrimSpace(r.URL.Query().Get("public_key"))
		if publicKey == "" {
			publicKey = signer.PublicKey()
		}
		stored, err := db.MandateChain(r.Context(), publicKey)
		if err != nil {
			serverError(w, "read chain", err)
			return
		}
		plain := make([]mandate.Mandate, 0, len(stored))
		for _, sm := range stored {
			plain = append(plain, sm.Mandate)
		}

		body := map[string]any{
			"public_key": publicKey,
			"length":     len(plain),
			"mandates":   stored,
			"valid":      true,
		}
		if err := mandate.VerifyChain(plain, publicKey); err != nil {
			body["valid"] = false
			body["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, body)
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

func serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %s: %v", serviceName, what, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
