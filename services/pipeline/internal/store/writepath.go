// This file is the write side of the store: everything the evidence builder,
// the scorer/drafter, the console action endpoint, the executor and outcome
// capture persist. store.go is the read model the console reads back; the two
// share the same pool and the same rule that any change to a case also appends
// to audit_log, so the trail cannot disagree with the row it describes.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- evidence ----------------------------------------------------------------

// EvidenceRecord is one row of evidence_records. Payload is the raw artifact
// the evidence builder gathered; Hash lets a reviewer confirm it was not edited
// after the fact.
type EvidenceRecord struct {
	ID        int64           `json:"id"`
	CaseID    int64           `json:"case_id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload"`
	Hash      string          `json:"hash"`
	CreatedAt time.Time       `json:"created_at"`
}

// ReplaceEvidence clears any evidence already recorded for a case and writes the
// given bundle, all in one transaction.
//
// Clear-and-insert rather than append because the evidence builder has no
// natural idempotency key per record, and bus redelivery can re-run it: without
// this a reprocessed cs.evidence.requested would double every row. Rebuilding
// the whole bundle is cheap and leaves exactly one copy.
func (s *Store) ReplaceEvidence(ctx context.Context, caseID int64, records []EvidenceRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM evidence_records WHERE case_id = $1`, caseID); err != nil {
		return fmt.Errorf("clear evidence for case %d: %w", caseID, err)
	}
	for _, r := range records {
		payload := r.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO evidence_records (case_id, type, source, payload, hash)
			 VALUES ($1, $2, $3, $4, $5)`,
			caseID, r.Type, r.Source, []byte(payload), r.Hash); err != nil {
			return fmt.Errorf("insert evidence %q for case %d: %w", r.Type, caseID, err)
		}
	}

	detail, _ := json.Marshal(map[string]any{"count": len(records)})
	if err := appendAudit(ctx, tx, &caseID, "evidence-builder", "Evidence bundle assembled", detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// EvidenceForCase returns the evidence bundle in id order, which is the order it
// was assembled.
func (s *Store) EvidenceForCase(ctx context.Context, caseID int64) ([]EvidenceRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, case_id, type, source, payload, hash, created_at
		 FROM evidence_records WHERE case_id = $1 ORDER BY id ASC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]EvidenceRecord, 0)
	for rows.Next() {
		var r EvidenceRecord
		var payload []byte
		if err := rows.Scan(&r.ID, &r.CaseID, &r.Type, &r.Source, &payload, &r.Hash, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Payload = json.RawMessage(payload)
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- decisions ---------------------------------------------------------------

// Decision is the scorer's verdict for a case. Factors holds the full scoring
// payload (p_win, ev_fight, refund_cost, top_factors and the inputs), so the
// console can render the reasoning without the pipeline having to anticipate
// every field in a column.
type Decision struct {
	CaseID         int64           `json:"case_id"`
	Factors        json.RawMessage `json:"factors"`
	EV             string          `json:"ev"`
	Recommendation string          `json:"recommendation"`
	CreatedAt      time.Time       `json:"created_at"`
}

// SaveDecision upserts the decision for a case; decisions.case_id is the primary
// key, so re-scoring replaces rather than duplicates.
func (s *Store) SaveDecision(ctx context.Context, caseID int64, factors json.RawMessage, ev, recommendation string) error {
	if len(factors) == 0 {
		factors = json.RawMessage("{}")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO decisions (case_id, factors, ev, recommendation)
		 VALUES ($1, $2, $3::numeric, $4)
		 ON CONFLICT (case_id) DO UPDATE SET
		   factors = EXCLUDED.factors, ev = EXCLUDED.ev, recommendation = EXCLUDED.recommendation`,
		caseID, []byte(factors), ev, recommendation)
	if err != nil {
		return fmt.Errorf("save decision for case %d: %w", caseID, err)
	}
	return nil
}

// GetDecision returns the stored decision, or ErrNotFound when the case has not
// been scored yet.
func (s *Store) GetDecision(ctx context.Context, caseID int64) (Decision, error) {
	var d Decision
	var factors []byte
	err := s.pool.QueryRow(ctx,
		`SELECT case_id, factors, ev::text, recommendation, created_at
		 FROM decisions WHERE case_id = $1`, caseID).
		Scan(&d.CaseID, &factors, &d.EV, &d.Recommendation, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, ErrNotFound
	}
	if err != nil {
		return Decision{}, err
	}
	d.Factors = json.RawMessage(factors)
	return d, nil
}

// --- drafts ------------------------------------------------------------------

// Draft is the generated rebuttal for a case, with the grounding verdict the
// console gates the action bar on.
type Draft struct {
	CaseID           int64           `json:"case_id"`
	Text             string          `json:"text"`
	Citations        json.RawMessage `json:"citations"`
	GroundingPassed  bool            `json:"grounding_passed"`
	UncitedSentences json.RawMessage `json:"uncited_sentences"`
	Source           string          `json:"source"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// SaveDraft upserts the draft for a case; drafts.case_id is the primary key.
func (s *Store) SaveDraft(ctx context.Context, d Draft) error {
	citations := d.Citations
	if len(citations) == 0 {
		citations = json.RawMessage("[]")
	}
	uncited := d.UncitedSentences
	if len(uncited) == 0 {
		uncited = json.RawMessage("[]")
	}
	source := d.Source
	if source == "" {
		source = "template"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO drafts (case_id, text, citations, grounding_passed, uncited_sentences, source, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now())
		 ON CONFLICT (case_id) DO UPDATE SET
		   text = EXCLUDED.text, citations = EXCLUDED.citations,
		   grounding_passed = EXCLUDED.grounding_passed,
		   uncited_sentences = EXCLUDED.uncited_sentences,
		   source = EXCLUDED.source, updated_at = now()`,
		d.CaseID, d.Text, []byte(citations), d.GroundingPassed, []byte(uncited), source)
	if err != nil {
		return fmt.Errorf("save draft for case %d: %w", d.CaseID, err)
	}
	return nil
}

// GetDraft returns the stored draft, or ErrNotFound when none has been written.
func (s *Store) GetDraft(ctx context.Context, caseID int64) (Draft, error) {
	var d Draft
	var citations, uncited []byte
	err := s.pool.QueryRow(ctx,
		`SELECT case_id, text, citations, grounding_passed, uncited_sentences, source, updated_at
		 FROM drafts WHERE case_id = $1`, caseID).
		Scan(&d.CaseID, &d.Text, &citations, &d.GroundingPassed, &uncited, &d.Source, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	d.Citations = json.RawMessage(citations)
	d.UncitedSentences = json.RawMessage(uncited)
	return d, nil
}

// --- actions -----------------------------------------------------------------

// Action is a merchant action on a case. IdempotencyKey is the primary key: the
// console mints it, so a double-click or a retried request records one action,
// not two, and the executor keys its PayPal call on the same value.
type Action struct {
	IdempotencyKey string          `json:"idempotency_key"`
	CaseID         int64           `json:"case_id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	Note           string          `json:"note"`
	Detail         json.RawMessage `json:"detail"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// InsertAction records a requested action, returning false when the idempotency
// key was already claimed. A repeat is not an error: it means the action is
// already in flight, and the caller returns the existing row.
func (s *Store) InsertAction(ctx context.Context, a Action) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`INSERT INTO actions (idempotency_key, case_id, kind, status, note)
		 VALUES ($1, $2, $3, 'requested', $4)
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		a.IdempotencyKey, a.CaseID, a.Kind, a.Note)
	if err != nil {
		return false, fmt.Errorf("insert action %s: %w", a.IdempotencyKey, err)
	}
	inserted := tag.RowsAffected() == 1
	if inserted {
		detail, _ := json.Marshal(map[string]any{"kind": a.Kind, "key": a.IdempotencyKey})
		if err := appendAudit(ctx, tx, &a.CaseID, "console", "Merchant requested "+a.Kind, detail); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return inserted, nil
}

// UpdateActionResult records what the executor did: a new status and the detail
// PayPal (or the simulator) returned.
func (s *Store) UpdateActionResult(ctx context.Context, key, status string, detail json.RawMessage) error {
	if len(detail) == 0 {
		detail = json.RawMessage("null")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE actions SET status = $2, detail = $3, updated_at = now() WHERE idempotency_key = $1`,
		key, status, []byte(detail))
	if err != nil {
		return fmt.Errorf("update action %s: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAction looks an action up by its idempotency key.
func (s *Store) GetAction(ctx context.Context, key string) (Action, error) {
	a, err := scanAction(s.pool.QueryRow(ctx,
		`SELECT idempotency_key, case_id, kind, status, note, detail, created_at, updated_at
		 FROM actions WHERE idempotency_key = $1`, key))
	return a, err
}

// ActionsForCase returns a case's actions, newest first.
func (s *Store) ActionsForCase(ctx context.Context, caseID int64) ([]Action, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT idempotency_key, case_id, kind, status, note, detail, created_at, updated_at
		 FROM actions WHERE case_id = $1 ORDER BY created_at DESC, idempotency_key`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Action, 0)
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAction(row pgx.Row) (Action, error) {
	var a Action
	var detail []byte
	err := row.Scan(&a.IdempotencyKey, &a.CaseID, &a.Kind, &a.Status, &a.Note,
		&detail, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, err
	}
	a.Detail = json.RawMessage(detail)
	return a, nil
}

// --- outcomes ----------------------------------------------------------------

// Outcome is a recorded ruling or settlement for a case. Payload carries the
// detail, including whether the result was a real PayPal ruling or a labeled
// simulation.
type Outcome struct {
	ID        int64           `json:"id"`
	CaseID    int64           `json:"case_id"`
	Result    string          `json:"result"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// OutcomeInput describes one outcome to record. EventKey, when set, makes the
// write idempotent: a second outcome carrying the same key is a no-op. Status,
// when set, is the case lifecycle status to advance to in the same transaction.
type OutcomeInput struct {
	CaseID     int64
	Result     string
	Status     string
	EventKey   string
	Payload    json.RawMessage
	AuditEvent string
}

// RecordOutcome inserts an outcome, advances the case status, and appends to the
// audit trail — all in one transaction, so a crash can never leave the outcome
// recorded but the status un-advanced. It returns false when EventKey was
// already recorded, in which case nothing is written: redelivery of a
// cs.outcomes record is a no-op rather than a second ruling.
func (s *Store) RecordOutcome(ctx context.Context, in OutcomeInput) (bool, error) {
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	var eventKey any
	if in.EventKey != "" {
		eventKey = in.EventKey
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`INSERT INTO outcomes (case_id, result, payload, event_key)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (event_key) DO NOTHING`,
		in.CaseID, in.Result, []byte(payload), eventKey)
	if err != nil {
		return false, fmt.Errorf("insert outcome for case %d: %w", in.CaseID, err)
	}
	if tag.RowsAffected() == 0 {
		// Already recorded under this event key. Do not re-advance the status or
		// re-audit: the first delivery already did.
		return false, nil
	}

	if in.Status != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE cases SET status = $2, updated_at = now() WHERE id = $1`,
			in.CaseID, in.Status); err != nil {
			return false, fmt.Errorf("advance case %d to %s: %w", in.CaseID, in.Status, err)
		}
	}
	event := in.AuditEvent
	if event == "" {
		event = "Outcome recorded: " + in.Result
	}
	if err := appendAudit(ctx, tx, &in.CaseID, "outcome-capture", event, []byte(payload)); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// OutcomesForCase returns a case's outcomes, newest first.
func (s *Store) OutcomesForCase(ctx context.Context, caseID int64) ([]Outcome, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, case_id, result, payload, created_at
		 FROM outcomes WHERE case_id = $1 ORDER BY created_at DESC, id DESC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Outcome, 0)
	for rows.Next() {
		var o Outcome
		var payload []byte
		if err := rows.Scan(&o.ID, &o.CaseID, &o.Result, &payload, &o.CreatedAt); err != nil {
			return nil, err
		}
		o.Payload = json.RawMessage(payload)
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- case status -------------------------------------------------------------

// SetCaseStatus advances a case's own lifecycle (open -> evidence_ready ->
// decision_pending -> action_taken -> won/lost/refunded/settled) and records the
// transition in the same transaction. It is the one place cases.status is
// written after the normaliser opens the row, so the lifecycle has a single
// owner per step.
func (s *Store) SetCaseStatus(ctx context.Context, caseID int64, status, actor, event string, detail any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE cases SET status = $2, updated_at = now() WHERE id = $1`, caseID, status)
	if err != nil {
		return fmt.Errorf("set status of case %d: %w", caseID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, tx, &caseID, actor, event, encoded); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
