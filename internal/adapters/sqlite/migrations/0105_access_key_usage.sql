-- 0105: 个人访问 Key 的使用统计与整体开关（#332 管理端）。request_count 是累计
-- 已通过鉴权的请求数，每次加一在库里完成，并发写不丢计数；last_used_at 仍由
-- 0096 的列记录最近一次使用。access_key_settings 只有一个键
-- access_keys.enabled；没有这一行时取 Product Schema application.access_keys.enabled
-- 声明的默认值。只追加，不改既有列。
ALTER TABLE access_keys ADD COLUMN request_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS access_key_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);