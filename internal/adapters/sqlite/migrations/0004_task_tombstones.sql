CREATE TABLE IF NOT EXISTS generation_task_tombstones (
    task_id     TEXT PRIMARY KEY REFERENCES generation_tasks(id) ON DELETE CASCADE,
    actor_id    TEXT NOT NULL,
    deleted_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS generation_task_tombstones_deleted_at
    ON generation_task_tombstones(deleted_at DESC, task_id DESC);
