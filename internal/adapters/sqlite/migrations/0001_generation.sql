PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS generation_tasks (
    id                    TEXT PRIMARY KEY,
    owner_id              TEXT NOT NULL,
    capability            TEXT NOT NULL,
    offering_json         TEXT NOT NULL,
    input_json            TEXT NOT NULL,
    request_hash          TEXT NOT NULL,
    idempotency_key       TEXT NOT NULL DEFAULT '',
    state                 TEXT NOT NULL,
    progress              INTEGER NOT NULL DEFAULT 0,
    failure_json          TEXT,
    current_attempt_id    TEXT,
    materialize_failures  INTEGER NOT NULL DEFAULT 0,
    next_action_at        TEXT,
    version               INTEGER NOT NULL,
    lease_owner           TEXT,
    lease_token           TEXT,
    lease_until           TEXT,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    finished_at           TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS generation_tasks_owner_idempotency
    ON generation_tasks(owner_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS generation_tasks_ready
    ON generation_tasks(state, next_action_at, lease_until);

CREATE INDEX IF NOT EXISTS generation_tasks_owner_created
    ON generation_tasks(owner_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS generation_attempts (
    id                    TEXT PRIMARY KEY,
    task_id               TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
    ordinal               INTEGER NOT NULL,
    offering_json         TEXT NOT NULL,
    state                 TEXT NOT NULL,
    submission_key        TEXT NOT NULL,
    upstream_provider_id  TEXT,
    upstream_job_id       TEXT,
    failure_json          TEXT,
    opaque_json           TEXT,
    started_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    finished_at           TEXT,
    UNIQUE(task_id, ordinal)
);

CREATE UNIQUE INDEX IF NOT EXISTS generation_attempts_upstream_job
    ON generation_attempts(upstream_provider_id, upstream_job_id)
    WHERE upstream_provider_id IS NOT NULL AND upstream_job_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS generation_artifact_claims (
    attempt_id     TEXT NOT NULL REFERENCES generation_attempts(id) ON DELETE CASCADE,
    claim_id       TEXT NOT NULL,
    role           TEXT NOT NULL,
    locator        TEXT NOT NULL DEFAULT '',
    inline_data    BLOB,
    media_type     TEXT NOT NULL DEFAULT '',
    expires_at     TEXT,
    metadata_json  TEXT,
    PRIMARY KEY(attempt_id, claim_id)
);

CREATE TABLE IF NOT EXISTS generation_assets (
    id           TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
    attempt_id   TEXT NOT NULL REFERENCES generation_attempts(id) ON DELETE RESTRICT,
    role         TEXT NOT NULL,
    required     INTEGER NOT NULL,
    ordinal      INTEGER NOT NULL,
    storage_key  TEXT NOT NULL,
    media_type   TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL,
    sha256       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    UNIQUE(task_id, role, ordinal)
);

CREATE INDEX IF NOT EXISTS generation_assets_sha256
    ON generation_assets(sha256);

CREATE TABLE IF NOT EXISTS generation_input_assets (
    id           TEXT PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    storage_key  TEXT NOT NULL,
    media_type   TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL,
    sha256       TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS generation_input_assets_owner_created
    ON generation_input_assets(owner_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS generation_input_assets_sha256
    ON generation_input_assets(sha256);

CREATE TABLE IF NOT EXISTS generation_task_events (
    sequence    INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    data_json   TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS generation_task_events_task_sequence
    ON generation_task_events(task_id, sequence);
