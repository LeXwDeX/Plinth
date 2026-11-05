-- 0078: 上游排队（#131）。Provider 已受理作业但仍在上游自己的队列里时，
-- 记录最近一次 accepted/running 观察报告的排队信息（JSON，如 {"ahead":1313}；
-- 没有前方任务数时为 {}）。NULL 表示最近一次观察没有报告排队。任务读取只对
-- 运行中的当前 Attempt 投影该列；历史行为 NULL。
ALTER TABLE generation_attempts ADD COLUMN last_observation_queue_json TEXT;
