package main

import (
	"encoding/json"
	"errors"
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

func newRouter(cfg config, pub publisher, dd deduper) http.Handler {
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
			if err := verifySignature(r, body); err != nil {
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

// verifySignature must validate the PayPal transmission headers against the
// configured webhook id (POST /v1/notifications/verify-webhook-signature).
// Not implemented yet, so it fails closed.
func verifySignature(_ *http.Request, _ []byte) error {
	return errors.New("signature verification not implemented")
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
