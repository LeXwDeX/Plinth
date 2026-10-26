CREATE TABLE processing_plans (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    source_task_id TEXT NOT NULL,
    source_asset_id TEXT NOT NULL,
    state TEXT NOT NULL,
    approved_credits INTEGER NOT NULL,
    request_hash TEXT NOT NULL,
    steps_json TEXT NOT NULL,
    failure TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX processing_plans_owner_session ON processing_plans(owner_id, session_id, created_at DESC);
CREATE INDEX processing_plans_active ON processing_plans(state, updated_at) WHERE state IN ('PENDING', 'RUNNING');
