// Package mandate records what a user actually asked an AI agent to buy, and
// signs it.
//
// This is the evidence a merchant cannot otherwise produce. When an agent
// places an order on someone's behalf and the buyer later claims the charge was
// unauthorised, the merchant's defence is the user's own instruction: what they
// asked for, what limits they set, and when. A mandate is that instruction,
// captured at order time rather than reconstructed afterwards.
//
// Two properties make it worth anything as evidence:
//
//   - Each mandate is signed with Ed25519, so it cannot be edited after the
//     fact without invalidating the signature.
//   - Each mandate carries the hash of the previous one, so mandates form a
//     chain per merchant. Removing or reordering a record breaks the chain, so
//     a mandate cannot be quietly back-dated into the ledger once a dispute is
//     already open, which is exactly when the temptation would arise.
//
// The signing key is the merchant's. Verification needs only the public key, so
// a reviewer can check a mandate without being able to mint one.
package mandate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GenesisHash is the PrevHash of the first mandate in a chain. A fixed,
// non-empty value means "deliberately first" rather than "field was missing".
const GenesisHash = "genesis"

// Payload is the user's instruction, as captured when the agent ordered. It is
// the signed part of a mandate: every field here is covered by the signature.
type Payload struct {
	// User and Agent identify who authorised the purchase and which agent
	// acted. Opaque identifiers, not credentials.
	User  string `json:"user"`
	Agent string `json:"agent"`

	// Instruction is the user's request in their own words. This is what a
	// reviewer reads when deciding whether the charge was authorised.
	Instruction string `json:"instruction"`

	// Constraints are the limits the user set ("under $200", "ships by
	// Friday"). They matter because an order outside them is not covered by
	// this mandate, and the merchant should not claim that it is.
	Constraints []string `json:"constraints"`

	// AmountAuthorized is the ceiling the user approved, in Currency. The
	// captured order amount is checked against it, not assumed equal to it.
	AmountAuthorized string `json:"amount_authorized"`
	Currency         string `json:"currency"`

	OrderID  string    `json:"order_id"`
	IssuedAt time.Time `json:"issued_at"`
}

// Mandate is a signed Payload, linked to the one before it.
type Mandate struct {
	Payload Payload `json:"payload"`
	// PrevHash is the Hash of the preceding mandate, or GenesisHash.
	PrevHash string `json:"prev_hash"`
	// Hash covers the canonical payload and PrevHash; it is what the next
	// mandate links to.
	Hash string `json:"hash"`
	// Signature is Ed25519 over the same bytes Hash is taken of, base64.
	Signature string `json:"signature"`
	// PublicKey is the verifying key, base64, so a stored mandate is
	// self-describing to a reviewer.
	PublicKey string `json:"public_key"`
}

var (
	// ErrBadSignature means the payload does not match the signature: the
	// record was altered after signing, or signed by a different key.
	ErrBadSignature = errors.New("mandate signature does not verify")
	// ErrBrokenChain means a mandate does not link to its predecessor.
	ErrBrokenChain = errors.New("mandate chain is broken")
	// ErrNotCovered means the order is outside what the user authorised, so
	// this mandate is not evidence that the charge was approved.
	ErrNotCovered = errors.New("order is not covered by the mandate")
)

// Signer mints mandates with a merchant's Ed25519 private key.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner returns a Signer for a 64-byte Ed25519 private key.
func NewSigner(priv ed25519.PrivateKey) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key is %d bytes, want %d", len(priv), ed25519.PrivateKeySize)
	}
	return &Signer{priv: priv}, nil
}

// NewSignerFromBase64 parses a base64 Ed25519 private key, as held in
// MANDATE_SIGNING_KEY.
func NewSignerFromBase64(encoded string) (*Signer, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode signing key: %w", err)
	}
	return NewSigner(ed25519.PrivateKey(raw))
}

// GenerateSigner creates a fresh keypair and returns the Signer with the
// private key base64-encoded, for development and the demo agent. A real
// deployment supplies a key it already holds rather than minting one per boot,
// or every restart starts a new chain nobody can verify against the old one.
func GenerateSigner() (*Signer, string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate key: %w", err)
	}
	s, err := NewSigner(priv)
	if err != nil {
		return nil, "", err
	}
	return s, base64.StdEncoding.EncodeToString(priv), nil
}

// PublicKey returns the verifying key, base64-encoded.
func (s *Signer) PublicKey() string {
	pub := s.priv.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(pub)
}

// signingBytes is the exact byte sequence that is hashed and signed.
//
// It is built from a canonical JSON encoding of the payload with PrevHash
// appended, so the same mandate always produces the same bytes. Go's
// encoding/json sorts struct fields by declaration order and map keys
// lexically, which makes this deterministic for a fixed struct; Payload is
// therefore a struct and never a map.
func signingBytes(p Payload, prevHash string) ([]byte, error) {
	canonical, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	buf := make([]byte, 0, len(canonical)+len(prevHash)+1)
	buf = append(buf, canonical...)
	buf = append(buf, '|')
	buf = append(buf, prevHash...)
	return buf, nil
}

