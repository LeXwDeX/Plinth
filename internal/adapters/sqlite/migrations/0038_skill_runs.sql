-- SkillRun is a durable recipe/lineage ledger, not a second Generation Task.
-- Provider execution state remains exclusively in generation_tasks.
CREATE TABLE skill_runs (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE RESTRICT,
    recipe_id TEXT NOT NULL,
    recipe_revision TEXT NOT NULL,
    recipe_hash TEXT NOT NULL,
    selected_offering_id TEXT NOT NULL,
    source_asset_id TEXT NOT NULL,
    source_task_id TEXT,
    source_prepared_input_id TEXT,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_id, idempotency_key)
);
CREATE INDEX skill_runs_owner_session ON skill_runs(owner_id, session_id, created_at DESC, id DESC);

CREATE TABLE skill_run_steps (
    run_id TEXT NOT NULL REFERENCES skill_runs(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    step_key TEXT NOT NULL,
    spec_json TEXT NOT NULL CHECK (json_valid(spec_json)),
    phase TEXT NOT NULL CHECK (phase IN ('planned','prepared','grant_pending','task_bound','output_ready')),
    operation_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    plan_step_id TEXT NOT NULL,
    prepared_input_id TEXT,
    prepared_input_hash TEXT,
    grant_request_id TEXT UNIQUE,
    task_id TEXT UNIQUE REFERENCES generation_tasks(id) ON DELETE RESTRICT,
    output_asset_id TEXT UNIQUE REFERENCES generation_assets(id) ON DELETE RESTRICT,
    review TEXT NOT NULL CHECK (review IN ('unreviewed','accepted','rejected')),
    reviewed_at TEXT,
    PRIMARY KEY (run_id, ordinal),
    UNIQUE(run_id, step_key),
    UNIQUE(operation_id),
    UNIQUE(idempotency_key),
    CHECK ((prepared_input_id IS NULL AND prepared_input_hash IS NULL) OR
           (prepared_input_id IS NOT NULL AND prepared_input_hash IS NOT NULL)),
    CHECK (phase = 'planned' OR prepared_input_id IS NOT NULL),
    CHECK (phase NOT IN ('grant_pending','task_bound','output_ready') OR grant_request_id IS NOT NULL),
    CHECK (phase NOT IN ('task_bound','output_ready') OR task_id IS NOT NULL),
    CHECK (phase <> 'output_ready' OR output_asset_id IS NOT NULL),
    CHECK ((review = 'unreviewed' AND reviewed_at IS NULL) OR
           (review <> 'unreviewed' AND reviewed_at IS NOT NULL AND output_asset_id IS NOT NULL))
);
CREATE INDEX skill_run_steps_task ON skill_run_steps(task_id) WHERE task_id IS NOT NULL;
