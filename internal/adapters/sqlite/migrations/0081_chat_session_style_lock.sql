-- Session-level style lock settings. Existing sessions start disabled with no
-- text or references. The reference list keeps the request order as JSON.
ALTER TABLE go_chat_sessions ADD COLUMN style_lock_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE go_chat_sessions ADD COLUMN style_lock_text TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN style_lock_references TEXT NOT NULL DEFAULT '[]';
