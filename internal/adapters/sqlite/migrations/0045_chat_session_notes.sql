-- The settings summary of a chat session: creative settings the user and the
-- Agent agreed on, injected into every Agent request. Existing sessions start
-- with an empty summary. Length is bounded by Product Schema, not here.
ALTER TABLE go_chat_sessions ADD COLUMN notes TEXT NOT NULL DEFAULT '';
