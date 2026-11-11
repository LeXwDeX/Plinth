-- 0090: 会话归属项目与风格来源（#240）。
--
-- project_id 为空表示会话不属任何项目。style_lock_source 是三态风格来源
-- （project/session/off）：新会话默认继承项目；升级前已经启用的会话锁
-- 必须映射为 session，否则升级后用户的锁会被默认值覆盖。
ALTER TABLE go_chat_sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN style_lock_source TEXT NOT NULL DEFAULT 'project';

UPDATE go_chat_sessions
SET style_lock_source = 'session'
WHERE style_lock_enabled = 1
   OR trim(style_lock_text) <> ''
   OR style_lock_references <> '[]';