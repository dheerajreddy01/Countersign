package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePublisher struct{ published []string }

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
	h := newRouter(config{}, pub, newMemoryDeduper())

	if rec := post(h, `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Fatal("event must not be published when verification fails")
	}
}

func TestDedupesByEventID(t *testing.T) {
	pub := &fakePublisher{}
	h := newRouter(config{skipVerification: true}, pub, newMemoryDeduper())

	body := `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED"}`
	post(h, body)
	post(h, body)

	if len(pub.published) != 1 {
		t.Fatalf("published %d times, want 1", len(pub.published))
	}
}

func TestRejectsEventWithoutID(t *testing.T) {
	h := newRouter(config{skipVerification: true}, &fakePublisher{}, newMemoryDeduper())

	if rec := post(h, `{"event_type":"X"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}
