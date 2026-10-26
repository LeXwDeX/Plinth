CREATE TABLE IF NOT EXISTS generation_usage_ledger (
    id                 TEXT PRIMARY KEY,
    task_id            TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL,
    user_name          TEXT NOT NULL DEFAULT '',
    user_email         TEXT NOT NULL DEFAULT '',
    task_type          TEXT NOT NULL,
    provider_id        TEXT NOT NULL DEFAULT '',
    estimated_credits  INTEGER NOT NULL DEFAULT 0,
    task_status        TEXT NOT NULL,
    params_json        TEXT NOT NULL DEFAULT '{}',
    result_urls_json   TEXT NOT NULL DEFAULT '[]',
    retry_count        INTEGER NOT NULL DEFAULT 0,
    max_retries        INTEGER NOT NULL DEFAULT 0,
    next_retry_at      TEXT,
    created_at         TEXT NOT NULL,
    finished_at        TEXT
);

CREATE INDEX IF NOT EXISTS generation_usage_ledger_task_created
    ON generation_usage_ledger(task_id, created_at, id);

CREATE INDEX IF NOT EXISTS generation_usage_ledger_user_created
    ON generation_usage_ledger(user_id, created_at DESC, id DESC);
