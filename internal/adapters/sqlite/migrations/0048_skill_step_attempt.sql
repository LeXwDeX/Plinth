-- A skill step that ended without a result (its task failed, or its approval
-- was denied) can be restarted on its own. Each restart is a new attempt with
-- its own paid identity (operation, idempotency key and plan step), so the
-- earlier attempt's task and grant stay exactly as they were. Attempt 0 is the
-- original step and keeps the identity it was created with.
ALTER TABLE skill_run_steps ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0);
