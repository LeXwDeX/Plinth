-- Receipts attest only to existing bytes; no historical Task/Attempt is updated.
CREATE TABLE generation_recovery_receipts (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
 source_task_id TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
 source_attempt_id TEXT NOT NULL REFERENCES generation_attempts(id) ON DELETE CASCADE,
 digest TEXT NOT NULL UNIQUE,
 payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
 created_at TEXT NOT NULL,
 UNIQUE(source_task_id, source_attempt_id)
);
CREATE INDEX generation_recovery_receipts_owner_source ON generation_recovery_receipts(owner_id, source_task_id);
CREATE TRIGGER generation_recovery_receipts_immutable
 BEFORE UPDATE ON generation_recovery_receipts BEGIN
 SELECT RAISE(ABORT, 'recovery receipts are immutable'); END;
