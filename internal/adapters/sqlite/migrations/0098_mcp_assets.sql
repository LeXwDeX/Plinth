-- 0098: 对外 MCP 的「资产」（#332 第 4 步）。外部调用方用一个名字指代一个资产，服务端把
-- （所有者、规范化后的名字）映射到一个对话会话：任务必须挂在会话上，会话也让人能在界面的
-- 会话列表里看到外部 Agent 做的每个资产。这张表既是映射，也是「会话来源为 MCP」的标记，
-- 不改 go_chat_sessions 的任何一列，也不影响既有会话的读取路径。
--
-- name_key 是规范化（折叠空白、转小写）后的名字，主键 (owner_id, name_key) 保证同一所有者
-- 的同一个名字只有一个会话；调用方不带名字时是默认资产，name 为空串：不指定项目时
-- name_key = ''，指定项目时每个项目一个，name_key = 'Project:<项目 ID>'（含大写字母，
-- 规范化后的名字不可能与之相同）。
-- session_id 唯一：一个会话至多是一个资产。会话被删除时映射随之删除，同名的下一次调用
-- 会建新会话。时间与库里其他表一致用 RFC3339Nano 文本。
CREATE TABLE IF NOT EXISTS mcp_assets (
    owner_id    TEXT NOT NULL,
    name_key    TEXT NOT NULL,
    name        TEXT NOT NULL,
    session_id  TEXT NOT NULL UNIQUE REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (owner_id, name_key)
);

CREATE INDEX IF NOT EXISTS mcp_assets_owner_created ON mcp_assets(owner_id, created_at);
