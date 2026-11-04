-- Exhausted and system-expired production plans each post one chat notice
-- after their state transition commits. The date records that the notice was
-- posted, so a failed post is retried by a later sweep and a successful one
-- is not repeated. Existing plans predate this retry and are backfilled as
-- posted: upgrading must not replay old notices into their sessions.
ALTER TABLE production_budgets ADD COLUMN notice_sent_at TEXT NOT NULL DEFAULT '';

UPDATE production_budgets SET notice_sent_at = COALESCE(NULLIF(decided_at, ''), created_at);
