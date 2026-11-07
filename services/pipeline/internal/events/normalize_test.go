package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const disputeCreated = `{
  "id": "WH-8KL2",
  "event_type": "CUSTOMER.DISPUTE.CREATED",
  "create_time": "2026-10-20T11:00:00Z",
  "resource": {
    "dispute_id": "PP-D-4471",
    "reason": "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
    "status": "WAITING_FOR_SELLER_RESPONSE",
    "dispute_amount": { "currency_code": "usd", "value": "149.00" },
    "dispute_life_cycle_stage": "INQUIRY",
    "seller_response_due_date": "2026-11-03T11:00:00Z",
    "create_time": "2026-10-20T10:59:00Z",
    "updated_time": "2026-10-20T11:00:00Z",
    "disputed_transactions": [
      {
        "seller_transaction_id": "7YH29301",
        "invoice_id": "INV-4471",
        "item_name": "Trail Runner GTX",
        "buyer": { "name": "A. Buyer", "email": "buyer@example.com" }
      }
    ]
  }
}`

func TestNormalizeDisputeCreated(t *testing.T) {
	d, err := Normalize([]byte(disputeCreated))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	if d.DisputeID != "PP-D-4471" {
		t.Errorf("DisputeID = %q, want PP-D-4471", d.DisputeID)
	}
	if d.Reason != ReasonItemNotReceived || !d.Supported {
		t.Errorf("Reason = %q supported=%v, want item_not_received supported", d.Reason, d.Supported)
	}
	if d.RawReason != "MERCHANDISE_OR_SERVICE_NOT_RECEIVED" {
		t.Errorf("RawReason = %q", d.RawReason)
	}
	if d.Stage != "inquiry" {
		t.Errorf("Stage = %q, want inquiry", d.Stage)
	}
	// Amount stays a decimal string: it must reach the NUMERIC column unrounded.
	if d.Amount != "149.00" {
		t.Errorf("Amount = %q, want 149.00", d.Amount)
	}
	if d.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (upper-cased)", d.Currency)
	}
	if d.RespondBy == nil || !d.RespondBy.Equal(time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("RespondBy = %v, want 2026-11-03T11:00:00Z", d.RespondBy)
	}
	if d.BuyerName != "A. Buyer" || d.BuyerEmail != "buyer@example.com" {
		t.Errorf("buyer = %q / %q", d.BuyerName, d.BuyerEmail)
	}
	if d.ProductName != "Trail Runner GTX" || d.TransactionID != "7YH29301" {
		t.Errorf("product = %q, txn = %q", d.ProductName, d.TransactionID)
	}
	if d.PayPalStatus != "WAITING_FOR_SELLER_RESPONSE" {
		t.Errorf("PayPalStatus = %q", d.PayPalStatus)
	}
	// The raw event must survive verbatim for the audit log.
	var round map[string]any
	if err := json.Unmarshal(d.Raw, &round); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}
	if round["id"] != "WH-8KL2" {
		t.Errorf("Raw lost the event id")
	}
}

func TestNormalizeIgnoresUnrelatedEventTypes(t *testing.T) {
	raw := `{"id":"WH-1","event_type":"PAYMENT.CAPTURE.COMPLETED","resource":{"dispute_id":"x"}}`
	if _, err := Normalize([]byte(raw)); !errors.Is(err, ErrNotADispute) {
		t.Fatalf("err = %v, want ErrNotADispute", err)
	}
}

func TestNormalizeRejectsMissingDisputeID(t *testing.T) {
	raw := `{"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED","resource":{}}`
	if _, err := Normalize([]byte(raw)); err == nil {
		t.Fatal("want an error when resource.dispute_id is absent")
	}
}

func TestNormalizeRejectsMalformedJSON(t *testing.T) {
	if _, err := Normalize([]byte("{not json")); err == nil {
		t.Fatal("want a decode error")
	}
}

func TestReasonMapping(t *testing.T) {
	// The supported set must stay in step with _PAYPAL_REASONS in
	// services/ai/app/classify.py.
	cases := []struct {
		raw       string
		want      string
		supported bool
	}{
		{"MERCHANDISE_OR_SERVICE_NOT_RECEIVED", ReasonItemNotReceived, true},
		{"MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED", ReasonNotAsDescribed, true},
		{"UNAUTHORISED", ReasonUnauthorized, true},
		{"UNAUTHORIZED", ReasonUnauthorized, true},
		{"CREDIT_NOT_PROCESSED", ReasonCreditNotProcessed, true},
		{"DUPLICATE_TRANSACTION", ReasonDuplicate, true},
		{"  unauthorised  ", ReasonUnauthorized, true},
		// Reasons PayPal defines but Countersign does not model: these must
		// come back unsupported rather than be forced into a category.
		{"INCORRECT_AMOUNT", ReasonOther, false},
		{"PAYMENT_BY_OTHER_MEANS", ReasonOther, false},
		{"CANCELED_RECURRING_BILLING", ReasonOther, false},
		{"PROBLEM_WITH_REMITTANCE", ReasonOther, false},
		{"OTHER", ReasonOther, false},
		{"", ReasonOther, false},
	}
	for _, c := range cases {
		got, supported := Reason(c.raw)
		if got != c.want || supported != c.supported {
			t.Errorf("Reason(%q) = %q/%v, want %q/%v", c.raw, got, supported, c.want, c.supported)
		}
	}
}

func TestStageMapping(t *testing.T) {
	cases := map[string]string{
		"INQUIRY":         "inquiry",
		"CHARGEBACK":      "chargeback",
		"PRE_ARBITRATION": "pre_arbitration",
		"ARBITRATION":     "arbitration",
		// Unknown or absent stages default to the cheapest answerable point.
		"":        "inquiry",
		"SOMEHOW": "inquiry",
	}
	for raw, want := range cases {
		if got := Stage(raw); got != want {
			t.Errorf("Stage(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeFallsBackToNowWhenNoTimestampParses(t *testing.T) {
	raw := `{"id":"WH-2","event_type":"CUSTOMER.DISPUTE.CREATED",
	         "resource":{"dispute_id":"PP-D-1","create_time":"not-a-time"}}`
	d, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if d.OccurredAt.IsZero() {
		t.Error("OccurredAt must never be the zero time")
	}
	if d.RespondBy != nil {
		t.Error("RespondBy must stay nil when PayPal sends no due date")
	}
	// No disputed_transactions: buyer fields stay empty rather than panicking.
	if d.BuyerName != "" || d.TransactionID != "" {
		t.Error("buyer fields should be empty")
	}
}
