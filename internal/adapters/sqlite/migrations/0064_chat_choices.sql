-- 0064: 对话内选择卡（#321）。工具在同一能力有多个兼容服务、用户没有指定时
-- 存一张选择卡；卡片按 owner/会话归属，刷新、换会话、换设备后都能读到。
-- 作答一次后不可再改（state 由 pending 变为 answered）；新卡会让同一会话里
-- 尚未作答的旧卡失效（superseded）。只新增表和索引，重复执行幂等。
CREATE TABLE IF NOT EXISTS chat_choices (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    assistant_message_id TEXT NOT NULL,
    call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    capability TEXT NOT NULL,
    step_index INTEGER NOT NULL,
    options_json TEXT NOT NULL,
    default_option_id TEXT NOT NULL,
    allow_default INTEGER NOT NULL,
    default_arguments TEXT NOT NULL,
    state TEXT NOT NULL,
    answer_option_id TEXT NOT NULL DEFAULT '',
    answer_default INTEGER NOT NULL DEFAULT 0,
    answer_source TEXT NOT NULL DEFAULT '',
    answer_message_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    answered_at TEXT NOT NULL DEFAULT '',
    UNIQUE (owner_id, session_id, assistant_message_id, call_id)
);
CREATE INDEX IF NOT EXISTS idx_chat_choices_session
    ON chat_choices(owner_id, session_id, created_at);
