// Command gateway receives PayPal webhooks, verifies them, dedupes by event id
// and publishes the raw event to the pp.webhooks.raw topic.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	cfg := configFromEnv()

	// Build the verifier before serving: a gateway that cannot check a
	// signature would reject every delivery, and finding that out from the
	// first rejected webhook is worse than refusing to start.
	var v verifier
	if cfg.skipVerification {
		log.Print("WARNING: GATEWAY_SKIP_SIGNATURE_VERIFY=true — every POST to " +
			"/webhooks/paypal is accepted unverified. Local development only.")
	} else {
		pv, err := newPayPalVerifier(cfg)
		if err != nil {
			log.Fatalf("%v (or set GATEWAY_SKIP_SIGNATURE_VERIFY=true for local development)", err)
		}
		v = pv
		log.Printf("verifying webhooks against %s for webhook id %s", cfg.apiBase, cfg.webhookID)
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           newRouter(cfg, newProxyPublisher(cfg.proxyURL), newMemoryDeduper(), v),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Bind before announcing it: logging "listening" first makes a port clash
	// read as a successful start followed by a contradiction.
	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("gateway listening on %s", cfg.addr)
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type config struct {
	addr             string
	proxyURL         string
	skipVerification bool

	// PayPal credentials for POST /v1/notifications/verify-webhook-signature.
	apiBase      string
	clientID     string
	clientSecret string
	webhookID    string
}

func configFromEnv() config {
	return config{
		addr:             getenv("GATEWAY_ADDR", ":8080"),
		proxyURL:         proxyURLFromEnv("http://localhost:18082"),
		skipVerification: os.Getenv("GATEWAY_SKIP_SIGNATURE_VERIFY") == "true",
		apiBase:          getenv("PAYPAL_API_BASE", "https://api-m.sandbox.paypal.com"),
		clientID:         os.Getenv("PAYPAL_CLIENT_ID"),
		clientSecret:     os.Getenv("PAYPAL_CLIENT_SECRET"),
		webhookID:        os.Getenv("PAYPAL_WEBHOOK_ID"),
	}
}

// proxyURLFromEnv mirrors bus.ProxyURLFromEnv in the pipeline module: a
// platform that assigns internal hostnames at deploy time can inject
// "host:port" but cannot interpolate it into a URL, so accept either form.
func proxyURLFromEnv(fallback string) string {
	if url := strings.TrimSpace(os.Getenv("REDPANDA_PROXY_URL")); url != "" {
		return url
	}
	if hostport := strings.TrimSpace(os.Getenv("REDPANDA_PROXY_HOSTPORT")); hostport != "" {
		return "http://" + hostport
	}
	return fallback
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
