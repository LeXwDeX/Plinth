-- 0060: 上游结果重新取回（#295）。上游已经生成成功、本地入库失败的 Task，
-- 可以新建一个“重新取回”Task：它不调用上游创建接口，只按来源 Attempt 已受理的
-- 上游作业再查询一次结果并走原有的下载、校验、入库流程。这里记录新 Task 与来源
-- Task/Attempt 的绑定、序号、发起方（owner/agent）和时间，与新 Task 同一事务写入；
-- 来源 Task 及其 Attempt、检查点和计费记录都不改动。
CREATE TABLE IF NOT EXISTS generation_upstream_refetches (
    task_id           TEXT PRIMARY KEY REFERENCES generation_tasks(id) ON DELETE CASCADE,
    owner_id          TEXT NOT NULL,
    session_id        TEXT NOT NULL DEFAULT '',
    source_task_id    TEXT NOT NULL,
    source_attempt_id TEXT NOT NULL,
    ordinal           INTEGER NOT NULL,
    requested_by      TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    UNIQUE(source_attempt_id, ordinal)
);

CREATE INDEX IF NOT EXISTS generation_upstream_refetches_source
    ON generation_upstream_refetches(owner_id, source_task_id);

CREATE INDEX IF NOT EXISTS generation_upstream_refetches_session
    ON generation_upstream_refetches(owner_id, session_id);
