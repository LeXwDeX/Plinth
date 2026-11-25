-- 0115: 用户的项目组 KEY（#599）。
--
-- project_group_key 是管理员给用户设置的「项目组」标签，可空：NULL 表示未分组，
-- 不存空串。每个用户至多属于一个项目组，KEY 本身即标识，没有项目组表；KEY 的
-- 格式由 Product Schema administration.users.limits.project_group_key_pattern
-- 声明，由管理服务校验。它不是权限，不进入 roles_json。软删除用户时保留该值。
-- 旧用户从 NULL 开始，即未分组。

ALTER TABLE authentication_users ADD COLUMN project_group_key TEXT;
