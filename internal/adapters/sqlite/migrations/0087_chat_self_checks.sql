-- 0087: 会话级 3D 自检记录（#246）。一条记录对应 (owner, session, task)：
-- 服务端按真实只读工具调用追加 checks，Agent 用 record_self_check 写结论。
-- 记录是会话数据，不改写 generation_tasks 的终态；status 是结论状态的去规范
-- 副本，仅用于列表与提示注入，真相在 record_json。会话或任务删除时级联清理。
CREATE TABLE chat_self_checks (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
  model_asset_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  record_json TEXT NOT NULL CHECK (json_valid(record_json)),
  revision INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(owner_id, session_id, task_id)
);
CREATE INDEX idx_chat_self_checks_session ON chat_self_checks(session_id, updated_at DESC);