-- 0116: 对话发送的幂等键（#665）。
--
-- 浏览器开始一轮对话时可带 Idempotency-Key；服务端在写入轮次的同一事务里记下
-- (owner, 会话, 键) → 请求摘要与该轮的两条消息 ID。应答丢失后浏览器用同一个键
-- 重发，服务端按记录回答原来那一轮，不再开始第二轮付费回合；同一键配不同请求
-- 摘要回答 409。记录在 expires_at（写入时间加 Product Schema
-- chat.api.turn_idempotency.retention_seconds）之后失效，之后任何一次带键的
-- 轮次写入都会删除失效记录；会话或该轮回复消息删除时随之级联删除。
-- 时间列用定宽 UTC 纳秒格式（同 formatTaskTime），按文本比较即按时间比较。
-- 只新增表和索引，旧库原有数据不变。

CREATE TABLE IF NOT EXISTS go_chat_turn_keys (
    owner_id             TEXT NOT NULL,
    session_id           TEXT NOT NULL,
    idempotency_key      TEXT NOT NULL,
    fingerprint          TEXT NOT NULL,
    user_message_id      TEXT NOT NULL,
    assistant_message_id TEXT NOT NULL,
    created_at           TEXT NOT NULL,
    expires_at           TEXT NOT NULL,
    PRIMARY KEY (owner_id, session_id, idempotency_key),
    FOREIGN KEY(session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
    FOREIGN KEY(assistant_message_id) REFERENCES go_chat_messages(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_go_chat_turn_keys_expires
    ON go_chat_turn_keys(expires_at);
CREATE INDEX IF NOT EXISTS idx_go_chat_turn_keys_assistant
    ON go_chat_turn_keys(assistant_message_id);
