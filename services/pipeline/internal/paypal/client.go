// Package paypal is a thin client for the PayPal Disputes API, used by the
// executor to act on a dispute the merchant has approved.
//
// It reuses the same client-credentials OAuth flow the gateway uses to verify
// webhook signatures (services/gateway/verify.go): a cached bearer token,
// refreshed a little before expiry and re-fetched once on a 401. The actions it
// exposes are the ones the console offers — send a message, provide evidence,
// make an offer, accept the claim — each mapping to one Disputes endpoint.
package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

// Client calls one PayPal API base (sandbox or live).
type Client struct {
	apiBase      string
	clientID     string
	clientSecret string
	http         *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

const tokenRefreshMargin = time.Minute

// New returns a client, or nil when any credential is missing so the caller can
// treat "no PayPal configured" as a first-class state and simulate instead.
func New(apiBase, clientID, clientSecret string) *Client {
	if clientID == "" || clientSecret == "" {
		return nil
	}
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = "https://api-m.sandbox.paypal.com"
	}
	return &Client{
		apiBase:      base,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 20 * time.Second},
	}
}

// Result is what an action returned: the dispute status PayPal reports
// afterwards (when it could be read) and the raw response for the audit trail.
type Result struct {
	DisputeStatus string          `json:"dispute_status"`
	Raw           json.RawMessage `json:"raw"`
}

// SendMessage posts a message to the buyer on the dispute.
func (c *Client) SendMessage(ctx context.Context, disputeID, message string) (Result, error) {
	body := map[string]any{"message": message}
	return c.action(ctx, disputeID, "send-message", body)
}

// MakeOffer proposes a resolution. offerType defaults to REFUND when empty.
func (c *Client) MakeOffer(ctx context.Context, disputeID, amount, currency, note, offerType string) (Result, error) {
	if offerType == "" {
		offerType = "REFUND"
	}
	body := map[string]any{
		"note":       note,
		"offer_type": offerType,
		"offer_amount": map[string]string{
			"currency_code": currency,
			"value":         amount,
		},
	}
	return c.action(ctx, disputeID, "make-offer", body)
}

// AcceptClaim concedes the dispute, refunding the buyer. Terminal.
func (c *Client) AcceptClaim(ctx context.Context, disputeID, note string) (Result, error) {
	body := map[string]any{"note": note}
	return c.action(ctx, disputeID, "accept-claim", body)
}

// ProvideEvidence submits evidence on the dispute.
//
// Unlike the other actions this endpoint is multipart/form-data: an "input"
// part carrying the evidences JSON, and optional document parts. The evidence
// here is textual (notes), which the sandbox accepts without an attached file.
func (c *Client) ProvideEvidence(ctx context.Context, disputeID, evidenceType, notes string) (Result, error) {
	if evidenceType == "" {
		evidenceType = "PROOF_OF_FULFILLMENT"
	}
	input := map[string]any{
		"evidences": []map[string]any{
			{"evidence_type": evidenceType, "notes": notes},
		},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textPartHeader("input", "application/json"))
	if err != nil {
		return Result{}, err
	}
	if _, err := part.Write(inputJSON); err != nil {
		return Result{}, err
	}
	if err := mw.Close(); err != nil {
		return Result{}, err
	}

	path := fmt.Sprintf("/v1/customer/disputes/%s/provide-evidence", disputeID)
	raw, err := c.send(ctx, http.MethodPost, path, &buf, mw.FormDataContentType(), false)
	if err != nil {
		return Result{}, err
	}
	return c.withStatus(ctx, disputeID, raw), nil
}

// action posts a JSON body to one dispute sub-resource and reads the resulting
// dispute status back.
func (c *Client) action(ctx context.Context, disputeID, verb string, body any) (Result, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	path := fmt.Sprintf("/v1/customer/disputes/%s/%s", disputeID, verb)
	raw, err := c.send(ctx, http.MethodPost, path, bytes.NewReader(encoded), "application/json", false)
	if err != nil {
		return Result{}, err
	}
	return c.withStatus(ctx, disputeID, raw), nil
}

// withStatus enriches a Result with the dispute's current status, read back with
// a GET. A failure to read the status is not fatal: the action already
// succeeded, and the status is a nicety for the audit trail.
func (c *Client) withStatus(ctx context.Context, disputeID string, raw json.RawMessage) Result {
	res := Result{Raw: raw}
	status, err := c.disputeStatus(ctx, disputeID)
	if err == nil {
		res.DisputeStatus = status
	}
	return res
}

func (c *Client) disputeStatus(ctx context.Context, disputeID string) (string, error) {
	raw, err := c.send(ctx, http.MethodGet, "/v1/customer/disputes/"+disputeID, nil, "", false)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	return decoded.Status, nil
}

// send performs one authenticated request, retrying once with a fresh token on a
// 401. It returns the response body for any 2xx, and an error carrying PayPal's
// own message otherwise — those messages name the offending field, which is what
// makes a wrong dispute stage or a bad amount diagnosable.
func (c *Client) send(ctx context.Context, method, path string, body io.Reader, contentType string, retried bool) (json.RawMessage, error) {
	// Buffer the body so it can be replayed on the one retry.
	var raw []byte
	if body != nil {
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}

	token, err := c.accessToken(ctx, retried)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling PayPal %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retried {
		c.discardToken()
		return c.send(ctx, method, path, bytes.NewReader(raw), contentType, true)
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("PayPal %s returned %s: %s", path, resp.Status, bytes.TrimSpace(payload))
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(payload), nil
}

func (c *Client) accessToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	if !force && c.token != "" && time.Now().Before(c.expires.Add(-tokenRefreshMargin)) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiBase+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.clientID, c.clientSecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting PayPal token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
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
	c.mu.Lock()
	c.token = decoded.AccessToken
	c.expires = time.Now().Add(time.Duration(decoded.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return decoded.AccessToken, nil
}

func (c *Client) discardToken() {
	c.mu.Lock()
	c.token, c.expires = "", time.Time{}
	c.mu.Unlock()
}

func textPartHeader(name, contentType string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, name))
	h.Set("Content-Type", contentType)
	return h
}
