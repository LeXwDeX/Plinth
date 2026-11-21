-- 0106: a production plan whose required steps all succeeded is `completed`
-- (#410). Until now every plan stayed `active` after its last step, so a
-- finished plan kept reading "in progress" and kept its remaining credits
-- reserved; only a newer plan or an exhausted budget ended it.
--
-- SQLite cannot alter a CHECK constraint, so production_budgets is rebuilt
-- with the new state. Every row and every column is copied unchanged: the plan
-- bytes, the per-unit limits, the decision, the notice date. The runner
-- disables foreign_keys for this migration, so the rebuild does not cascade
-- into budget_reservations, and refuses to commit when foreign_key_check finds
-- a dangling reference.
--
-- The current-plan index keeps its meaning ("at most one plan of a session may
-- spend, be shown as current and be replaced") and gains `completed`: a
-- finished plan stays the session's current plan, so the card still shows it
-- as done, until a newer plan is approved and closes it the way it closes an
-- exhausted one. A new plan is unaffected either way: proposing one only
-- closes the session's pending plan, and approval closes the current one.

CREATE TABLE production_budgets_completed (
    id             TEXT PRIMARY KEY,
    owner_id       TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    plan_json      TEXT NOT NULL CHECK (json_valid(plan_json)),
    -- One {"unit", "unit_label", "amount"} per unit, amount a decimal string.
    limits_json    TEXT NOT NULL CHECK (
        json_valid(limits_json) AND json_type(limits_json) = 'array' AND json_array_length(limits_json) > 0),
    state          TEXT NOT NULL
        CHECK (state IN ('pending', 'active', 'exhausted', 'closed', 'denied', 'completed')),
    created_at     TEXT NOT NULL,
    decided_at     TEXT,
    decided_by     TEXT NOT NULL DEFAULT '',
    message_id     TEXT NOT NULL DEFAULT '',
    call_id        TEXT NOT NULL DEFAULT '',
    notice_sent_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

INSERT INTO production_budgets_completed (
    id, owner_id, session_id, plan_json, limits_json, state, created_at,
    decided_at, decided_by, message_id, call_id, notice_sent_at
)
SELECT id, owner_id, session_id, plan_json, limits_json, state, created_at,
    decided_at, decided_by, message_id, call_id, notice_sent_at
FROM production_budgets;

DROP TABLE production_budgets;

ALTER TABLE production_budgets_completed RENAME TO production_budgets;

CREATE INDEX idx_production_budgets_owner_session
    ON production_budgets(owner_id, session_id, created_at DESC);
CREATE UNIQUE INDEX idx_production_budgets_current
    ON production_budgets(owner_id, session_id)
    WHERE state IN ('active', 'exhausted', 'completed');