-- At most one action per case may be waiting on the executor.
--
-- The case service mints a fresh idempotency key for every POST, so the
-- primary key on actions cannot tell a double-clicked "approve" from two
-- deliberate actions: both requests inserted, both were published, and the
-- executor ran both against PayPal (a second accept_claim refunds the buyer a
-- second time). The executor's redelivery guard is per key and cannot help.
--
-- A check-then-insert in the handler would let two concurrent requests both
-- pass the check, so the rule lives here instead: while one action for a case
-- is 'requested' (published, not yet executed or failed), a second insert
-- fails with a unique violation, which the case service turns into a 409.
-- Once the executor marks the first action executed or failed it leaves the
-- index, and the merchant can act again.

-- A database that predates this rule may already hold more than one requested
-- action for a case (a double-click, or actions queued while the executor was
-- down), and the index cannot be built over them. Keep the newest and mark the
-- older ones failed. The executor skips an action it finds failed, so these
-- will not run late; they are the duplicates this migration exists to stop.
UPDATE actions a
SET status = 'failed',
    detail = jsonb_build_object(
      'error', 'superseded: another action on this case was already requested',
      'superseded_by_migration', '005_one_action_in_flight'),
    updated_at = now()
WHERE a.status = 'requested'
  AND EXISTS (
    SELECT 1 FROM actions newer
    WHERE newer.case_id = a.case_id
      AND newer.status = 'requested'
      AND (newer.created_at, newer.idempotency_key) > (a.created_at, a.idempotency_key)
  );

CREATE UNIQUE INDEX IF NOT EXISTS actions_one_requested_per_case
  ON actions (case_id) WHERE status = 'requested';
