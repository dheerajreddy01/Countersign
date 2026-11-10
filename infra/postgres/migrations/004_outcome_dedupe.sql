-- Make outcome recording idempotent.
--
-- The executor publishes more than one outcome per action (an immediate
-- "submitted" and a terminal ruling), and the case service can redeliver a
-- cs.outcomes record if it restarts between handling one and committing the
-- offset. Without a key to dedupe on, each redelivery appended a second outcome
-- row and re-flipped cases.status. event_key is unique per (action, result), so
-- a repeat is a no-op.
ALTER TABLE outcomes ADD COLUMN IF NOT EXISTS event_key TEXT;

-- Unique only when set, so pre-existing rows (event_key NULL) do not collide.
CREATE UNIQUE INDEX IF NOT EXISTS outcomes_event_key
  ON outcomes (event_key) WHERE event_key IS NOT NULL;
