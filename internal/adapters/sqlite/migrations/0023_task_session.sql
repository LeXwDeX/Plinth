-- Public generation steps belong to a stable creative session when submitted
-- through Chat. Legacy standalone tasks retain NULL session ownership.
ALTER TABLE generation_tasks
    ADD COLUMN session_id TEXT REFERENCES go_chat_sessions(id) ON DELETE SET NULL;

CREATE INDEX generation_tasks_owner_session_created
    ON generation_tasks(owner_id, session_id, created_at DESC, id DESC)
    WHERE session_id IS NOT NULL;

-- Backfill only an exact, owner-matching tool-call binding with one session.
-- Ambiguous references and tasks with no recorded binding remain unassigned.
WITH matches AS (
    SELECT task.id AS task_id, session.id AS session_id
    FROM generation_tasks AS task
    JOIN go_chat_sessions AS session ON session.owner_id = task.owner_id
    JOIN go_chat_messages AS message ON message.session_id = session.id
    JOIN json_each(CASE WHEN json_valid(message.tool_calls)
        THEN message.tool_calls ELSE '[]' END) AS call
      ON CASE WHEN json_valid(call.value)
          THEN json_extract(call.value, '$.task_id') ELSE NULL END = task.id
), unique_matches AS (
    SELECT task_id, MIN(session_id) AS session_id
    FROM matches
    GROUP BY task_id
    HAVING COUNT(DISTINCT session_id) = 1
)
UPDATE generation_tasks
SET session_id = (
    SELECT session_id FROM unique_matches WHERE task_id = generation_tasks.id
)
WHERE id IN (SELECT task_id FROM unique_matches);
