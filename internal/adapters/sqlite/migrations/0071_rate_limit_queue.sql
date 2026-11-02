-- 0071: 上游限流排队计数。被上游明确限流拒绝（未受理、不计费）的 Attempt
-- 让任务回到 QUEUED 等待，而不消耗 Retry.MaxAttempts；该列记录截至本
-- Attempt（含它自己，若它被排队）累计的排队次数，下一个 Attempt 逐值继承，
-- 重试判定以 ordinal - rate_limit_queued 作为有效序号。历史行为 0。
ALTER TABLE generation_attempts ADD COLUMN rate_limit_queued INTEGER NOT NULL DEFAULT 0;
