CREATE TABLE scene_revisions (
    session_id TEXT PRIMARY KEY REFERENCES go_chat_sessions(id) ON DELETE RESTRICT,
    owner_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    updated_at TEXT NOT NULL
);
CREATE INDEX scene_revisions_owner_session ON scene_revisions(owner_id, session_id);

CREATE TABLE scene_objects (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE RESTRICT,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    active_representation_id TEXT NOT NULL,
    rect_x REAL NOT NULL, rect_y REAL NOT NULL,
    rect_width REAL NOT NULL CHECK (rect_width > 0),
    rect_height REAL NOT NULL CHECK (rect_height > 0),
    anchor_x REAL NOT NULL, anchor_y REAL NOT NULL,
    deleted_at TEXT,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX scene_objects_owner_session ON scene_objects(owner_id, session_id, created_at);

CREATE TABLE scene_representations (
    id TEXT PRIMARY KEY,
    object_id TEXT NOT NULL REFERENCES scene_objects(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('bitmap', 'vector', 'model3d', 'capture')),
    asset_id TEXT NOT NULL,
    source_task_id TEXT,
    source_representation_id TEXT REFERENCES scene_representations(id) ON DELETE RESTRICT,
    prepared_input_id TEXT,
    metadata_schema_revision TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX scene_representations_object_created ON scene_representations(object_id, created_at, id);
CREATE TRIGGER scene_representations_immutable_update BEFORE UPDATE ON scene_representations BEGIN
    SELECT RAISE(ABORT, 'scene representation is immutable');
END;
CREATE TRIGGER scene_representations_immutable_delete BEFORE DELETE ON scene_representations BEGIN
    SELECT RAISE(ABORT, 'scene representation is immutable');
END;
