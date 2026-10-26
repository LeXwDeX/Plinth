-- The list read model contains only presentation identity and semantic prompt.
-- Triggers keep imports and all task writers consistent with the same snapshot.
CREATE TABLE generation_task_summaries (
 task_id TEXT PRIMARY KEY REFERENCES generation_tasks(id) ON DELETE CASCADE,
 offering_json TEXT NOT NULL,
 prompt_input_json TEXT NOT NULL
);
INSERT INTO generation_task_summaries (task_id, offering_json, prompt_input_json) SELECT task.id, json_object(
    'id', json_extract(task.offering_json, '$.id'),
    'provider_id', json_extract(task.offering_json, '$.provider_id'),
    'model_id', json_extract(task.offering_json, '$.model_id'),
    'estimated_credits', json_extract(task.offering_json, '$.estimated_credits'),
    'parameters', json((SELECT json_group_array(json_object('name', json_extract(value, '$.name'), 'semantic', 'prompt'))
        FROM json_each(task.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt'))
), COALESCE((SELECT json_group_object(input.key, input.value)
    FROM json_each(task.input_json) AS input
    WHERE input.key IN (SELECT json_extract(value, '$.name') FROM json_each(task.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt')), '{}') FROM generation_tasks task;
CREATE TRIGGER generation_task_summary_insert AFTER INSERT ON generation_tasks
BEGIN
 INSERT INTO generation_task_summaries (task_id, offering_json, prompt_input_json) VALUES (NEW.id, json_object(
    'id', json_extract(NEW.offering_json, '$.id'),
    'provider_id', json_extract(NEW.offering_json, '$.provider_id'),
    'model_id', json_extract(NEW.offering_json, '$.model_id'),
    'estimated_credits', json_extract(NEW.offering_json, '$.estimated_credits'),
    'parameters', json((SELECT json_group_array(json_object('name', json_extract(value, '$.name'), 'semantic', 'prompt'))
        FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt'))
), COALESCE((SELECT json_group_object(input.key, input.value)
    FROM json_each(NEW.input_json) AS input
    WHERE input.key IN (SELECT json_extract(value, '$.name') FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt')), '{}'))
 ON CONFLICT(task_id) DO UPDATE SET offering_json = excluded.offering_json, prompt_input_json = excluded.prompt_input_json;
END;
CREATE TRIGGER generation_task_summary_update AFTER UPDATE OF offering_json, input_json ON generation_tasks
WHEN NEW.offering_json IS NOT OLD.offering_json OR NEW.input_json IS NOT OLD.input_json
BEGIN
 INSERT INTO generation_task_summaries (task_id, offering_json, prompt_input_json) VALUES (NEW.id, json_object(
    'id', json_extract(NEW.offering_json, '$.id'),
    'provider_id', json_extract(NEW.offering_json, '$.provider_id'),
    'model_id', json_extract(NEW.offering_json, '$.model_id'),
    'estimated_credits', json_extract(NEW.offering_json, '$.estimated_credits'),
    'parameters', json((SELECT json_group_array(json_object('name', json_extract(value, '$.name'), 'semantic', 'prompt'))
        FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt'))
), COALESCE((SELECT json_group_object(input.key, input.value)
    FROM json_each(NEW.input_json) AS input
    WHERE input.key IN (SELECT json_extract(value, '$.name') FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt')), '{}'))
 ON CONFLICT(task_id) DO UPDATE SET offering_json = excluded.offering_json, prompt_input_json = excluded.prompt_input_json;
END;
CREATE INDEX generation_tasks_created ON generation_tasks(created_at DESC, id DESC);
CREATE INDEX generation_tasks_active_created ON generation_tasks(CASE WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0 ELSE 1 END, created_at DESC, id DESC);
CREATE INDEX generation_tasks_owner_active_created ON generation_tasks(owner_id, CASE WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0 ELSE 1 END, created_at DESC, id DESC);
CREATE INDEX generation_tasks_owner_capability_active_created ON generation_tasks(owner_id, capability, CASE WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0 ELSE 1 END, created_at DESC, id DESC);
CREATE INDEX generation_tasks_capability_active_created ON generation_tasks(capability, CASE WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0 ELSE 1 END, created_at DESC, id DESC);
CREATE INDEX generation_tasks_unix_created ON generation_tasks(unixepoch(created_at,'subsec') DESC,id DESC);
CREATE INDEX generation_tasks_owner_unix_created ON generation_tasks(owner_id,unixepoch(created_at,'subsec') DESC,id DESC);
CREATE INDEX generation_usage_ledger_unix_created ON generation_usage_ledger(unixepoch(created_at,'subsec') DESC,task_id DESC);
CREATE INDEX generation_usage_ledger_user_unix_created ON generation_usage_ledger(user_id,unixepoch(created_at,'subsec') DESC,task_id DESC);
