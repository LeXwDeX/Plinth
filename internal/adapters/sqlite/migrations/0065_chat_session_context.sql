-- 0065: 会话思考强度、上下文用量与上下文压缩（#322）。
-- reasoning_effort 是会话选择的思考档位（空串表示用模型 profile 的默认档）。
-- context_* 是上游最近一次请求返回的 token 用量（NULL 表示上游没有返回或
-- 压缩后尚未更新），不是估算值。chat_context_compactions 每次压缩一行：
-- 摘要正文、覆盖到的最后一条消息与轮数；最新一行就是当前摘要。聊天记录本身不改。
ALTER TABLE go_chat_sessions ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN context_prompt_tokens INTEGER;
ALTER TABLE go_chat_sessions ADD COLUMN context_completion_tokens INTEGER;
ALTER TABLE go_chat_sessions ADD COLUMN context_model TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN context_updated_at TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS chat_context_compactions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    trigger TEXT NOT NULL,
    summary TEXT NOT NULL,
    through_message_id TEXT NOT NULL,
    through_created_at TEXT NOT NULL DEFAULT '',
    rounds INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_context_compactions_session
    ON chat_context_compactions(owner_id, session_id, created_at);
