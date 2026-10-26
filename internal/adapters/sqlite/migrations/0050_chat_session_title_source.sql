-- Where a chat session's title came from: 'pending' (still the default title,
-- waiting for the automatic summary), 'auto' (written once by the Agent model
-- after the first turn) or 'user' (set by a person, never overwritten).
-- Existing sessions already have the title they will keep.
ALTER TABLE go_chat_sessions ADD COLUMN title_source TEXT NOT NULL DEFAULT 'user';
