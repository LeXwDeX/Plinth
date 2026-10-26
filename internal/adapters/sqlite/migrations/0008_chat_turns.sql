CREATE TABLE IF NOT EXISTS go_chat_turns (
    assistant_message_id  TEXT PRIMARY KEY,
    session_id            TEXT NOT NULL,
    owner_id              TEXT NOT NULL,
    options_json          TEXT NOT NULL,
    state                 TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    FOREIGN KEY(assistant_message_id) REFERENCES go_chat_messages(id) ON DELETE CASCADE,
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_go_chat_turns_state_created
    ON go_chat_turns(state, created_at ASC);
