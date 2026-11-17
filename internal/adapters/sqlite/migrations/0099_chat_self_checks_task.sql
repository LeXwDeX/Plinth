-- 0099: 按任务读取自检记录（#332 第 7c-2 步）。对外 MCP 的状态块要知道平台对一条已完成
-- 3D 任务的最后一次自检结论，但记录写在发起审阅的会话里：任务被人挪到分支会话、或自检
-- 发生在祖先会话时，只按 session_id 查不到。这里只补一个 (owner_id, task_id) 的索引，
-- 让跨会话按任务取最新一条不必扫表；不改表结构，也不改变记录的写入路径。
CREATE INDEX IF NOT EXISTS idx_chat_self_checks_task ON chat_self_checks(owner_id, task_id, updated_at DESC);