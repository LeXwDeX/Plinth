-- AGENTS 模式的会话级 agent 状态（请求参数、任务绑定、同意记录）
-- 持久化：重启后待批准的同意门禁仍然生效，参数与绑定不丢。
CREATE TABLE IF NOT EXISTS go_agent_state (
    owner_id   TEXT NOT NULL,
    scope      TEXT NOT NULL,
    state_key  TEXT NOT NULL,
    value_json TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (owner_id, scope, state_key)
);
CREATE INDEX IF NOT EXISTS idx_go_agent_state_scope
    ON go_agent_state (owner_id, scope);
