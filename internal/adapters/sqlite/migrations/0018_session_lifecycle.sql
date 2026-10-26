CREATE TABLE authentication_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    issued_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE INDEX idx_authentication_sessions_expiry ON authentication_sessions(expires_at);
ALTER TABLE go_chat_streams ADD COLUMN assistant_message_id TEXT NOT NULL DEFAULT '';
UPDATE go_chat_streams SET assistant_message_id = COALESCE((
    SELECT assistant_message_id FROM go_chat_turns
    WHERE go_chat_turns.session_id = go_chat_streams.session_id
    ORDER BY created_at DESC LIMIT 1
), '');
