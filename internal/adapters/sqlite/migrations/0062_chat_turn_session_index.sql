-- 0062: 会话读取按会话列最近轮次（#318 恢复分域模型）以及活动轮次读取
-- （ListSessionTurns）都按 session_id 过滤；此前只有按 state 的索引，
-- 每次都要扫全表。只加索引，不改数据，重复执行幂等。
CREATE INDEX IF NOT EXISTS idx_go_chat_turns_session_created
    ON go_chat_turns(session_id, created_at);
