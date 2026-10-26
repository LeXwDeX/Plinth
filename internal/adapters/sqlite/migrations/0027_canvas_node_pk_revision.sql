-- B15：节点主键改为 (project_id, id) 复合——跨项目/跨用户撞节点 ID
-- 不再互相覆盖或 500；项目加 revision 乐观锁，后写覆盖先写被拒绝。
CREATE TABLE IF NOT EXISTS go_canvas_nodes_v2 (
    project_id TEXT NOT NULL,
    id         TEXT NOT NULL,
    type       TEXT NOT NULL,
    x          REAL NOT NULL,
    y          REAL NOT NULL,
    width      REAL NOT NULL,
    height     REAL NOT NULL,
    z_index    INTEGER NOT NULL,
    data_json  TEXT NOT NULL,
    visible    INTEGER NOT NULL,
    locked     INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY(project_id) REFERENCES go_canvas_projects(id) ON DELETE CASCADE
);
INSERT INTO go_canvas_nodes_v2
    (project_id, id, type, x, y, width, height, z_index, data_json, visible, locked, created_at, updated_at)
SELECT project_id, id, type, x, y, width, height, z_index, data_json, visible, locked, created_at, updated_at
FROM go_canvas_nodes
WHERE true
ON CONFLICT (project_id, id) DO NOTHING;
DROP TABLE go_canvas_nodes;
ALTER TABLE go_canvas_nodes_v2 RENAME TO go_canvas_nodes;
CREATE INDEX IF NOT EXISTS idx_go_canvas_nodes_project_z
    ON go_canvas_nodes(project_id, z_index ASC);

ALTER TABLE go_canvas_projects ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
