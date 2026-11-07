package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// paypalVerifier checks a delivery against PayPal's
// POST /v1/notifications/verify-webhook-signature endpoint.
//
// PayPal signs each webhook with a certificate it publishes, and the documented
// way to check one is to hand the five transmission headers, the configured
// webhook id and the exact body back to PayPal and let it answer SUCCESS or
// FAILURE. Verifying offline would mean fetching and trust-checking that
// certificate, caching it, handling rotation, and reimplementing the
// canonical-form rules — more moving parts to get subtly wrong, for a service
// that already depends on PayPal being reachable.
//
// The body must be passed through byte for byte. Re-encoding it changes key
// order and whitespace, and the signature is over the bytes PayPal sent.
type paypalVerifier struct {
	apiBase      string
	clientID     string
	clientSecret string
	webhookID    string
	client       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// The transmission headers PayPal sends. All five are required: a delivery
// missing any of them cannot be checked, so it is rejected without spending a
// call on it.
const (
	headerTransmissionID   = "PAYPAL-TRANSMISSION-ID"
	headerTransmissionTime = "PAYPAL-TRANSMISSION-TIME"
	headerTransmissionSig  = "PAYPAL-TRANSMISSION-SIG"
	headerCertURL          = "PAYPAL-CERT-URL"
	headerAuthAlgo         = "PAYPAL-AUTH-ALGO"
)

// Tokens last 9 hours; refresh a minute early rather than racing the expiry.
const tokenRefreshMargin = time.Minute

var errMissingConfig = errors.New(
	"signature verification needs PAYPAL_CLIENT_ID, PAYPAL_CLIENT_SECRET and PAYPAL_WEBHOOK_ID")

func newPayPalVerifier(cfg config) (*paypalVerifier, error) {
	if cfg.clientID == "" || cfg.clientSecret == "" || cfg.webhookID == "" {
		return nil, errMissingConfig
	}
	return &paypalVerifier{
		apiBase:      strings.TrimRight(cfg.apiBase, "/"),
		clientID:     cfg.clientID,
		clientSecret: cfg.clientSecret,
		webhookID:    cfg.webhookID,
		client:       &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Verify reports nil when PayPal confirms the delivery was signed for this
// webhook id. Every other outcome is an error: a verifier that cannot reach
// PayPal must not wave the event through.
func (v *paypalVerifier) Verify(ctx context.Context, header http.Header, body []byte) error {
	headers := map[string]string{}
	for _, name := range []string{
		headerTransmissionID, headerTransmissionTime, headerTransmissionSig,
		headerCertURL, headerAuthAlgo,
	} {
		value := strings.TrimSpace(header.Get(name))
		if value == "" {
			return fmt.Errorf("missing %s header", name)
		}
		headers[name] = value
	}

	// The certificate is fetched by PayPal, not by us, but a cert_url pointing
	// somewhere else is a sign the delivery is not what it claims to be.
	if err := checkCertURL(headers[headerCertURL]); err != nil {
		return err
	}

	if !json.Valid(body) {
		return errors.New("body is not valid JSON")
	}

	payload, err := verificationRequest(headers, v.webhookID, body)
	if err != nil {
		return err
	}

	status, err := v.ask(ctx, payload, false)
	if err != nil {
		return err
	}
	if status != "SUCCESS" {
		return fmt.Errorf("PayPal reported verification_status %q", status)
	}
	return nil
}

// verificationRequest assembles the request body with the event spliced in as
// received.
//
// It cannot be built with json.Marshal and a json.RawMessage: Marshal compacts
// raw JSON and escapes <, > and & into <-style sequences, so a body with
// insignificant whitespace or those characters would reach PayPal in a
// different form than it was signed in, and verification would fail for
// deliveries that are perfectly genuine.
func verificationRequest(headers map[string]string, webhookID string, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for _, field := range [][2]string{
		{"auth_algo", headers[headerAuthAlgo]},
		{"cert_url", headers[headerCertURL]},
		{"transmission_id", headers[headerTransmissionID]},
		{"transmission_sig", headers[headerTransmissionSig]},
		{"transmission_time", headers[headerTransmissionTime]},
		{"webhook_id", webhookID},
	} {
		encoded, err := json.Marshal(field[1])
		if err != nil {
			return nil, err
		}
		buf.WriteString(`"` + field[0] + `":`)
		buf.Write(encoded)
		buf.WriteByte(',')
	}
	buf.WriteString(`"webhook_event":`)
	buf.Write(body)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ask posts one verification request, retrying once with a fresh token if the
// cached one has been rejected.
func (v *paypalVerifier) ask(ctx context.Context, payload []byte, retried bool) (string, error) {
	token, err := v.accessToken(ctx, retried)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.apiBase+"/v1/notifications/verify-webhook-signature", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling PayPal: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retried {
		v.discardToken()
		return v.ask(ctx, payload, true)
	}
	if resp.StatusCode/100 != 2 {
		// PayPal error bodies name the offending field, which is what makes a
		// misconfigured webhook id diagnosable; they carry no secrets.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("PayPal returned %s: %s", resp.Status, bytes.TrimSpace(detail))
	}

	var decoded struct {
		VerificationStatus string `json:"verification_status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decoding PayPal response: %w", err)
	}
	if decoded.VerificationStatus == "" {
		return "", errors.New("PayPal response had no verification_status")
	}
	return decoded.VerificationStatus, nil
}

// accessToken returns a cached client-credentials token, fetching one when it
// is absent, near expiry, or known to have been rejected.
func (v *paypalVerifier) accessToken(ctx context.Context, force bool) (string, error) {
	v.mu.Lock()
	if !force && v.token != "" && time.Now().Before(v.expires.Add(-tokenRefreshMargin)) {
		token := v.token
		v.mu.Unlock()
		return token, nil
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.apiBase+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(v.clientID, v.clientSecret)

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting PayPal token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Deliberately not echoing the body: a token error quotes the request.
		return "", fmt.Errorf("PayPal token request returned %s", resp.Status)
	}

	var decoded struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decoding PayPal token: %w", err)
	}
	if decoded.AccessToken == "" {
		return "", errors.New("PayPal token response had no access_token")
	}

	v.mu.Lock()
	v.token = decoded.AccessToken
	v.expires = time.Now().Add(time.Duration(decoded.ExpiresIn) * time.Second)
	v.mu.Unlock()
	return decoded.AccessToken, nil
}

func (v *paypalVerifier) discardToken() {
	v.mu.Lock()
	v.token, v.expires = "", time.Time{}
	v.mu.Unlock()
}

// checkCertURL requires an https URL on a paypal.com host.
func checkCertURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable %s", headerCertURL)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%s is not https", headerCertURL)
	}
	host := parsed.Hostname()
	if host != "paypal.com" && !strings.HasSuffix(host, ".paypal.com") {
		return fmt.Errorf("%s host %q is not PayPal", headerCertURL, host)
	}
	return nil
}
