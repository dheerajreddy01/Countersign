// Package evidence assembles the evidence bundle for a dispute from what the
// pipeline actually holds: the case row and any mandate recorded against the
// disputed order.
//
// The records it builds are demo evidence, not a live integration with a
// merchant's order, carrier and support systems — there is no such integration
// in this project, and inventing one would be the kind of overclaim the
// grounding guard exists to prevent. Every record is therefore marked
// "demo": true in its payload and sourced to a named stand-in, so the console
// and a reviewer can see what is real (the case, the mandate) and what is
// staged (the carrier scan, the support thread). The mandate is the exception:
// it is a real signed record, and its signal reflects an actual Verify/Covers
// check, not a staged value.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

// Signals are the scoring inputs the bundle implies. They are derived from the
// evidence that was actually assembled, not guessed: delivery_confirmed is true
// because a delivery record is in the bundle, mandate_matching is true because a
// stored mandate verified and covered the order.
type Signals struct {
	DeliveryConfirmed    bool
	SignatureCaptured    bool
	MandateMatching      bool
	PriorCustomerContact bool
	DaysToDeadline       float64
}

// Build returns the evidence records for a case and the scoring signals they
// imply. Records carry the citation markers (E1, E2, ...) the drafter and the
// grounding guard key on, in their payload and in the summaries returned
// separately by Summaries.
func Build(c store.Case, mandates []store.StoredMandate, now time.Time) ([]store.EvidenceRecord, Signals) {
	var records []store.EvidenceRecord
	var sig Signals
	sig.DaysToDeadline = daysUntil(c.RespondBy, now)

	add := func(typ, source string, payload map[string]any, summary string) {
		marker := fmt.Sprintf("E%d", len(records)+1)
		payload["marker"] = marker
		payload["summary"] = summary
		payload["demo"] = true
		enc, _ := json.Marshal(payload)
		sum := sha256.Sum256(enc)
		records = append(records, store.EvidenceRecord{
			CaseID:  c.ID,
			Type:    typ,
			Source:  source,
			Payload: enc,
			Hash:    hex.EncodeToString(sum[:]),
		})
	}

	// The order itself is always in evidence: it is what the dispute is about.
	add("order_record", "orders", map[string]any{
		"order_id":    c.TransactionID,
		"amount":      c.Amount,
		"currency":    c.Currency,
		"product":     c.ProductName,
		"buyer_name":  c.BuyerName,
		"buyer_email": c.BuyerEmail,
	}, orderSummary(c))

	// The mandate, when one was recorded for this order and it checks out. This
	// is the real signal: a mandate that verifies and covers the captured amount
	// is the merchant's answer to "I did not authorise this".
	if m, ok := coveringMandate(mandates, c); ok {
		p := m.Mandate.Payload
		add("mandate", "mandate-ledger", map[string]any{
			"order_id":          m.OrderID,
			"user":              p.User,
			"agent":             p.Agent,
			"instruction":       p.Instruction,
			"constraints":       p.Constraints,
			"amount_authorized": p.AmountAuthorized,
			"currency":          p.Currency,
			"signature":         m.Mandate.Signature,
			"hash":              m.Mandate.Hash,
			"verified":          true,
			"covers":            true,
			// This one is not staged: demo marks the source, not the record.
			"demo": false,
		}, mandateSummary(p))
		sig.MandateMatching = true
	}

	switch c.Reason {
	case "item_not_received":
		add("delivery_confirmation", "carrier", map[string]any{
			"carrier":            "DemoShip Express",
			"tracking_number":    trackingFor(c.TransactionID),
			"delivered_at":       deliveredAt(c, now),
			"delivery_address":   c.BuyerName + ", on file",
			"signature_captured": true,
		}, deliverySummary(c, now))
		sig.DeliveryConfirmed = true
		sig.SignatureCaptured = true

		add("support_thread", "support", map[string]any{
			"channel":   "email",
			"contacted": true,
			"excerpt":   "Buyer was sent the tracking link and delivery confirmation after first contact.",
			"responded": true,
		}, "Merchant replied to the buyer with tracking and proof of delivery before the dispute was opened.")
		sig.PriorCustomerContact = true

	case "not_as_described":
		add("item_listing", "catalog", map[string]any{
			"product":     c.ProductName,
			"description": "Listing and photos matched the item shipped; specification unchanged since purchase.",
		}, fmt.Sprintf("Product listing for %q matches the item that shipped.", displayProduct(c)))
		add("support_thread", "support", map[string]any{
			"channel":   "email",
			"contacted": true,
			"excerpt":   "Buyer was offered a return label; merchant received no response before the dispute.",
			"responded": true,
		}, "Merchant offered a return and replacement before the dispute was opened.")
		sig.PriorCustomerContact = true
	}

	return records, sig
}

// Summaries returns the drafter-facing view of a bundle: the marker, type and a
// one-line summary for each record, in order. The drafter cites by marker, so
// this is the list it must be given.
func Summaries(records []store.EvidenceRecord) []Summary {
	out := make([]Summary, 0, len(records))
	for _, r := range records {
		var p struct {
			Marker  string `json:"marker"`
			Summary string `json:"summary"`
		}
		_ = json.Unmarshal(r.Payload, &p)
		out = append(out, Summary{ID: p.Marker, Type: r.Type, Summary: p.Summary})
	}
	return out
}

// Summary is one evidence item as the drafter sees it.
type Summary struct {
	ID      string
	Type    string
	Summary string
}

func coveringMandate(mandates []store.StoredMandate, c store.Case) (store.StoredMandate, bool) {
	for _, m := range mandates {
		if mandate.Verify(m.Mandate, m.Mandate.PublicKey) != nil {
			continue
		}
		if mandate.Covers(m.Mandate, c.TransactionID, c.Amount, c.Currency) != nil {
			continue
		}
		return m, true
	}
	return store.StoredMandate{}, false
}

func orderSummary(c store.Case) string {
	return fmt.Sprintf("Order %s for %s, %s %s, placed by %s.",
		orDash(c.TransactionID), displayProduct(c), amountOrDash(c.Amount), c.Currency, orDash(c.BuyerName))
}

func mandateSummary(p mandate.Payload) string {
	s := fmt.Sprintf("Signed mandate: the user authorised %q up to %s %s; the signature verifies and the order is within the authorised amount.",
		p.Instruction, amountOrDash(p.AmountAuthorized), p.Currency)
	if len(p.Constraints) > 0 {
		s += " Constraints: " + strings.Join(p.Constraints, "; ") + "."
	}
	return s
}

func deliverySummary(c store.Case, now time.Time) string {
	return fmt.Sprintf("Carrier DemoShip Express confirms tracking %s was delivered on %s with a signature on file.",
		trackingFor(c.TransactionID), deliveredAt(c, now))
}

func displayProduct(c store.Case) string {
	if c.ProductName != "" {
		return c.ProductName
	}
	return "the ordered item"
}

func trackingFor(orderID string) string {
	if orderID == "" {
		return "DS000000000"
	}
	return "DS" + strings.ToUpper(orderID)
}

func deliveredAt(c store.Case, now time.Time) string {
	// A plausible delivery date: a week before the dispute was created, or a
	// week ago when the case has no creation time yet.
	base := c.CreatedAt
	if base.IsZero() {
		base = now
	}
	return base.AddDate(0, 0, -7).Format("2006-01-02")
}

func daysUntil(respondBy *time.Time, now time.Time) float64 {
	if respondBy == nil {
		return 0
	}
	return respondBy.Sub(now).Hours() / 24
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func amountOrDash(s string) string {
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return "—"
	}
	return s
}
