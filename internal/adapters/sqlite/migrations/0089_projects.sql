-- 0089: 跨会话项目资产（#240）。
--
-- 一个项目属于一个 owner，持有可复用的风格指南、角色/道具引用与生产约定。
-- 项目只保存引用和元数据：图片与模型字节仍由 generation/input assets 拥有，
-- 删除项目资产只删 project_assets 行，不会触碰已完成任务的产物。

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX projects_owner_updated
    ON projects(owner_id, updated_at DESC, id DESC);

CREATE TABLE project_style_guides (
    project_id       TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    enabled          INTEGER NOT NULL DEFAULT 0,
    text             TEXT NOT NULL DEFAULT '',
    palette          TEXT NOT NULL DEFAULT '[]',
    references_json  TEXT NOT NULL DEFAULT '[]',
    updated_at       TEXT NOT NULL
);

CREATE TABLE project_conventions (
    project_id  TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    brief_json  TEXT NOT NULL DEFAULT '{}',
    texts_json  TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL
);

CREATE TABLE project_assets (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    owner_id      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    images_json   TEXT NOT NULL DEFAULT '[]',
    models_json   TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX project_assets_project_updated
    ON project_assets(project_id, updated_at DESC, id DESC);

CREATE INDEX project_assets_owner_updated
    ON project_assets(owner_id, updated_at DESC, id DESC);