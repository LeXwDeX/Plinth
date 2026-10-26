CREATE TABLE model_captures (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE RESTRICT,
    source_task_id TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE RESTRICT,
    model_asset_id TEXT NOT NULL REFERENCES generation_assets(id) ON DELETE RESTRICT,
    screenshot_asset_id TEXT NOT NULL REFERENCES generation_input_assets(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    command_hash TEXT NOT NULL,
    view_json TEXT NOT NULL CHECK (json_valid(view_json)),
    pixel_width INTEGER NOT NULL CHECK (pixel_width > 0),
    pixel_height INTEGER NOT NULL CHECK (pixel_height > 0),
    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(owner_id, idempotency_key)
);
CREATE INDEX model_captures_owner_session ON model_captures(owner_id, session_id, created_at DESC);
CREATE INDEX model_captures_screenshot ON model_captures(screenshot_asset_id);
CREATE TRIGGER model_captures_immutable_update BEFORE UPDATE ON model_captures BEGIN
    SELECT RAISE(ABORT, 'model captures are immutable');
END;
CREATE TRIGGER model_captures_immutable_delete BEFORE DELETE ON model_captures BEGIN
    SELECT RAISE(ABORT, 'model captures are immutable');
END;
