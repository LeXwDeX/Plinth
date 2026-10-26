ALTER TABLE go_chat_turns ADD COLUMN checkpoint_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE go_chat_turns ADD COLUMN checkpoint_version INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_go_chat_turns_state_cursor
ON go_chat_turns(state, created_at, assistant_message_id);
