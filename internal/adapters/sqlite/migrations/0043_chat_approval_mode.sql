-- Paid-step approval mode per chat session. Existing and new sessions start in
-- manual mode; the Product Schema decides which capabilities still need a human.
ALTER TABLE go_chat_sessions ADD COLUMN approval_mode TEXT NOT NULL DEFAULT 'manual'
    CHECK (approval_mode IN ('auto', 'manual'));
