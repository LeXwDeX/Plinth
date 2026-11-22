-- 0109: 生成成功后自动补齐缺失格式的持久记录（#461）。
--
-- 七种格式由平台保证交付（#457）：生成成功后，缺失的格式先走免费的本地路线
-- （local/generated-glb/to-gltf），再自动走付费的 Meshy Convert。平台自己
-- 发起的计划不能靠内存记住：重启后既要继续推进在途的转换，也不能为同一个
-- 任务再付一次费。
--
-- 一行一个任务：策略版本（在哪个 Offering 修订下建的计划）、状态、尝试
-- 次数、最后一个计划、已经花掉的积分与付费次数、原因、下次执行时间。扫描
-- 位置单独一行（水位线），首次启用写当前时间，历史任务永不回扫。
CREATE TABLE IF NOT EXISTS model_delivery_auto_completions (
    task_id         TEXT PRIMARY KEY,
    owner_id        TEXT NOT NULL,
    policy_revision TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    plan_id         TEXT NOT NULL DEFAULT '',
    paid_credits    INTEGER NOT NULL DEFAULT 0,
    paid_runs       INTEGER NOT NULL DEFAULT 0,
    reason          TEXT NOT NULL DEFAULT '',
    next_run_at     TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS model_delivery_auto_completions_due
    ON model_delivery_auto_completions(next_run_at, task_id);

CREATE TABLE IF NOT EXISTS model_delivery_auto_completion_watermark (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    updated_at TEXT NOT NULL,
    task_id    TEXT NOT NULL DEFAULT ''
);