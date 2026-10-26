-- 0066: 管理员 LLM 设置与会话对话模型（#322 Scope 2、5）。
-- llm_settings_versions 与 agent_configuration_versions 同形：每次发布、恢复或
-- 重置追加一个版本，revision 递增，写入按 expected_revision 做并发检查。
-- 设置里只有连接 ID、开放与否、档位和默认模型，不含地址、密钥或模型名。
-- llm_connection_checks 每个连接一行，记最近一次「测试连接」的分类结果
-- （不记上游错误正文）。go_chat_sessions.chat_model 是会话选的连接 ID，
-- 空串表示跟随管理员的默认模型。
CREATE TABLE IF NOT EXISTS llm_settings_versions (
    revision INTEGER PRIMARY KEY,
    settings_json TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS llm_connection_checks (
    connection_id TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    checked_at TEXT NOT NULL,
    reachable INTEGER NOT NULL,
    vision TEXT NOT NULL,
    outcome TEXT NOT NULL,
    actor_id TEXT NOT NULL
);
ALTER TABLE go_chat_sessions ADD COLUMN chat_model TEXT NOT NULL DEFAULT '';
