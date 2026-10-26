-- 0021: 私有派发检查点（W4，§4.2）。内核所有；普通提交与 upsertAttempt
-- 的显式列更新永不触碰该列。
ALTER TABLE generation_attempts ADD COLUMN dispatch_checkpoint_json TEXT;
