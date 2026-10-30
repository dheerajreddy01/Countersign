package mandate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testPayload(orderID string) Payload {
	return Payload{
		User:             "user_8812",
		Agent:            "countersign-demo-agent/1",
		Instruction:      "Buy the Trail Runner GTX in size 10, under $200, delivered this week.",
		Constraints:      []string{"under $200", "arrives by Friday"},
		AmountAuthorized: "200.00",
		Currency:         "USD",
		OrderID:          orderID,
		IssuedAt:         time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC),
	}
}

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	s, _, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	return s
}

func TestSignedMandateVerifies(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(m, s.PublicKey()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m.Hash == "" || m.Signature == "" {
		t.Fatal("hash and signature must be set")
	}
	if m.PrevHash != GenesisHash {
		t.Errorf("PrevHash = %q, want %q", m.PrevHash, GenesisHash)
	}
}

func TestEditingTheInstructionBreaksTheSignature(t *testing.T) {
	// The point of signing: the instruction cannot be rewritten after the
	// dispute is opened to make the order look better authorised.
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m.Payload.Instruction = "Buy anything you like at any price."
	if err := Verify(m, s.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestRaisingTheAuthorisedAmountBreaksTheSignature(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m.Payload.AmountAuthorized = "5000.00"
	if err := Verify(m, s.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestAnotherKeyCannotVerify(t *testing.T) {
	s := newTestSigner(t)
	other := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(m, other.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// signChain mints n linked mandates.
func signChain(t *testing.T, s *Signer, n int) []Mandate {
	t.Helper()
	chain := make([]Mandate, 0, n)
	prev := GenesisHash
	for i := 0; i < n; i++ {
		m, err := s.Sign(testPayload("ORD-"+string(rune('1'+i))), prev)
		if err != nil {
			t.Fatalf("Sign %d: %v", i, err)
		}
		chain = append(chain, m)
		prev = m.Hash
	}
	return chain
}

func TestVerifyChainAcceptsALinkedLedger(t *testing.T) {
	s := newTestSigner(t)
	if err := VerifyChain(signChain(t, s, 4), s.PublicKey()); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestEmptyChainIsVacuouslyValid(t *testing.T) {
	s := newTestSigner(t)
	if err := VerifyChain(nil, s.PublicKey()); err != nil {
		t.Fatalf("VerifyChain(nil): %v", err)
	}
}

func TestRemovingAMandateBreaksTheChain(t *testing.T) {
	// A merchant must not be able to drop an inconvenient record and present
	// the remainder as a complete ledger.
	s := newTestSigner(t)
	chain := signChain(t, s, 4)
	tampered := append(chain[:1:1], chain[2:]...)
	err := VerifyChain(tampered, s.PublicKey())
	if !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("err = %v, want ErrBrokenChain", err)
	}
}

func TestReorderingMandatesBreaksTheChain(t *testing.T) {
	s := newTestSigner(t)
	chain := signChain(t, s, 3)
	chain[1], chain[2] = chain[2], chain[1]
	if err := VerifyChain(chain, s.PublicKey()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("err = %v, want ErrBrokenChain", err)
	}
}

func TestInsertingABackdatedMandateBreaksTheChain(t *testing.T) {
	// The case that matters: a mandate minted after a dispute is opened and
	// spliced into the middle of the ledger.
	s := newTestSigner(t)
	chain := signChain(t, s, 3)
	late, err := s.Sign(testPayload("ORD-LATE"), chain[0].Hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	spliced := []Mandate{chain[0], late, chain[1], chain[2]}
	if err := VerifyChain(spliced, s.PublicKey()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("err = %v, want ErrBrokenChain", err)
	}
}

func TestSignRejectsIncompletePayloads(t *testing.T) {
	s := newTestSigner(t)
	p := testPayload("")
	if _, err := s.Sign(p, GenesisHash); err == nil {
		t.Error("want an error with no order_id")
	}
	p = testPayload("ORD-1")
	p.Instruction = "   "
	if _, err := s.Sign(p, GenesisHash); err == nil {
		t.Error("want an error with no instruction")
	}
	if _, err := s.Sign(testPayload("ORD-1"), ""); err == nil {
		t.Error("want an error with an empty prev_hash")
	}
}

func TestSignDefaultsIssuedAtAndConstraints(t *testing.T) {
	s := newTestSigner(t)
	p := testPayload("ORD-1")
	p.IssuedAt = time.Time{}
	p.Constraints = nil
	m, err := s.Sign(p, GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if m.Payload.IssuedAt.IsZero() {
		t.Error("IssuedAt should default to now")
	}
	if m.Payload.Constraints == nil {
		t.Error("Constraints should default to an empty slice, not null")
	}
	// Still verifies with the defaults filled in.
	if err := Verify(m, s.PublicKey()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSignIsDeterministicForTheSamePayload(t *testing.T) {
	s := newTestSigner(t)
	a, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	b, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if a.Hash != b.Hash {
		t.Errorf("hash is not deterministic: %s vs %s", a.Hash, b.Hash)
	}
}

func TestCoversAcceptsAnOrderWithinTheAuthorisedAmount(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	for _, amount := range []string{"149.00", "200.00", "199.99", "7"} {
		if err := Covers(m, "ORD-1", amount, "USD"); err != nil {
			t.Errorf("Covers(%s): %v", amount, err)
		}
	}
}

func TestCoversRejectsOrdersTheUserDidNotAuthorise(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cases := []struct {
		name     string
		orderID  string
		amount   string
		currency string
	}{
		{"different order", "ORD-2", "149.00", "USD"},
		{"over the ceiling", "ORD-1", "200.01", "USD"},
		{"different currency", "ORD-1", "149.00", "EUR"},
		{"unparseable amount", "ORD-1", "one hundred", "USD"},
	}
	for _, c := range cases {
		err := Covers(m, c.orderID, c.amount, c.currency)
		if !errors.Is(err, ErrNotCovered) {
			t.Errorf("%s: err = %v, want ErrNotCovered", c.name, err)
		}
	}
}

func TestCoversIsCaseInsensitiveAboutCurrency(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Covers(m, "ORD-1", "149.00", "usd"); err != nil {
		t.Fatalf("Covers: %v", err)
	}
}

func TestParseAmountExactness(t *testing.T) {
	// Money is compared in minor units; 0.1+0.2 style drift must not decide
	// whether an order was authorised.
	cases := map[string]int64{
		"0":       0,
		"7":       700,
		"7.5":     750,
		"149.00":  14900,
		"199.99":  19999,
		"0.01":    1,
		"-5.00":   -500,
		"1000000": 100000000,
	}
	for in, want := range cases {
		got, err := parseAmount(in)
		if err != nil {
			t.Errorf("parseAmount(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseAmount(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "   ", "abc", "1.234", "1.2.3", "12a.00"} {
		if _, err := parseAmount(bad); err == nil {
			t.Errorf("parseAmount(%q) should fail", bad)
		}
	}
}

func TestNewSignerRejectsWrongKeySizes(t *testing.T) {
	if _, err := NewSigner(nil); err == nil {
		t.Error("want an error for a nil key")
	}
	if _, err := NewSignerFromBase64("not base64!!"); err == nil {
		t.Error("want an error for undecodable base64")
	}
	if _, err := NewSignerFromBase64("c2hvcnQ="); err == nil {
		t.Error("want an error for a short key")
	}
}

func TestSignerRoundTripsThroughBase64(t *testing.T) {
	s, encoded, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	// A restart reloads the same key from the environment and continues the
	// same chain; a fresh key per boot would orphan the existing ledger.
	reloaded, err := NewSignerFromBase64("  " + encoded + "\n")
	if err != nil {
		t.Fatalf("NewSignerFromBase64: %v", err)
	}
	if reloaded.PublicKey() != s.PublicKey() {
		t.Fatal("reloaded signer has a different public key")
	}
	m, err := reloaded.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(m, s.PublicKey()); err != nil {
		t.Fatalf("Verify with the original public key: %v", err)
	}
}

func TestVerifyRejectsMalformedKeysAndSignatures(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(m, "not base64!!"); err == nil {
		t.Error("want an error for an undecodable public key")
	}
	if err := Verify(m, "c2hvcnQ="); err == nil {
		t.Error("want an error for a short public key")
	}
	bad := m
	bad.Signature = "not base64!!"
	if err := Verify(bad, s.PublicKey()); err == nil {
		t.Error("want an error for an undecodable signature")
	}
}

func TestTamperedHashIsDetectedBeforeTheSignature(t *testing.T) {
	s := newTestSigner(t)
	m, err := s.Sign(testPayload("ORD-1"), GenesisHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m.Hash = strings.Repeat("0", 64)
	if err := Verify(m, s.PublicKey()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}
