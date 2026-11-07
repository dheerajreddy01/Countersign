package main

import "testing"

// The gateway carries its own copy of this logic because it is a separate Go
// module; the two must stay in step.
func TestProxyURLFromEnv(t *testing.T) {
	const fallback = "http://localhost:18082"

	t.Run("explicit url wins", func(t *testing.T) {
		t.Setenv("REDPANDA_PROXY_URL", "http://proxy.internal:8082")
		t.Setenv("REDPANDA_PROXY_HOSTPORT", "ignored:9999")
		if got := proxyURLFromEnv(fallback); got != "http://proxy.internal:8082" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("hostport becomes a url", func(t *testing.T) {
		t.Setenv("REDPANDA_PROXY_URL", "")
		t.Setenv("REDPANDA_PROXY_HOSTPORT", "countersign-redpanda-a1b2:8082")
		if got := proxyURLFromEnv(fallback); got != "http://countersign-redpanda-a1b2:8082" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("blank values fall back", func(t *testing.T) {
		t.Setenv("REDPANDA_PROXY_URL", "")
		t.Setenv("REDPANDA_PROXY_HOSTPORT", "  ")
		if got := proxyURLFromEnv(fallback); got != fallback {
			t.Errorf("got %q, want the fallback", got)
		}
	})
}
