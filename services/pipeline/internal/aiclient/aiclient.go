// Package aiclient calls the Python AI service (services/ai) over HTTP for
// scoring and drafting.
//
// The AI service is the authority on both: the decision rule and the drafter
// live there, and the console already mirrors the rule in TypeScript only as an
// offline fallback. The pipeline has no such mirror, so when the scorer/drafter
// cannot reach the service it records that rather than inventing a decision.
package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to one AI service base URL.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for baseURL, or nil when baseURL is empty so a caller
// can treat "no AI service configured" as a first-class state.
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

// ScoreRequest mirrors services/ai ScoreRequest. Pointers are omitted when nil
// so the service applies its own defaults (e.g. COST_TO_FIGHT_DEFAULT).
type ScoreRequest struct {
	Amount               float64  `json:"amount"`
	Reason               string   `json:"reason,omitempty"`
	DeliveryConfirmed    bool     `json:"delivery_confirmed"`
	SignatureCaptured    bool     `json:"signature_captured"`
	MandateMatching      bool     `json:"mandate_matching"`
	PriorCustomerContact bool     `json:"prior_customer_contact"`
	BuyerDisputeHistory  int      `json:"buyer_dispute_history"`
	DaysToDeadline       float64  `json:"days_to_deadline"`
	CostToFight          *float64 `json:"cost_to_fight,omitempty"`
}

// ScoreResponse mirrors services/ai ScoreResponse. TopFactors is a list of
// [name, weight] pairs, which is how the AI service encodes them.
type ScoreResponse struct {
	PWin            float64         `json:"p_win"`
	EVFight         float64         `json:"ev_fight"`
	RefundCost      float64         `json:"refund_cost"`
	Recommendation  string          `json:"recommendation"`
	TopFactors      [][]any         `json:"top_factors"`
	FraudLikelihood *float64        `json:"fraud_likelihood,omitempty"`
	ModelVersions   json.RawMessage `json:"model_versions,omitempty"`
}

// Score calls POST /score.
func (c *Client) Score(ctx context.Context, req ScoreRequest) (ScoreResponse, error) {
	var resp ScoreResponse
	err := c.post(ctx, "/score", req, &resp)
	return resp, err
}

// Evidence is one item handed to the drafter. ID is the citation marker the
// grounding guard checks for (E1, E2, ...).
type Evidence struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

// DraftRequest mirrors services/ai DraftRequest.
type DraftRequest struct {
	Reason   string     `json:"reason"`
	Evidence []Evidence `json:"evidence"`
}

// DraftResponse mirrors services/ai DraftResponse.
type DraftResponse struct {
	Category         string   `json:"category"`
	Text             string   `json:"text"`
	Citations        []string `json:"citations"`
	GroundingPassed  bool     `json:"grounding_passed"`
	UncitedSentences []string `json:"uncited_sentences"`
	ClaimsCount      int      `json:"claims_count"`
	Source           string   `json:"source"`
}

// Draft calls POST /draft.
func (c *Client) Draft(ctx context.Context, req DraftRequest) (DraftResponse, error) {
	var resp DraftResponse
	err := c.post(ctx, "/draft", req, &resp)
	return resp, err
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ai service %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ai service %s returned %s: %s", path, resp.Status, bytes.TrimSpace(detail))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
