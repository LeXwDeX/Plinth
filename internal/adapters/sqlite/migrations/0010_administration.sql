CREATE TABLE IF NOT EXISTS administration_audit_events (
    id             TEXT PRIMARY KEY,
    action         TEXT NOT NULL,
    actor_id       TEXT NOT NULL DEFAULT '',
    actor_name     TEXT NOT NULL DEFAULT '',
    resource_type  TEXT NOT NULL DEFAULT '',
    resource_id    TEXT NOT NULL DEFAULT '',
    detail_json    TEXT NOT NULL DEFAULT '{}',
    ip_address     TEXT NOT NULL DEFAULT '',
    user_agent     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS administration_audit_events_created
    ON administration_audit_events(created_at DESC);

CREATE INDEX IF NOT EXISTS administration_audit_events_action
    ON administration_audit_events(action, created_at DESC);
