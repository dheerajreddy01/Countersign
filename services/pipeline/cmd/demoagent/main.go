// Command demoagent stands in for the AI shopping agent in the demo.
//
// It records, through the mandate service, what the user asked it to buy
// before the order is placed. That ordering is the point: a mandate written
// after a dispute arrives proves nothing, so the agent captures the
// instruction at the moment it acts.
//
// It does not talk to PayPal. The sandbox order is created by the scripts in
// scripts/, and this passes the resulting order id through, so the mandate and
// the order agree on an identifier the dispute webhook will also carry.
//
//	go run ./cmd/demoagent -order-id 7YH29301 \
//	    -instruction "Buy the Trail Runner GTX in size 10, under $200" \
//	    -authorized 200.00 -constraint "under $200" -constraint "arrives by Friday"
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type constraints []string

func (c *constraints) String() string { return strings.Join(*c, ", ") }

func (c *constraints) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("a constraint cannot be empty")
	}
	*c = append(*c, v)
	return nil
}

func main() {
	log.SetFlags(0)

	var limits constraints
	service := flag.String("service", envOr("MANDATE_SERVICE_URL", "http://localhost:8082"),
		"base URL of the mandate service")
	orderID := flag.String("order-id", "", "the order id the mandate covers (required)")
	user := flag.String("user", "user_8812", "identifier for the user who authorised the purchase")
	agent := flag.String("agent", "countersign-demo-agent/1", "identifier for the acting agent")
	instruction := flag.String("instruction", "", "the user's request, in their own words (required)")
	authorized := flag.String("authorized", "", "the amount ceiling the user approved, e.g. 200.00 (required)")
	currency := flag.String("currency", "USD", "currency of the authorised amount")
	flag.Var(&limits, "constraint", "a limit the user set; repeat for several")
	flag.Parse()

	missing := map[string]string{
		"-order-id":    *orderID,
		"-instruction": *instruction,
		"-authorized":  *authorized,
	}
	for name, value := range missing {
		if strings.TrimSpace(value) == "" {
			log.Fatalf("%s is required\n\nUsage:\n  demoagent -order-id ORD -instruction \"...\" -authorized 200.00", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"user":              *user,
		"agent":             *agent,
		"instruction":       *instruction,
		"constraints":       []string(limits),
		"amount_authorized": *authorized,
		"currency":          *currency,
		"order_id":          *orderID,
		"issued_at":         time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("encode mandate: %v", err)
	}

	endpoint := strings.TrimRight(*service, "/") + "/mandates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		log.Fatalf("mandate service at %s is unreachable: %v", *service, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		log.Fatalf("mandate service returned %s: %s", resp.Status, raw)
	}

	var stored struct {
		ID      int64  `json:"id"`
		OrderID string `json:"order_id"`
		Mandate struct {
			Hash     string `json:"hash"`
			PrevHash string `json:"prev_hash"`
		} `json:"mandate"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		log.Fatalf("decode response: %v", err)
	}

	fmt.Printf("Mandate %d recorded for order %s\n", stored.ID, stored.OrderID)
	fmt.Printf("  instruction: %s\n", *instruction)
	if len(limits) > 0 {
		fmt.Printf("  constraints: %s\n", strings.Join(limits, "; "))
	}
	fmt.Printf("  authorised:  %s %s\n", *authorized, *currency)
	fmt.Printf("  hash:        %s\n", stored.Mandate.Hash)
	fmt.Printf("  links to:    %s\n", stored.Mandate.PrevHash)
	fmt.Printf("\nVerify the whole ledger with:\n  curl -s %s/chain | jq '{length, valid}'\n",
		strings.TrimRight(*service, "/"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
