CREATE TABLE agent_generation_operations (
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    assistant_message_id TEXT NOT NULL,
    call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    command_hash TEXT NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    idempotency_key TEXT NOT NULL,
    prepared_input_id TEXT NOT NULL REFERENCES generation_prepared_inputs(id) ON DELETE RESTRICT,
    prepared_input_hash TEXT NOT NULL,
    offering_id TEXT NOT NULL,
    offering_revision TEXT NOT NULL,
    grant_request_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (owner_id, session_id, assistant_message_id, call_id),
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX agent_generation_operations_grant ON agent_generation_operations(owner_id, grant_request_id)
WHERE grant_request_id <> '';
