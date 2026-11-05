-- #120: a production plan budgets every credit unit on its own. Provider units
-- (tripo.credit, hyper3d.credit, meshy.credit, ...) and the historical integer
-- estimates (legacy.credits) are limited, reserved and charged separately and
-- are never added together.
--
-- A plan stored before this migration has one integer limit_credits and
-- integer step quotes and reservations without a unit. Those numbers were
-- historical integer estimates, so they move unchanged into the
-- legacy.credits bucket: every limit, reservation and charged total keeps its
-- value. A plan whose steps are priced in historical credits keeps running as
-- before; a step priced in a provider's own unit finds no limit in that unit
-- and waits for the person, or for a new plan that budgets the unit. The old
-- step quotes stay in plan_json and are read as legacy.credits.
--
-- Both tables are rebuilt: SQLite cannot drop the old integer columns or
-- their CHECK constraints in place. The runner disables foreign_keys for this
-- migration, so dropping production_budgets does not cascade into the
-- reservations, and checks the result with foreign_key_check.

CREATE TABLE production_budgets_units (
    id             TEXT PRIMARY KEY,
    owner_id       TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    plan_json      TEXT NOT NULL CHECK (json_valid(plan_json)),
    -- One {"unit", "unit_label", "amount"} per unit, amount a decimal string.
    limits_json    TEXT NOT NULL CHECK (
        json_valid(limits_json) AND json_type(limits_json) = 'array' AND json_array_length(limits_json) > 0),
    state          TEXT NOT NULL
        CHECK (state IN ('pending', 'active', 'exhausted', 'closed', 'denied')),
    created_at     TEXT NOT NULL,
    decided_at     TEXT,
    decided_by     TEXT NOT NULL DEFAULT '',
    message_id     TEXT NOT NULL DEFAULT '',
    call_id        TEXT NOT NULL DEFAULT '',
    notice_sent_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

INSERT INTO production_budgets_units (
    id, owner_id, session_id, plan_json, limits_json, state, created_at,
    decided_at, decided_by, message_id, call_id, notice_sent_at
)
SELECT id, owner_id, session_id, plan_json,
    json_array(json_object('unit', 'legacy.credits', 'unit_label', 'legacy.credits',
        'amount', CAST(limit_credits AS TEXT))),
    state, created_at, decided_at, decided_by, message_id, call_id, notice_sent_at
FROM production_budgets;

DROP TABLE production_budgets;

ALTER TABLE production_budgets_units RENAME TO production_budgets;

CREATE INDEX idx_production_budgets_owner_session
    ON production_budgets(owner_id, session_id, created_at DESC);
CREATE UNIQUE INDEX idx_production_budgets_current
    ON production_budgets(owner_id, session_id)
    WHERE state IN ('active', 'exhausted');

-- A reservation holds an exact amount (millionths) of one unit.
CREATE TABLE budget_reservations_units (
    grant_request_id TEXT PRIMARY KEY,
    budget_id        TEXT NOT NULL,
    task_id          TEXT NOT NULL DEFAULT '',
    unit             TEXT NOT NULL CHECK (unit <> ''),
    amount_micros    INTEGER NOT NULL CHECK (amount_micros >= 0),
    state            TEXT NOT NULL
        CHECK (state IN ('reserved', 'charged', 'released')),
    created_at       TEXT NOT NULL,
    settled_at       TEXT,
    FOREIGN KEY (budget_id) REFERENCES production_budgets(id) ON DELETE CASCADE
);

INSERT INTO budget_reservations_units (
    grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at, settled_at
)
SELECT grant_request_id, budget_id, task_id, 'legacy.credits', credits * 1000000,
    state, created_at, settled_at
FROM budget_reservations;

DROP TABLE budget_reservations;

ALTER TABLE budget_reservations_units RENAME TO budget_reservations;

CREATE INDEX idx_budget_reservations_budget_state
    ON budget_reservations(budget_id, state);
CREATE INDEX idx_budget_reservations_task
    ON budget_reservations(task_id) WHERE task_id <> '';
