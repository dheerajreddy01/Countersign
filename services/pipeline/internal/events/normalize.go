package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Dispute is the normalised event published on cs.dispute.events and consumed
// by the case service.
type Dispute struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	DisputeID string `json:"dispute_id"`

	// Reason is the internal category; RawReason is PayPal's enum value, kept
	// for the audit log. Supported is false when PayPal's reason has no
	// Countersign category, so the pipeline never forces an ill-fitting one
	// onto a reason the scorer does not model.
	Reason    string `json:"reason"`
	RawReason string `json:"raw_reason"`
	Supported bool   `json:"reason_supported"`

	Stage string `json:"stage"`
	// PayPalStatus is PayPal's own dispute status. The internal case lifecycle
	// (cases.status) belongs to Countersign and is not derived from it.
	PayPalStatus string `json:"paypal_status"`

	Amount   string `json:"amount"`
	Currency string `json:"currency"`

	RespondBy     *time.Time `json:"respond_by,omitempty"`
	BuyerName     string     `json:"buyer_name"`
	BuyerEmail    string     `json:"buyer_email"`
	ProductName   string     `json:"product_name"`
	TransactionID string     `json:"transaction_id"`

	OccurredAt time.Time `json:"occurred_at"`
	// Raw is the untouched webhook, so the audit log can record exactly what
	// PayPal sent rather than Countersign's reading of it.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// ErrNotADispute marks an event the pipeline does not act on. Callers commit
// the offset and move on instead of dead-lettering it.
var ErrNotADispute = errors.New("not a dispute event")

// Internal reason categories. These are the same values as ReasonCategory in
// services/ai/app/classify.py; the two must agree, or the scorer and drafter
// are handed a category they do not model.
const (
	ReasonItemNotReceived    = "item_not_received"
	ReasonUnauthorized       = "unauthorized"
	ReasonNotAsDescribed     = "not_as_described"
	ReasonDuplicate          = "duplicate"
	ReasonCreditNotProcessed = "credit_not_processed"
	ReasonOther              = "other"
)

// paypalReasons mirrors _PAYPAL_REASONS in services/ai/app/classify.py. Note
// PayPal's British spelling UNAUTHORISED; the American form is accepted
// defensively, as the classifier does.
var paypalReasons = map[string]string{
	"MERCHANDISE_OR_SERVICE_NOT_RECEIVED":     ReasonItemNotReceived,
	"MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED": ReasonNotAsDescribed,
	"UNAUTHORISED":          ReasonUnauthorized,
	"UNAUTHORIZED":          ReasonUnauthorized,
	"CREDIT_NOT_PROCESSED":  ReasonCreditNotProcessed,
	"DUPLICATE_TRANSACTION": ReasonDuplicate,
}

// stages maps PayPal's dispute_life_cycle_stage to the console's
// DisputeStage values.
var stages = map[string]string{
	"INQUIRY":         "inquiry",
	"CHARGEBACK":      "chargeback",
	"PRE_ARBITRATION": "pre_arbitration",
	"ARBITRATION":     "arbitration",
}

var whitespace = regexp.MustCompile(`\s+`)

// Reason maps a PayPal reason enum value to an internal category. The second
// result is false when Countersign does not model that reason.
func Reason(raw string) (string, bool) {
	key := strings.ToUpper(whitespace.ReplaceAllString(strings.TrimSpace(raw), "_"))
	if category, ok := paypalReasons[key]; ok {
		return category, true
	}
	return ReasonOther, false
}

// Stage maps PayPal's lifecycle stage to an internal stage, defaulting to
// inquiry: a dispute PayPal has not staged yet is answerable at the cheapest
// point, and calling it a chargeback would overstate both stage and deadline.
func Stage(raw string) string {
	if stage, ok := stages[strings.ToUpper(strings.TrimSpace(raw))]; ok {
		return stage
	}
	return "inquiry"
}

// IsDisputeEvent reports whether the pipeline acts on this event type.
func IsDisputeEvent(eventType string) bool {
	switch eventType {
	case TypeDisputeCreated, TypeDisputeUpdated, TypeDisputeResolved:
		return true
	}
	return false
}

// Normalize converts a raw PayPal webhook into a Dispute. It returns an error
// wrapping ErrNotADispute for event types the pipeline ignores.
func Normalize(raw []byte) (Dispute, error) {
	var wh Webhook
	if err := json.Unmarshal(raw, &wh); err != nil {
		return Dispute{}, fmt.Errorf("decode webhook: %w", err)
	}
	if !IsDisputeEvent(wh.EventType) {
		return Dispute{}, fmt.Errorf("%q: %w", wh.EventType, ErrNotADispute)
	}
	if wh.Resource.DisputeID == "" {
		return Dispute{}, errors.New("webhook has no resource.dispute_id")
	}

	reason, supported := Reason(wh.Resource.Reason)
	d := Dispute{
		EventID:      wh.ID,
		EventType:    wh.EventType,
		DisputeID:    wh.Resource.DisputeID,
		Reason:       reason,
		RawReason:    strings.ToUpper(strings.TrimSpace(wh.Resource.Reason)),
		Supported:    supported,
		Stage:        Stage(wh.Resource.DisputeLifeCycleStage),
		PayPalStatus: strings.ToUpper(strings.TrimSpace(wh.Resource.Status)),
		Amount:       strings.TrimSpace(wh.Resource.DisputeAmount.Value),
		Currency:     strings.ToUpper(strings.TrimSpace(wh.Resource.DisputeAmount.CurrencyCode)),
		OccurredAt:   firstTime(wh.CreateTime, wh.Resource.UpdatedTime, wh.Resource.CreateTime),
		Raw:          json.RawMessage(raw),
	}
	if d.Currency == "" {
		d.Currency = "USD"
	}
	if due, ok := parseTime(wh.Resource.SellerResponseDueDate); ok {
		d.RespondBy = &due
	}
	if len(wh.Resource.DisputedTransactions) > 0 {
		t := wh.Resource.DisputedTransactions[0]
		d.BuyerName = strings.TrimSpace(t.Buyer.Name)
		d.BuyerEmail = strings.TrimSpace(t.Buyer.Email)
		d.ProductName = strings.TrimSpace(t.ItemName)
		d.TransactionID = strings.TrimSpace(t.SellerTransactionID)
	}
	return d, nil
}

// firstTime returns the first parseable timestamp, falling back to now so an
// event is never recorded with a zero time.
func firstTime(candidates ...string) time.Time {
	for _, c := range candidates {
		if t, ok := parseTime(c); ok {
			return t
		}
	}
	return time.Now().UTC()
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	// PayPal sends RFC 3339; some fields arrive without the colon in the zone.
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
