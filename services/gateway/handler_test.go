package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePublisher struct{ published []string }

// stubVerifier answers without reaching PayPal; verify_test.go covers the real
// verifier against a stand-in for its two endpoints.
type stubVerifier struct {
	err   error
	calls int
}

func (s *stubVerifier) Verify(_ context.Context, _ http.Header, _ []byte) error {
	s.calls++
	return s.err
}

func (f *fakePublisher) Publish(_, key string, _ []byte) error {
	f.published = append(f.published, key)
	return nil
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/paypal", strings.NewReader(body)))
	return rec
}

func TestRejectsWhenVerificationFails(t *testing.T) {
	pub := &fakePublisher{}
	stub := &stubVerifier{err: errors.New("not signed for this webhook id")}
	h := newRouter(config{}, pub, newMemoryDeduper(), stub)

	if rec := post(h, `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Fatal("event must not be published when verification fails")
	}
}

func TestPublishesWhatVerifies(t *testing.T) {
	pub := &fakePublisher{}
	stub := &stubVerifier{}
	h := newRouter(config{}, pub, newMemoryDeduper(), stub)

	if rec := post(h, `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`); rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if stub.calls != 1 {
		t.Fatalf("verifier called %d times, want 1", stub.calls)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
}

// Without a verifier and without the skip flag there is no way to tell a
// PayPal delivery from anyone else's POST, so the gateway must refuse rather
// than accept or pretend to check.
func TestRefusesWhenNoVerifierIsConfigured(t *testing.T) {
	pub := &fakePublisher{}
	h := newRouter(config{}, pub, newMemoryDeduper(), nil)

	if rec := post(h, `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Fatal("nothing may be published without verification")
	}
}

// The skip flag is the documented local-development escape hatch, so it must
// bypass verification entirely rather than consult a verifier.
func TestSkipFlagBypassesTheVerifier(t *testing.T) {
	pub := &fakePublisher{}
	stub := &stubVerifier{err: errors.New("would reject")}
	h := newRouter(config{skipVerification: true}, pub, newMemoryDeduper(), stub)

	if rec := post(h, `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`); rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if stub.calls != 0 {
		t.Fatal("the verifier must not be consulted when verification is skipped")
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
}

func TestDedupesByEventID(t *testing.T) {
	pub := &fakePublisher{}
	h := newRouter(config{skipVerification: true}, pub, newMemoryDeduper(), nil)

	body := `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`
	post(h, body)
	post(h, body)

	if len(pub.published) != 1 {
		t.Fatalf("published %d times, want 1", len(pub.published))
	}
}

func TestRejectsEventWithoutID(t *testing.T) {
	h := newRouter(config{skipVerification: true}, &fakePublisher{}, newMemoryDeduper(), nil)

	if rec := post(h, `{"event_type":"X"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}
