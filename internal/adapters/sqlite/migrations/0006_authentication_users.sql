CREATE TABLE IF NOT EXISTS authentication_users (
    id             TEXT PRIMARY KEY,
    username       TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL DEFAULT '',
    nickname       TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    roles_json     TEXT NOT NULL DEFAULT '[]',
    is_admin       INTEGER NOT NULL DEFAULT 0,
    is_active      INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    deleted_at     TEXT,
    last_login     TEXT
);

CREATE INDEX IF NOT EXISTS authentication_users_active_username
    ON authentication_users(username, is_active)
    WHERE deleted_at IS NULL;
