-- 0091: 会话级偏好（#241）。
--
-- 偏好是服务端按事实记录的会话取舍：选用（selected/pick）、显式否决
-- （rejected/reject，带预设与自由原因）与弱信号（signal/regenerate、
-- inpaint、prompt_edit，explicit=0）。direction 来自产生候选的工具调用
-- prompt；alternatives_json 保存同一结果卡内未选候选的句柄（未选不是否决）。
-- active=0 是软删除：列表与系统提示都不再包含，历史事实仍保留。
-- user_edited=1 的记录不会被上限自动淘汰。

CREATE TABLE chat_session_preferences (
    id                TEXT PRIMARY KEY,
    owner_id          TEXT NOT NULL,
    session_id        TEXT NOT NULL,
    kind              TEXT NOT NULL,
    source            TEXT NOT NULL,
    direction         TEXT NOT NULL DEFAULT '',
    reason_preset     TEXT NOT NULL DEFAULT '',
    reason_text       TEXT NOT NULL DEFAULT '',
    task_id           TEXT NOT NULL DEFAULT '',
    asset_id          TEXT NOT NULL DEFAULT '',
    handle            TEXT NOT NULL DEFAULT '',
    alternatives_json TEXT NOT NULL DEFAULT '[]',
    explicit          INTEGER NOT NULL DEFAULT 1,
    active            INTEGER NOT NULL DEFAULT 1,
    user_edited       INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX chat_session_preferences_session_active
    ON chat_session_preferences(owner_id, session_id, active, created_at DESC, id DESC);