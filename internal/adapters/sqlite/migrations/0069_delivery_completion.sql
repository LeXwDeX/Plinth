-- 0069: 本地交付完成（#330 第三轮后续）。失败转换 Task 从精确关联的
-- 已交付 Task 复制自己的资产后，记录一个本地终态 Attempt。provenance
-- 只存在该 Attempt 上；普通 Attempt 保持 NULL。唯一索引保证每个 Task
-- 至多一个本地完成。
ALTER TABLE generation_attempts ADD COLUMN delivery_completion_json TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS generation_attempts_delivery_completion
    ON generation_attempts(task_id) WHERE delivery_completion_json IS NOT NULL;
