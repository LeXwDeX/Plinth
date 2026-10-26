-- Stop intent survives a lost paid Submit response, canceled worker, and restart.
-- Turn state remains QUEUED/RUNNING until reconciliation has completed.
ALTER TABLE go_chat_turns ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;
