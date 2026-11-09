-- Columns the write path needs that 001/002 did not anticipate.
--
-- Additive ALTERs with IF NOT EXISTS, for the same reason as 002: the files in
-- this directory run only when the data directory is first created, so editing
-- an earlier file would silently do nothing for a volume that already exists.
-- One file serves the fresh compose volume, the CI integration job, and
-- infra/postgres/migrate.sh against a live database.

-- The drafter returns more than text and citations: the console gates the
-- action bar on whether the grounding guard passed, and shows which drafter
-- (template or groq) produced the text. Persist those alongside the draft so a
-- reload of the case page tells the same story the drafter did.
ALTER TABLE drafts ADD COLUMN IF NOT EXISTS grounding_passed  BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE drafts ADD COLUMN IF NOT EXISTS uncited_sentences JSONB   NOT NULL DEFAULT '[]';
ALTER TABLE drafts ADD COLUMN IF NOT EXISTS source            TEXT    NOT NULL DEFAULT 'template';

-- An action carries the merchant's note and, once the executor has run, the
-- result PayPal returned. updated_at records when the executor last touched it,
-- separate from created_at when the console requested it.
ALTER TABLE actions ADD COLUMN IF NOT EXISTS note       TEXT        NOT NULL DEFAULT '';
ALTER TABLE actions ADD COLUMN IF NOT EXISTS detail     JSONB;
ALTER TABLE actions ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS actions_case_idx ON actions (case_id);