// Sign produces a mandate linking to prevHash. Pass GenesisHash for the first
// mandate in a chain.
func (s *Signer) Sign(p Payload, prevHash string) (Mandate, error) {
	if strings.TrimSpace(p.OrderID) == "" {
		return Mandate{}, errors.New("mandate payload needs an order_id")
	}
	if strings.TrimSpace(p.Instruction) == "" {
		return Mandate{}, errors.New("mandate payload needs the user's instruction")
	}
	if prevHash == "" {
		return Mandate{}, errors.New("prev_hash must be set (use GenesisHash for the first)")
	}
	if p.IssuedAt.IsZero() {
		p.IssuedAt = time.Now().UTC()
	} else {
		p.IssuedAt = p.IssuedAt.UTC()
	}
	if p.Constraints == nil {
		p.Constraints = []string{}
	}

	body, err := signingBytes(p, prevHash)
	if err != nil {
		return Mandate{}, err
	}
	sum := sha256.Sum256(body)
	return Mandate{
		Payload:   p,
		PrevHash:  prevHash,
		Hash:      hex.EncodeToString(sum[:]),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, body)),
		PublicKey: s.PublicKey(),
	}, nil
}

// Verify checks a mandate's hash and signature against a public key. It does
// not check the chain; use VerifyChain for that.
func Verify(m Mandate, publicKey string) error {
	body, err := signingBytes(m.Payload, m.PrevHash)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != m.Hash {
		// The hash is derived, so a mismatch means the stored payload is not
		// the one that was hashed.
		return fmt.Errorf("%w: hash does not match payload", ErrBadSignature)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
		return ErrBadSignature
	}
	return nil
}

// VerifyChain checks a ledger in order: every mandate must verify, and each
// must link to the hash of the one before it. An empty chain is vacuously
// valid. This is what detects a record being removed, reordered, or inserted
// after the fact.
func VerifyChain(chain []Mandate, publicKey string) error {
	expected := GenesisHash
	for i, m := range chain {
		if err := Verify(m, publicKey); err != nil {
			return fmt.Errorf("mandate %d (order %s): %w", i, m.Payload.OrderID, err)
		}
		if m.PrevHash != expected {
			return fmt.Errorf("%w at mandate %d (order %s): prev_hash is %s, want %s",
				ErrBrokenChain, i, m.Payload.OrderID, m.PrevHash, expected)
		}
		expected = m.Hash
	}
	return nil
}

// Covers reports whether an order is within what the user authorised: the same
// order id, and a captured amount at or below the authorised ceiling in the
// same currency.
//
// This is deliberately separate from Verify. A mandate can be perfectly
// authentic and still not authorise the charge in question, and presenting it
// as though it did would be the same overclaiming the grounding guard exists
// to prevent.
func Covers(m Mandate, orderID string, capturedAmount string, currency string) error {
	if m.Payload.OrderID != orderID {
		return fmt.Errorf("%w: mandate is for order %s, not %s", ErrNotCovered, m.Payload.OrderID, orderID)
	}
	if !strings.EqualFold(m.Payload.Currency, currency) {
		return fmt.Errorf("%w: mandate authorised %s, order is %s", ErrNotCovered, m.Payload.Currency, currency)
	}
	authorized, err := parseAmount(m.Payload.AmountAuthorized)
	if err != nil {
		return fmt.Errorf("%w: mandate amount %q: %v", ErrNotCovered, m.Payload.AmountAuthorized, err)
	}
	captured, err := parseAmount(capturedAmount)
	if err != nil {
		return fmt.Errorf("%w: captured amount %q: %v", ErrNotCovered, capturedAmount, err)
	}
	if captured > authorized {
		return fmt.Errorf("%w: captured %s exceeds the authorised %s",
			ErrNotCovered, capturedAmount, m.Payload.AmountAuthorized)
	}
	return nil
}

// parseAmount reads a decimal money string as integer minor units, so amounts
// compare exactly instead of through binary floating point.
func parseAmount(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty amount")
	}
	neg := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(strings.TrimPrefix(value, "-"), "+")

	whole, frac, hasFrac := strings.Cut(value, ".")
	if whole == "" {
		whole = "0"
	}
	if !hasFrac {
		frac = "00"
	}
	switch len(frac) {
	case 0:
		frac = "00"
	case 1:
		frac += "0"
	case 2:
	default:
		return 0, fmt.Errorf("more than two decimal places: %q", value)
	}

	var units int64
	for _, part := range []string{whole, frac} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("not a decimal amount: %q", value)
			}
			units = units*10 + int64(r-'0')
		}
	}
	if neg {
		units = -units
	}
	return units, nil
}
