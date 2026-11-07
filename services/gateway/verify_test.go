package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePayPal stands in for the two endpoints the verifier uses: the
// client-credentials token endpoint and verify-webhook-signature.
type fakePayPal struct {
	mu sync.Mutex

	tokenCalls  int
	verifyCalls int

	// status returned by verify-webhook-signature.
	status string
	// httpStatus overrides the verify response code when non-zero.
	httpStatus int
	// rejectTokensBefore makes verify answer 401 until this many tokens have
	// been issued, which is how an expired cached token behaves.
	rejectTokensBefore int

	// lastEvent is the raw webhook_event as it arrived, and lastBody the whole
	// request, so a test can assert the body was not re-encoded.
	lastEvent     json.RawMessage
	lastWebhookID string
	lastAuthZ     string
}

func (f *fakePayPal) server() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user == "" || pass == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.tokenCalls++
		issued := f.tokenCalls
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-` +
			string(rune('0'+issued)) + `","expires_in":32400}`))
	})

	mux.HandleFunc("POST /v1/notifications/verify-webhook-signature",
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				WebhookEvent json.RawMessage `json:"webhook_event"`
				WebhookID    string          `json:"webhook_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			f.mu.Lock()
			f.verifyCalls++
			calls := f.verifyCalls
			f.lastEvent = body.WebhookEvent
			f.lastWebhookID = body.WebhookID
			f.lastAuthZ = r.Header.Get("Authorization")
			tokens := f.tokenCalls
			status, httpStatus, rejectBefore := f.status, f.httpStatus, f.rejectTokensBefore
			f.mu.Unlock()
			_ = calls

			if tokens < rejectBefore {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if httpStatus != 0 {
				w.WriteHeader(httpStatus)
				_, _ = w.Write([]byte(`{"name":"INVALID_RESOURCE_ID","message":"id invalid"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"verification_status":"` + status + `"}`))
		})

	return httptest.NewServer(mux)
}

func (f *fakePayPal) counts() (tokens, verifies int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls, f.verifyCalls
}

func signedHeaders() http.Header {
	h := http.Header{}
	h.Set(headerTransmissionID, "d4a7c2e0-1111-11ef-9a1b-0242ac120002")
	h.Set(headerTransmissionTime, "2026-11-07T01:30:00Z")
	h.Set(headerTransmissionSig, "Rk9SR0VE")
	h.Set(headerCertURL, "https://api.sandbox.paypal.com/v1/notifications/certs/CERT-abc")
	h.Set(headerAuthAlgo, "SHA256withRSA")
	return h
}

func verifierFor(t *testing.T, base string) *paypalVerifier {
	t.Helper()
	v, err := newPayPalVerifier(config{
		apiBase:      base,
		clientID:     "client-id",
		clientSecret: "client-secret",
		webhookID:    "WH-TEST-ID",
	})
	if err != nil {
		t.Fatalf("newPayPalVerifier: %v", err)
	}
	return v
}

func TestVerifierAcceptsWhatPayPalConfirms(t *testing.T) {
	fake := &fakePayPal{status: "SUCCESS"}
	srv := fake.server()
	defer srv.Close()

	// Deliberately awkward spacing, and characters encoding/json would escape
	// to <-style sequences. The signature is over the bytes PayPal sent,
	// so re-encoding the body breaks verification for genuine deliveries.
	body := []byte(`{"event_type":"CUSTOMER.DISPUTE.CREATED",  "id":"WH-1",` +
		`"summary":"buyer says item < described & not received"}`)

	if err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(), body); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if string(fake.lastEvent) != string(body) {
		t.Errorf("webhook_event was altered:\n got %s\nwant %s", fake.lastEvent, body)
	}
	if fake.lastWebhookID != "WH-TEST-ID" {
		t.Errorf("webhook_id = %q, want the configured id", fake.lastWebhookID)
	}
	if fake.lastAuthZ != "Bearer tok-1" {
		t.Errorf("Authorization = %q, want the fetched bearer token", fake.lastAuthZ)
	}
}

func TestVerifierRejectsWhatPayPalDoesNotConfirm(t *testing.T) {
	fake := &fakePayPal{status: "FAILURE"}
	srv := fake.server()
	defer srv.Close()

	err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(),
		[]byte(`{"id":"WH-1"}`))
	if err == nil {
		t.Fatal("a FAILURE status must not verify")
	}
	if !strings.Contains(err.Error(), "FAILURE") {
		t.Errorf("error should name the status, got: %v", err)
	}
}

func TestVerifierRejectsMissingHeadersWithoutCallingPayPal(t *testing.T) {
	fake := &fakePayPal{status: "SUCCESS"}
	srv := fake.server()
	defer srv.Close()
	v := verifierFor(t, srv.URL)

	for _, missing := range []string{
		headerTransmissionID, headerTransmissionTime, headerTransmissionSig,
		headerCertURL, headerAuthAlgo,
	} {
		headers := signedHeaders()
		headers.Del(missing)
		err := v.Verify(context.Background(), headers, []byte(`{"id":"WH-1"}`))
		if err == nil {
			t.Errorf("missing %s must not verify", missing)
		}
	}

	if tokens, verifies := fake.counts(); tokens != 0 || verifies != 0 {
		t.Errorf("PayPal was called %d/%d times for unusable deliveries", tokens, verifies)
	}
}

func TestVerifierRejectsForeignCertURL(t *testing.T) {
	fake := &fakePayPal{status: "SUCCESS"}
	srv := fake.server()
	defer srv.Close()
	v := verifierFor(t, srv.URL)

	for _, certURL := range []string{
		"https://paypal.com.attacker.example/certs/CERT-abc",
		"http://api.sandbox.paypal.com/certs/CERT-abc",
		"https://notpaypal.com/certs/CERT-abc",
		"::not a url::",
	} {
		headers := signedHeaders()
		headers.Set(headerCertURL, certURL)
		if err := v.Verify(context.Background(), headers, []byte(`{"id":"WH-1"}`)); err == nil {
			t.Errorf("cert_url %q must not verify", certURL)
		}
	}

	if tokens, verifies := fake.counts(); tokens != 0 || verifies != 0 {
		t.Errorf("PayPal was called %d/%d times for a foreign cert_url", tokens, verifies)
	}
}

func TestVerifierRejectsNonJSONBody(t *testing.T) {
	fake := &fakePayPal{status: "SUCCESS"}
	srv := fake.server()
	defer srv.Close()

	if err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(),
		[]byte("not json")); err == nil {
		t.Fatal("a non-JSON body must not verify")
	}
}

func TestVerifierRejectsWhenPayPalErrors(t *testing.T) {
	fake := &fakePayPal{httpStatus: http.StatusBadRequest}
	srv := fake.server()
	defer srv.Close()

	err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(),
		[]byte(`{"id":"WH-1"}`))
	if err == nil {
		t.Fatal("an error from PayPal must not verify")
	}
	// The field PayPal names is what makes a wrong webhook id diagnosable.
	if !strings.Contains(err.Error(), "INVALID_RESOURCE_ID") {
		t.Errorf("error should carry PayPal's detail, got: %v", err)
	}
}

func TestVerifierRejectsWhenPayPalIsUnreachable(t *testing.T) {
	srv := (&fakePayPal{status: "SUCCESS"}).server()
	base := srv.URL
	srv.Close() // nothing is listening now

	if err := verifierFor(t, base).Verify(context.Background(), signedHeaders(),
		[]byte(`{"id":"WH-1"}`)); err == nil {
		t.Fatal("an unreachable PayPal must fail closed, not open")
	}
}

func TestVerifierReusesItsToken(t *testing.T) {
	fake := &fakePayPal{status: "SUCCESS"}
	srv := fake.server()
	defer srv.Close()
	v := verifierFor(t, srv.URL)

	for i := 0; i < 3; i++ {
		if err := v.Verify(context.Background(), signedHeaders(), []byte(`{"id":"WH-1"}`)); err != nil {
			t.Fatalf("Verify %d: %v", i, err)
		}
	}

	tokens, verifies := fake.counts()
	if tokens != 1 {
		t.Errorf("fetched %d tokens for 3 deliveries, want 1", tokens)
	}
	if verifies != 3 {
		t.Errorf("made %d verify calls, want 3", verifies)
	}
}

func TestVerifierRefetchesARejectedToken(t *testing.T) {
	// The first token is refused, as a cached token past its expiry would be.
	fake := &fakePayPal{status: "SUCCESS", rejectTokensBefore: 2}
	srv := fake.server()
	defer srv.Close()

	if err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(),
		[]byte(`{"id":"WH-1"}`)); err != nil {
		t.Fatalf("Verify should recover from a stale token: %v", err)
	}

	tokens, verifies := fake.counts()
	if tokens != 2 {
		t.Errorf("fetched %d tokens, want 2 (one stale, one fresh)", tokens)
	}
	if verifies != 2 {
		t.Errorf("made %d verify calls, want 2 (one 401, one retry)", verifies)
	}
}

func TestVerifierRetriesOnlyOnce(t *testing.T) {
	// Every token is refused: the retry must not loop.
	fake := &fakePayPal{status: "SUCCESS", rejectTokensBefore: 99}
	srv := fake.server()
	defer srv.Close()

	if err := verifierFor(t, srv.URL).Verify(context.Background(), signedHeaders(),
		[]byte(`{"id":"WH-1"}`)); err == nil {
		t.Fatal("persistent 401s must fail, not verify")
	}

	if _, verifies := fake.counts(); verifies != 2 {
		t.Errorf("made %d verify calls, want 2 (the call and one retry)", verifies)
	}
}

func TestNewPayPalVerifierNeedsCredentials(t *testing.T) {
	full := config{
		apiBase:      "https://api-m.sandbox.paypal.com",
		clientID:     "id",
		clientSecret: "secret",
		webhookID:    "WH-1",
	}
	for name, mangle := range map[string]func(config) config{
		"no client id":     func(c config) config { c.clientID = ""; return c },
		"no client secret": func(c config) config { c.clientSecret = ""; return c },
		"no webhook id":    func(c config) config { c.webhookID = ""; return c },
	} {
		if _, err := newPayPalVerifier(mangle(full)); !errors.Is(err, errMissingConfig) {
			t.Errorf("%s: got %v, want errMissingConfig", name, err)
		}
	}
	if _, err := newPayPalVerifier(full); err != nil {
		t.Errorf("complete config should build: %v", err)
	}
}
