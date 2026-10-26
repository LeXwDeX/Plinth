CREATE TABLE harness_selections (
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    digest TEXT NOT NULL CHECK (length(digest) = 64),
    selection_json TEXT NOT NULL CHECK (json_valid(selection_json)),
    PRIMARY KEY (owner_id, session_id)
);

CREATE TABLE harness_selection_versions (
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision >= 0),
    digest TEXT NOT NULL CHECK (length(digest) = 64),
    selection_json TEXT NOT NULL CHECK (json_valid(selection_json)),
    PRIMARY KEY (owner_id, session_id, revision)
);

CREATE TRIGGER harness_selection_versions_immutable_update
BEFORE UPDATE ON harness_selection_versions
BEGIN SELECT RAISE(ABORT, 'harness selection version is immutable'); END;

ALTER TABLE execution_grants ADD COLUMN harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0);
ALTER TABLE execution_grants ADD COLUMN harness_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_generation_operations ADD COLUMN harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0);
ALTER TABLE agent_generation_operations ADD COLUMN harness_digest TEXT NOT NULL DEFAULT '';

ALTER TABLE processing_plans ADD COLUMN harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0);
ALTER TABLE processing_plans ADD COLUMN harness_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE skill_runs ADD COLUMN harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0);
ALTER TABLE skill_runs ADD COLUMN harness_digest TEXT NOT NULL DEFAULT '';
