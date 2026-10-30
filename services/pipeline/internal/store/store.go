// Package store is the pipeline's Postgres access layer.
//
// Tables are defined in infra/postgres/init. Every write that changes a case
// also appends to audit_log in the same transaction, so the trail cannot
// disagree with the row it describes.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/events"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/mandate"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// Store holds the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres and verifies the connection before returning, so a
// bad DATABASE_URL fails at boot rather than on the first dispute.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Case is a row of `cases` as the pipeline reads it back.
type Case struct {
	ID              int64      `json:"id"`
	PayPalDisputeID string     `json:"paypal_dispute_id"`
	Reason          string     `json:"reason"`
	RawReason       string     `json:"raw_reason"`
	ReasonSupported bool       `json:"reason_supported"`
	Stage           string     `json:"stage"`
	Status          string     `json:"status"`
	PayPalStatus    string     `json:"paypal_status"`
	Amount          string     `json:"amount"`
	Currency        string     `json:"currency"`
	RespondBy       *time.Time `json:"respond_by"`
	BuyerName       string     `json:"buyer_name"`
	BuyerEmail      string     `json:"buyer_email"`
	ProductName     string     `json:"product_name"`
	TransactionID   string     `json:"transaction_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// AuditEntry is one row of audit_log.
type AuditEntry struct {
	ID        int64           `json:"id"`
	CaseID    *int64          `json:"case_id"`
	Actor     string          `json:"actor"`
	Event     string          `json:"event"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

const caseColumns = `id, paypal_dispute_id, reason, raw_reason, reason_supported, stage, status,
	paypal_status, amount::text, currency, respond_by, buyer_name, buyer_email,
	product_name, transaction_id, created_at, updated_at`

func scanCase(row pgx.Row) (Case, error) {
	var c Case
	err := row.Scan(&c.ID, &c.PayPalDisputeID, &c.Reason, &c.RawReason, &c.ReasonSupported,
		&c.Stage, &c.Status, &c.PayPalStatus, &c.Amount, &c.Currency, &c.RespondBy,
		&c.BuyerName, &c.BuyerEmail, &c.ProductName, &c.TransactionID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	return c, err
}

// UpsertCase records a dispute, keyed on PayPal's dispute id.
//
// PayPal retries webhooks and sends an UPDATED event for the same dispute, so
// this must be idempotent: a second delivery updates the row instead of
// creating a duplicate case. cases.status is deliberately not overwritten -
// that column is Countersign's own lifecycle, and a repeat webhook must not
// reset a case the merchant has already acted on.
func (s *Store) UpsertCase(ctx context.Context, d events.Dispute) (Case, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Case{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	amount := d.Amount
	if amount == "" {
		amount = "0"
	}

	var c Case
	var inserted bool
	row := tx.QueryRow(ctx, `
		INSERT INTO cases (paypal_dispute_id, reason, raw_reason, reason_supported, stage, status,
		                   paypal_status, amount, currency, respond_by, buyer_name, buyer_email,
		                   product_name, transaction_id)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7::numeric, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (paypal_dispute_id) DO UPDATE SET
			reason           = EXCLUDED.reason,
			raw_reason       = EXCLUDED.raw_reason,
			reason_supported = EXCLUDED.reason_supported,
			stage            = EXCLUDED.stage,
			paypal_status    = EXCLUDED.paypal_status,
			amount           = EXCLUDED.amount,
			currency         = EXCLUDED.currency,
			respond_by       = EXCLUDED.respond_by,
			buyer_name       = EXCLUDED.buyer_name,
			buyer_email      = EXCLUDED.buyer_email,
			product_name     = EXCLUDED.product_name,
			transaction_id   = EXCLUDED.transaction_id,
			updated_at       = now()
		RETURNING `+caseColumns+`, (xmax = 0) AS inserted`,
		d.DisputeID, d.Reason, d.RawReason, d.Supported, d.Stage, d.PayPalStatus,
		amount, d.Currency, d.RespondBy, d.BuyerName, d.BuyerEmail, d.ProductName, d.TransactionID)

	err = row.Scan(&c.ID, &c.PayPalDisputeID, &c.Reason, &c.RawReason, &c.ReasonSupported,
		&c.Stage, &c.Status, &c.PayPalStatus, &c.Amount, &c.Currency, &c.RespondBy,
		&c.BuyerName, &c.BuyerEmail, &c.ProductName, &c.TransactionID,
		&c.CreatedAt, &c.UpdatedAt, &inserted)
	if err != nil {
		return Case{}, false, fmt.Errorf("upsert case %s: %w", d.DisputeID, err)
	}

	event := "Dispute updated from PayPal webhook"
	if inserted {
		event = "Case opened from PayPal dispute webhook"
	}
	detail, _ := json.Marshal(map[string]any{
		"event_id":         d.EventID,
		"event_type":       d.EventType,
		"raw_reason":       d.RawReason,
		"reason":           d.Reason,
		"reason_supported": d.Supported,
		"stage":            d.Stage,
		"paypal_status":    d.PayPalStatus,
	})
	if err := appendAudit(ctx, tx, &c.ID, "normalizer", event, detail); err != nil {
		return Case{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Case{}, false, err
	}
	return c, inserted, nil
}

// GetCaseByDisputeID looks a case up by PayPal's dispute id.
func (s *Store) GetCaseByDisputeID(ctx context.Context, disputeID string) (Case, error) {
	return scanCase(s.pool.QueryRow(ctx,
		`SELECT `+caseColumns+` FROM cases WHERE paypal_dispute_id = $1`, disputeID))
}

// GetCase looks a case up by its internal id.
func (s *Store) GetCase(ctx context.Context, id int64) (Case, error) {
	return scanCase(s.pool.QueryRow(ctx,
		`SELECT `+caseColumns+` FROM cases WHERE id = $1`, id))
}

// ListCases returns open cases most urgent first.
//
// Cases with no deadline sort last rather than first: a case PayPal has not
// given a due date for is not the most urgent thing on the merchant's board.
func (s *Store) ListCases(ctx context.Context, limit int) ([]Case, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+caseColumns+` FROM cases
		 ORDER BY respond_by ASC NULLS LAST, created_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cases := make([]Case, 0, limit)
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, rows.Err()
}

// RecordWebhookEvent claims a PayPal event id, returning false if it was
// already claimed.
//
// The gateway dedupes in memory, which forgets everything on restart; this is
// the durable half, so a webhook PayPal retries after a redeploy is not
// processed twice.
func (s *Store) RecordWebhookEvent(ctx context.Context, eventID, eventType, handledBy string) (bool, error) {
	if eventID == "" {
		// Nothing to dedupe on. Process it rather than dropping a real event.
		return true, nil
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO webhook_events (event_id, event_type, handled_by)
		 VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`,
		eventID, eventType, handledBy)
	if err != nil {
		return false, fmt.Errorf("record webhook event %s: %w", eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// AppendAudit writes one audit row outside a transaction.
func (s *Store) AppendAudit(ctx context.Context, caseID *int64, actor, event string, detail any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return appendAudit(ctx, s.pool, caseID, actor, event, encoded)
}

// Timeline returns the audit trail for a case, oldest first.
func (s *Store) Timeline(ctx context.Context, caseID int64) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, case_id, actor, event, detail, created_at
		 FROM audit_log WHERE case_id = $1 ORDER BY created_at ASC, id ASC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.CaseID, &e.Actor, &e.Event, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx, so an audit row can be
// written either on its own or inside the transaction that made the change it
// describes.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func appendAudit(ctx context.Context, q execer, caseID *int64, actor, event string, detail json.RawMessage) error {
	if len(detail) == 0 {
		detail = json.RawMessage("null")
	}
	_, err := q.Exec(ctx,
		`INSERT INTO audit_log (case_id, actor, event, detail) VALUES ($1, $2, $3, $4)`,
		caseID, actor, event, detail)
	if err != nil {
		return fmt.Errorf("append audit %q: %w", event, err)
	}
	return nil
}

// --- mandates ---------------------------------------------------------------

// StoredMandate is a mandate as read back from the ledger.
type StoredMandate struct {
	ID        int64           `json:"id"`
	OrderID   string          `json:"order_id"`
	Mandate   mandate.Mandate `json:"mandate"`
	CreatedAt time.Time       `json:"created_at"`
}

// LatestMandateHash returns the hash to link the next mandate to, or
// mandate.GenesisHash when the chain for this key is empty.
func (s *Store) LatestMandateHash(ctx context.Context, publicKey string) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT hash FROM mandates WHERE public_key = $1 ORDER BY id DESC LIMIT 1`,
		publicKey).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return mandate.GenesisHash, nil
	}
	if err != nil {
		return "", fmt.Errorf("latest mandate hash: %w", err)
	}
	return hash, nil
}

// InsertMandate appends a signed mandate to the ledger.
//
// The mandate is verified first: an unverifiable record is worthless as
// evidence, and storing one would let a later VerifyChain fail for the whole
// ledger because of a single bad write.
func (s *Store) InsertMandate(ctx context.Context, m mandate.Mandate) (StoredMandate, error) {
	if err := mandate.Verify(m, m.PublicKey); err != nil {
		return StoredMandate{}, fmt.Errorf("refusing to store an unverifiable mandate: %w", err)
	}
	payload, err := json.Marshal(m.Payload)
	if err != nil {
		return StoredMandate{}, err
	}

	var stored StoredMandate
	stored.Mandate = m
	err = s.pool.QueryRow(ctx,
		`INSERT INTO mandates (order_id, signed_payload, prev_hash, signature, hash, public_key)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, order_id, created_at`,
		m.Payload.OrderID, payload, m.PrevHash, m.Signature, m.Hash, m.PublicKey,
	).Scan(&stored.ID, &stored.OrderID, &stored.CreatedAt)
	if err != nil {
		return StoredMandate{}, fmt.Errorf("insert mandate for order %s: %w", m.Payload.OrderID, err)
	}

	detail, _ := json.Marshal(map[string]any{
		"order_id": m.Payload.OrderID,
		"hash":     m.Hash,
		"prev":     m.PrevHash,
	})
	if err := appendAudit(ctx, s.pool, nil, "mandate-service", "Mandate signed and stored", detail); err != nil {
		return StoredMandate{}, err
	}
	return stored, nil
}

// MandatesForOrder returns every mandate recorded against an order id.
func (s *Store) MandatesForOrder(ctx context.Context, orderID string) ([]StoredMandate, error) {
	return s.queryMandates(ctx,
		`SELECT id, order_id, signed_payload, prev_hash, signature, hash, public_key, created_at
		 FROM mandates WHERE order_id = $1 ORDER BY id ASC`, orderID)
}

// MandateChain returns the whole ledger for a signing key in insertion order,
// which is the order mandate.VerifyChain expects.
func (s *Store) MandateChain(ctx context.Context, publicKey string) ([]StoredMandate, error) {
	return s.queryMandates(ctx,
		`SELECT id, order_id, signed_payload, prev_hash, signature, hash, public_key, created_at
		 FROM mandates WHERE public_key = $1 ORDER BY id ASC`, publicKey)
}

func (s *Store) queryMandates(ctx context.Context, sql string, args ...any) ([]StoredMandate, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StoredMandate
	for rows.Next() {
		var sm StoredMandate
		var payload []byte
		if err := rows.Scan(&sm.ID, &sm.OrderID, &payload, &sm.Mandate.PrevHash,
			&sm.Mandate.Signature, &sm.Mandate.Hash, &sm.Mandate.PublicKey, &sm.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &sm.Mandate.Payload); err != nil {
			return nil, fmt.Errorf("mandate %d has an undecodable payload: %w", sm.ID, err)
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}
