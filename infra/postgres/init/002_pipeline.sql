-- Columns the pipeline writes that 001_schema.sql did not anticipate.
--
-- Written as additive ALTERs rather than edits to 001 so that an existing
-- volume picks them up too: the files in this directory only run when the data
-- directory is first created, so editing 001 would silently do nothing for
-- anyone who has already started Postgres once.

-- Fields the normaliser extracts from the dispute webhook. The console already
-- renders buyer and product, and a case row without them cannot feed it.
ALTER TABLE cases ADD COLUMN IF NOT EXISTS buyer_name      TEXT NOT NULL DEFAULT '';
ALTER TABLE cases ADD COLUMN IF NOT EXISTS buyer_email     TEXT NOT NULL DEFAULT '';
ALTER TABLE cases ADD COLUMN IF NOT EXISTS product_name    TEXT NOT NULL DEFAULT '';
ALTER TABLE cases ADD COLUMN IF NOT EXISTS transaction_id  TEXT NOT NULL DEFAULT '';

-- PayPal's own dispute status, kept separate from cases.status: that column is
-- Countersign's internal lifecycle (open -> evidence_ready -> ...) and is not
-- derived from PayPal's.
ALTER TABLE cases ADD COLUMN IF NOT EXISTS paypal_status   TEXT NOT NULL DEFAULT '';

-- The raw PayPal reason enum, alongside the normalised category in `reason`.
-- Kept so a reason Countersign does not model is still recoverable, and so a
-- later mapping change can be re-applied to history.
ALTER TABLE cases ADD COLUMN IF NOT EXISTS raw_reason      TEXT NOT NULL DEFAULT '';
ALTER TABLE cases ADD COLUMN IF NOT EXISTS reason_supported BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS cases_transaction_idx ON cases (transaction_id);
CREATE INDEX IF NOT EXISTS cases_respond_by_idx  ON cases (respond_by);

-- The mandate ledger stores the hash each record is linked by, and the public
-- key that verifies it, so a stored mandate is self-describing: a reviewer can
-- check the chain without needing anything the service holds privately.
ALTER TABLE mandates ADD COLUMN IF NOT EXISTS hash       TEXT NOT NULL DEFAULT '';
ALTER TABLE mandates ADD COLUMN IF NOT EXISTS public_key TEXT NOT NULL DEFAULT '';

-- One chain per signing key. The hash must be unique within it, and uniqueness
-- on prev_hash is what stops two mandates claiming the same predecessor, which
-- is how a back-dated record would be spliced in.
CREATE UNIQUE INDEX IF NOT EXISTS mandates_hash_key      ON mandates (public_key, hash);
CREATE UNIQUE INDEX IF NOT EXISTS mandates_prev_hash_key ON mandates (public_key, prev_hash);

-- Webhook dedupe rows are only useful for as long as PayPal might retry, but
-- the gateway's in-memory deduper forgets them on restart. Recording the
-- consumer that handled an event makes the table usable for that instead.
ALTER TABLE webhook_events ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT '';
ALTER TABLE webhook_events ADD COLUMN IF NOT EXISTS handled_by TEXT NOT NULL DEFAULT '';
