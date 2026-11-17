-- 0096: 个人访问 Key（#332）。每个 Key 一行：id 是 Key 里公开的那一段，prefix 是
-- 可见的识别前缀（"<前缀>_<id>"，不含任何随机密钥位），secret_hash 是整把 Key 的
-- SHA-256（十六进制），明文只在创建时返回一次，库里没有。scopes_json 是权限范围数组，
-- 取值由 Product Schema application.access_keys 声明并在签发时校验，这里不加取值 CHECK。
-- 时间与库里其他表一致用 RFC3339Nano 文本，空字符串表示未设置；吊销只写 revoked_at，
-- 不删行，列表仍能看到。用户为软删除，无需级联。
CREATE TABLE IF NOT EXISTS access_keys (
    id            TEXT PRIMARY KEY,
    owner_id      TEXT NOT NULL,
    name          TEXT NOT NULL,
    prefix        TEXT NOT NULL,
    secret_hash   TEXT NOT NULL,
    scopes_json   TEXT NOT NULL CHECK (json_valid(scopes_json) AND json_type(scopes_json) = 'array'),
    created_at    TEXT NOT NULL,
    last_used_at  TEXT NOT NULL DEFAULT '',
    expires_at    TEXT NOT NULL DEFAULT '',
    revoked_at    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS access_keys_owner ON access_keys(owner_id, created_at);
