CREATE TABLE IF NOT EXISTS go_canvas_projects (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL,
    name        TEXT NOT NULL,
    session_id  TEXT UNIQUE,
    viewport    TEXT NOT NULL,
    bg_style    TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_go_canvas_projects_owner_updated
    ON go_canvas_projects(owner_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS go_canvas_nodes (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    type        TEXT NOT NULL,
    x           REAL NOT NULL,
    y           REAL NOT NULL,
    width       REAL NOT NULL,
    height      REAL NOT NULL,
    z_index     INTEGER NOT NULL,
    data_json   TEXT NOT NULL,
    visible     INTEGER NOT NULL,
    locked      INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    FOREIGN KEY(project_id) REFERENCES go_canvas_projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_go_canvas_nodes_project_z
    ON go_canvas_nodes(project_id, z_index ASC);
