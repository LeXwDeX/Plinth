-- Budget approval mode (#103): one production plan the person approves once,
-- with a total credit limit the Agent spends under.
--
-- go_chat_sessions.approval_mode's CHECK must accept 'budget'. SQLite cannot
-- alter a CHECK constraint, so the table is rebuilt. The runner disables
-- foreign_keys for this migration; otherwise DROP TABLE would cascade into
-- every child table that references a session.

CREATE TABLE go_chat_sessions_budget (
    id                       TEXT PRIMARY KEY,
    owner_id                 TEXT NOT NULL,
    owner_name               TEXT NOT NULL,
    title                    TEXT NOT NULL,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    approval_mode            TEXT NOT NULL DEFAULT 'manual'
        CHECK (approval_mode IN ('auto', 'manual', 'budget')),
    notes                    TEXT NOT NULL DEFAULT '',
    picked                   TEXT NOT NULL DEFAULT '',
    agent_scope              TEXT NOT NULL DEFAULT 'auto',
    title_source             TEXT NOT NULL DEFAULT 'user',
    reasoning_effort         TEXT NOT NULL DEFAULT '',
    context_prompt_tokens    INTEGER,
    context_completion_tokens INTEGER,
    context_model            TEXT NOT NULL DEFAULT '',
    context_updated_at       TEXT NOT NULL DEFAULT '',
    chat_model               TEXT NOT NULL DEFAULT '',
    brief                    TEXT NOT NULL DEFAULT '',
    explicit_model_3d        TEXT NOT NULL DEFAULT ''
);

INSERT INTO go_chat_sessions_budget (
    id, owner_id, owner_name, title, created_at, updated_at, approval_mode,
    notes, picked, agent_scope, title_source, reasoning_effort,
    context_prompt_tokens, context_completion_tokens, context_model,
    context_updated_at, chat_model, brief, explicit_model_3d
)
SELECT id, owner_id, owner_name, title, created_at, updated_at, approval_mode,
    notes, picked, agent_scope, title_source, reasoning_effort,
    context_prompt_tokens, context_completion_tokens, context_model,
    context_updated_at, chat_model, brief, explicit_model_3d
FROM go_chat_sessions;

DROP TABLE go_chat_sessions;

ALTER TABLE go_chat_sessions_budget RENAME TO go_chat_sessions;

CREATE INDEX IF NOT EXISTS idx_go_chat_sessions_owner_updated
    ON go_chat_sessions(owner_id, updated_at DESC);

-- One production plan per session waits for or holds the budget. A newer
-- pending plan may coexist with the still-active older one until it replaces
-- it; at most one plan of a session is active (or exhausted by spending).
CREATE TABLE production_budgets (
    id            TEXT PRIMARY KEY,
    owner_id      TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    plan_json     TEXT NOT NULL CHECK (json_valid(plan_json)),
    limit_credits INTEGER NOT NULL CHECK (limit_credits > 0),
    state         TEXT NOT NULL
        CHECK (state IN ('pending', 'active', 'exhausted', 'closed', 'denied')),
    created_at    TEXT NOT NULL,
    decided_at    TEXT,
    decided_by    TEXT NOT NULL DEFAULT '',
    -- The assistant message and tool call that proposed the plan; the pending
    -- card is anchored there and the turn's locale is frozen with it.
    message_id    TEXT NOT NULL DEFAULT '',
    call_id       TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX idx_production_budgets_owner_session
    ON production_budgets(owner_id, session_id, created_at DESC);
CREATE UNIQUE INDEX idx_production_budgets_current
    ON production_budgets(owner_id, session_id)
    WHERE state IN ('active', 'exhausted');

-- One reservation per approved Agent step. The grant request is unique, and
-- the row holds its credits until the Task ends: charged when the Task may
-- have been billed, released when it certainly was not.
CREATE TABLE budget_reservations (
    grant_request_id TEXT PRIMARY KEY,
    budget_id        TEXT NOT NULL,
    task_id          TEXT NOT NULL DEFAULT '',
    credits          INTEGER NOT NULL CHECK (credits >= 0),
    state            TEXT NOT NULL
        CHECK (state IN ('reserved', 'charged', 'released')),
    created_at       TEXT NOT NULL,
    settled_at       TEXT,
    FOREIGN KEY (budget_id) REFERENCES production_budgets(id) ON DELETE CASCADE
);

CREATE INDEX idx_budget_reservations_budget_state
    ON budget_reservations(budget_id, state);
CREATE INDEX idx_budget_reservations_task
    ON budget_reservations(task_id) WHERE task_id <> '';

-- A grant records whether an active production plan may authorize it. Steps
-- the browser itself started are never eligible.
ALTER TABLE execution_grants ADD COLUMN budget_eligible INTEGER NOT NULL DEFAULT 0
    CHECK (budget_eligible IN (0, 1));

-- Processing plans remember who started them: an Agent-created plan runs its
-- steps under the session budget, a browser-created one keeps per-step human
-- approval.
ALTER TABLE processing_plans ADD COLUMN origin TEXT NOT NULL DEFAULT 'browser'
    CHECK (origin IN ('agent', 'browser'));
