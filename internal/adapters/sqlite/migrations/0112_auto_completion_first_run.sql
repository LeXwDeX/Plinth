-- 0112: 自动补齐记录的首次处理时间（#478）。
--
-- auto_completion.deadline_hours 说的是「一个记录最多被扫多久」。基准必须是记录
-- 第一次被处理的时间，而 next_run_at 每轮都会被扫描间隔刷新，用它作基准的话
-- now.After(next_run_at + deadline) 恒为 false，超时永远判不到：记录每轮重扫、
-- 永不置 done，还长期占着批次槽位。
--
-- 既有行用 updated_at 回填：那是它们上一次被看的时间，再旧的记录也已经有了一个
-- 明确的起点。旧二进制不读这一列（查询与插入都显式列出列名），回滚无需 down 迁移。
ALTER TABLE model_delivery_auto_completions ADD COLUMN first_run_at TEXT NOT NULL DEFAULT '';
UPDATE model_delivery_auto_completions SET first_run_at = updated_at WHERE first_run_at = '';