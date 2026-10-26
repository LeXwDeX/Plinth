-- 登录失败限流持久化。此前计数只在进程内存里（httpapi.loginFailureLimiter），
-- 重启即清零：攻击者可以"撞满阈值 → 等一次重启 → 再撞"的节奏绕开限流，
-- 多副本部署时每个进程更是各算各的。落库后限流随部署存活。
CREATE TABLE IF NOT EXISTS authentication_login_failures (
    client_key  TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS authentication_login_failures_key_time
    ON authentication_login_failures(client_key, occurred_at DESC);
CREATE INDEX IF NOT EXISTS authentication_login_failures_time
    ON authentication_login_failures(occurred_at);
