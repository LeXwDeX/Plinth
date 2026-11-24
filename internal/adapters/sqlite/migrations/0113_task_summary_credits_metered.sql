-- 0113: 任务摘要投影带上冻结服务的计费标记（#517）。
--
-- 对外 MCP 的 get_task、get_task_assets、list_tasks 经摘要投影读任务，失败归因
-- （generationtool.TaskFailureReport）要知道冻结的 Offering 是否按积分计费：计费服务
-- 在 Schema 声明了「不自动重试」文案的失败类别必须让调用方去问用户、不可重试。
-- 0020 的投影只存展示身份与提示词参数，没有 credits_metered，这几个工具因此总把
-- 计费失败当成可重试。
--
-- 两个触发器按 0020 原样重建，只多写 credits_metered（布尔，缺省 false）。存量行只回填
-- 计费任务那部分：没有这个键的旧行解码为 false，与未计费一致，不必改写；回填条件排除
-- 已是 true 的行，重复执行不再写。一条 UPDATE 只碰计费任务的摘要行，按主键查找。
-- 旧二进制解码时忽略多出的键，回滚无需 down 迁移。
DROP TRIGGER IF EXISTS generation_task_summary_insert;
DROP TRIGGER IF EXISTS generation_task_summary_update;
CREATE TRIGGER generation_task_summary_insert AFTER INSERT ON generation_tasks
BEGIN
 INSERT INTO generation_task_summaries (task_id, offering_json, prompt_input_json) VALUES (NEW.id, json_object(
    'id', json_extract(NEW.offering_json, '$.id'),
    'provider_id', json_extract(NEW.offering_json, '$.provider_id'),
    'model_id', json_extract(NEW.offering_json, '$.model_id'),
    'estimated_credits', json_extract(NEW.offering_json, '$.estimated_credits'),
    'credits_metered', json(CASE WHEN json_extract(NEW.offering_json, '$.credits_metered') = 1 THEN 'true' ELSE 'false' END),
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
    'credits_metered', json(CASE WHEN json_extract(NEW.offering_json, '$.credits_metered') = 1 THEN 'true' ELSE 'false' END),
    'parameters', json((SELECT json_group_array(json_object('name', json_extract(value, '$.name'), 'semantic', 'prompt'))
        FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt'))
), COALESCE((SELECT json_group_object(input.key, input.value)
    FROM json_each(NEW.input_json) AS input
    WHERE input.key IN (SELECT json_extract(value, '$.name') FROM json_each(NEW.offering_json, '$.parameters') WHERE json_extract(value, '$.semantic') = 'prompt')), '{}'))
 ON CONFLICT(task_id) DO UPDATE SET offering_json = excluded.offering_json, prompt_input_json = excluded.prompt_input_json;
END;
UPDATE generation_task_summaries
SET offering_json = json_set(offering_json, '$.credits_metered', json('true'))
WHERE task_id IN (SELECT id FROM generation_tasks WHERE json_extract(offering_json, '$.credits_metered') = 1)
  AND json_extract(offering_json, '$.credits_metered') IS NOT 1;
