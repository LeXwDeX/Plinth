CREATE TABLE IF NOT EXISTS generation_prepared_inputs (
    id              TEXT PRIMARY KEY,
    owner_id        TEXT NOT NULL,
    session_id      TEXT NOT NULL DEFAULT '',
    input_hash      TEXT NOT NULL,
    payload_json    TEXT NOT NULL CHECK (json_valid(payload_json)),
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS generation_prepared_inputs_owner_session
ON generation_prepared_inputs(owner_id, session_id, created_at DESC, id DESC);

CREATE TRIGGER IF NOT EXISTS generation_prepared_inputs_immutable
BEFORE UPDATE ON generation_prepared_inputs
BEGIN
    SELECT RAISE(ABORT, 'prepared inputs are immutable');
END;
