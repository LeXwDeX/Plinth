-- 0022: 成功后观察资源态（W6，§6.5）。内核所有；写者唯一
-- （成功提交初始化、合并更新、deadline finalizer），读者为领取谓词
-- 与公开 resources[] 投影。NULL = 无成功后观察（旧任务零行为变化）。
ALTER TABLE generation_tasks ADD COLUMN resource_state_json TEXT;
