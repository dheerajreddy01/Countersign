package evidence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

func markerAt(t *testing.T, r store.EvidenceRecord) string {
	t.Helper()
	var p struct {
		Marker string `json:"marker"`
	}
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return p.Marker
}

func types(records []store.EvidenceRecord) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Type
	}
	return out
}

func TestBuildItemNotReceivedWithoutMandate(t *testing.T) {
	c := store.Case{
		ID: 1, Reason: "item_not_received", Amount: "128.00", Currency: "USD",
		ProductName: "Trail Runner GTX", BuyerName: "A. Buyer", TransactionID: "ORD1",
	}
	records, sig := Build(c, nil, time.Now().UTC())

	if got := types(records); len(got) != 3 || got[0] != "order_record" || got[1] != "delivery_confirmation" || got[2] != "support_thread" {
		t.Fatalf("unexpected record types: %v", got)
	}
	// Markers must be E1..En in order: the drafter cites by them.
	for i, r := range records {
		want := "E" + string(rune('1'+i))
		if got := markerAt(t, r); got != want {
			t.Errorf("record %d marker = %q, want %q", i, got, want)
		}
	}
	if !sig.DeliveryConfirmed || !sig.SignatureCaptured || !sig.PriorCustomerContact {
		t.Errorf("delivery signals should be set for item_not_received: %+v", sig)
	}
	if sig.MandateMatching {
		t.Error("mandate_matching must be false when no mandate was recorded")
	}
}

func TestBuildUnauthorizedWithCoveringMandate(t *testing.T) {
	signer, _, err := mandate.GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	m, err := signer.Sign(mandate.Payload{
		User: "user_1", Agent: "agent_1", Instruction: "Buy the shoes under $200",
		AmountAuthorized: "200.00", Currency: "USD", OrderID: "ORD9", IssuedAt: time.Now().UTC(),
	}, mandate.GenesisHash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	c := store.Case{
		ID: 2, Reason: "unauthorized", Amount: "180.00", Currency: "USD",
		ProductName: "Trail Runner GTX", TransactionID: "ORD9",
	}
	records, sig := Build(c, []store.StoredMandate{{OrderID: "ORD9", Mandate: m}}, time.Now().UTC())

	if got := types(records); len(got) != 2 || got[0] != "order_record" || got[1] != "mandate" {
		t.Fatalf("unexpected record types: %v", got)
	}
	if !sig.MandateMatching {
		t.Error("a verifying, covering mandate must set mandate_matching")
	}
}

func TestBuildIgnoresMandateThatDoesNotCover(t *testing.T) {
	signer, _, _ := mandate.GenerateSigner()
	// Authorised only $50, but the captured order is $180: authentic, yet not
	// covering, so it must not be presented as authorising the charge.
	m, _ := signer.Sign(mandate.Payload{
		User: "u", Agent: "a", Instruction: "Buy something small",
		AmountAuthorized: "50.00", Currency: "USD", OrderID: "ORD9", IssuedAt: time.Now().UTC(),
	}, mandate.GenesisHash)

	c := store.Case{ID: 3, Reason: "unauthorized", Amount: "180.00", Currency: "USD", TransactionID: "ORD9"}
	records, sig := Build(c, []store.StoredMandate{{OrderID: "ORD9", Mandate: m}}, time.Now().UTC())

	if sig.MandateMatching {
		t.Error("mandate_matching must be false when the mandate does not cover the amount")
	}
	if got := types(records); len(got) != 1 || got[0] != "order_record" {
		t.Fatalf("a non-covering mandate must not add a mandate record: %v", got)
	}
}

func TestSummariesTrackMarkers(t *testing.T) {
	c := store.Case{ID: 4, Reason: "item_not_received", Amount: "10.00", Currency: "USD", TransactionID: "ORD2"}
	records, _ := Build(c, nil, time.Now().UTC())
	sums := Summaries(records)
	if len(sums) != len(records) {
		t.Fatalf("summaries = %d, records = %d", len(sums), len(records))
	}
	for i, s := range sums {
		if s.ID != markerAt(t, records[i]) {
			t.Errorf("summary %d id = %q, want %q", i, s.ID, markerAt(t, records[i]))
		}
		if s.Summary == "" {
			t.Errorf("summary %d has no text", i)
		}
	}
}
