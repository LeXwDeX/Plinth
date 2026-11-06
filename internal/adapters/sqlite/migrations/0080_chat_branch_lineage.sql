ALTER TABLE go_chat_sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN parent_message_id TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN parent_title TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN parent_deleted INTEGER NOT NULL DEFAULT 0 CHECK (parent_deleted IN (0, 1));

CREATE INDEX idx_go_chat_sessions_parent ON go_chat_sessions(owner_id, parent_session_id);
