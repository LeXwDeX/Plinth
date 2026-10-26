CREATE TABLE execution_grants (
    request_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    offering_id TEXT NOT NULL,
    offering_revision TEXT NOT NULL,
    prepared_input_id TEXT NOT NULL,
    prepared_input_hash TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    plan_step_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('pending','approved','claimed','bound','denied')),
    task_id TEXT UNIQUE,
    source TEXT NOT NULL CHECK (source IN ('human_browser','agent_human_decision')),
    request_version INTEGER NOT NULL CHECK (request_version > 0),
    decided_by TEXT,
    decided_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((status = 'bound' AND task_id IS NOT NULL) OR (status <> 'bound' AND task_id IS NULL)),
    UNIQUE (owner_id, idempotency_key),
    UNIQUE (owner_id, session_id, operation_id, plan_step_id)
);

CREATE INDEX execution_grants_session_status ON execution_grants(owner_id, session_id, status);

CREATE TABLE execution_grant_audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    request_version INTEGER NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('superseded','approved','denied')),
    actor_id TEXT NOT NULL,
    identity_json TEXT NOT NULL CHECK (json_valid(identity_json)),
    occurred_at TEXT NOT NULL
);

CREATE INDEX execution_grant_audit_request ON execution_grant_audit(owner_id, request_id, id);

CREATE TRIGGER execution_grant_audit_immutable_update
BEFORE UPDATE ON execution_grant_audit
BEGIN SELECT RAISE(ABORT, 'execution grant audit is immutable'); END;

CREATE TRIGGER execution_grant_audit_immutable_delete
BEFORE DELETE ON execution_grant_audit
BEGIN SELECT RAISE(ABORT, 'execution grant audit is immutable'); END;
