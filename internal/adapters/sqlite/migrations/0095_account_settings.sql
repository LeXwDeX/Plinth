-- 0095: 每个用户的账户设置（#330）。目前只有 3D 步数模式：资产列表与外部 MCP 各一份，
-- 模式（agent / unlimited / cap）与步数上限。模式字符串由 Product Schema
-- application.step_policy 声明并在写入时校验，这里不加 CHECK，以免改 Schema 时需要重建表。
-- 没有行表示使用 Schema 默认值（version 为 0）；写入用 version 做乐观并发，
-- 首次写入（期望版本 0）插入 version 1。用户为软删除，无需级联。
CREATE TABLE IF NOT EXISTS account_settings (
    owner_id        TEXT PRIMARY KEY,
    list_step_mode  TEXT NOT NULL,
    list_step_cap   INTEGER NOT NULL,
    mcp_step_mode   TEXT NOT NULL,
    mcp_step_cap    INTEGER NOT NULL,
    version         INTEGER NOT NULL,
    updated_at      TEXT NOT NULL
);
