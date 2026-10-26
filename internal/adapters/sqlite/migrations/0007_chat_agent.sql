CREATE TABLE IF NOT EXISTS go_chat_sessions (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL,
    owner_name  TEXT NOT NULL,
    title       TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_go_chat_sessions_owner_updated
    ON go_chat_sessions(owner_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS go_chat_messages (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    image_urls  TEXT NOT NULL,
    tool_calls  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_go_chat_messages_session_created
    ON go_chat_messages(session_id, created_at ASC);

CREATE TABLE IF NOT EXISTS go_chat_streams (
    session_id  TEXT PRIMARY KEY,
    status      TEXT NOT NULL,
    cleanup_at  TEXT,
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS go_chat_events (
    session_id  TEXT NOT NULL,
    event_id    INTEGER NOT NULL,
    data_json   TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY(session_id, event_id),
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_go_chat_events_session_created
    ON go_chat_events(session_id, created_at ASC);
