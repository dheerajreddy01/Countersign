// Command gateway receives PayPal webhooks, verifies them, dedupes by event id
// and publishes the raw event to the pp.webhooks.raw topic.
package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	cfg := configFromEnv()

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           newRouter(cfg, newProxyPublisher(cfg.proxyURL), newMemoryDeduper()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("gateway listening on %s", cfg.addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type config struct {
	addr             string
	proxyURL         string
	skipVerification bool
}

func configFromEnv() config {
	return config{
		addr:             getenv("GATEWAY_ADDR", ":8080"),
		proxyURL:         getenv("REDPANDA_PROXY_URL", "http://localhost:18082"),
		skipVerification: os.Getenv("GATEWAY_SKIP_SIGNATURE_VERIFY") == "true",
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
