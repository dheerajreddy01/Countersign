package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
)

const rawTopic = "pp.webhooks.raw"

type publisher interface {
	Publish(topic, key string, value []byte) error
}

type deduper interface {
	// Seen records the id and reports whether it had been seen before.
	Seen(id string) bool
}

// verifier confirms a delivery really came from PayPal, signed for this
// webhook id. Injected so the handler can be tested without reaching PayPal.
type verifier interface {
	Verify(ctx context.Context, header http.Header, body []byte) error
}

func newRouter(cfg config, pub publisher, dd deduper, v verifier) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /webhooks/paypal", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		if !cfg.skipVerification {
			if v == nil {
				// Refusing is the only safe answer: without a verifier there is
				// no way to tell a PayPal delivery from anyone else's POST.
				log.Print("webhook rejected: no verifier configured")
				http.Error(w, "signature verification unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := v.Verify(r.Context(), r.Header, body); err != nil {
				log.Printf("webhook rejected: %v", err)
				http.Error(w, "signature verification failed", http.StatusUnauthorized)
				return
			}
		}

		var evt struct {
			ID        string `json:"id"`
			EventType string `json:"event_type"`
		}
		if err := json.Unmarshal(body, &evt); err != nil || evt.ID == "" {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}

		if dd.Seen(evt.ID) {
			w.WriteHeader(http.StatusOK) // duplicate delivery; already handled
			return
		}

		if err := pub.Publish(rawTopic, evt.ID, body); err != nil {
			log.Printf("publish failed for %s: %v", evt.ID, err)
			http.Error(w, "publish failed", http.StatusBadGateway) // PayPal will retry
			return
		}

		log.Printf("accepted %s (%s)", evt.ID, evt.EventType)
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

type memoryDeduper struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newMemoryDeduper() *memoryDeduper {
	return &memoryDeduper{seen: map[string]struct{}{}}
}

func (m *memoryDeduper) Seen(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.seen[id]; ok {
		return true
	}
	m.seen[id] = struct{}{}
	return false
}
