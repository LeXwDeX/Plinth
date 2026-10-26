-- 逐请求级审计：管理读路径此前完全不写审计，管理员「读了什么」不可追溯。
-- 补齐按操作人检索的索引：安全复核与离职交接都要问"这个人做过什么"，
-- 缺该索引时只能全表扫描，审计量一大就不可用。
CREATE INDEX IF NOT EXISTS administration_audit_events_actor
    ON administration_audit_events(actor_id, created_at DESC);

-- 资源维度：追某条用户/任务被哪些管理员动过。
CREATE INDEX IF NOT EXISTS administration_audit_events_resource
    ON administration_audit_events(resource_type, resource_id, created_at DESC);
