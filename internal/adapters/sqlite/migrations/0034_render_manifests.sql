CREATE TABLE render_manifests (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE RESTRICT,
    scene_revision INTEGER NOT NULL CHECK (scene_revision >= 1),
    frame_x REAL NOT NULL, frame_y REAL NOT NULL,
    frame_width REAL NOT NULL CHECK (frame_width > 0),
    frame_height REAL NOT NULL CHECK (frame_height > 0),
    pixel_width INTEGER NOT NULL CHECK (pixel_width > 0),
    pixel_height INTEGER NOT NULL CHECK (pixel_height > 0),
    matrix_json TEXT NOT NULL,
    inputs_json TEXT NOT NULL,
    renderer_version TEXT NOT NULL,
    output_asset_id TEXT NOT NULL,
    output_size_bytes INTEGER NOT NULL CHECK (output_size_bytes > 0),
    output_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX render_manifests_owner_session ON render_manifests(owner_id, session_id, created_at, id);
CREATE TRIGGER render_manifests_immutable_update BEFORE UPDATE ON render_manifests BEGIN
    SELECT RAISE(ABORT, 'render manifest is immutable');
END;
CREATE TRIGGER render_manifests_immutable_delete BEFORE DELETE ON render_manifests BEGIN
    SELECT RAISE(ABORT, 'render manifest is immutable');
END;
