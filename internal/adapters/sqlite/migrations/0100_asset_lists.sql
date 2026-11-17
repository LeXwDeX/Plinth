-- 0100: 项目制作清单（#327 PR2）。
--
-- 一份清单是项目下的一组待做资产：Agent 提案一个 draft，用户确认后成为项目
-- 唯一的 confirmed；再次提案时旧的 draft 被 superseded。清单只保存引用与
-- 计划，不报价、不扣费、不建任务；项目资产要等验收通过后才登记（#329）。
--
-- item 的 route_json 是提案时解析的可用性快照（capability/offering_id/
-- available/reason），不可用时也保存并标出，不静默换模型。步数是计划值：
-- planned_model3d_step_count 与 step_policy.counted_capabilities 同口径，
-- 实际计步仍由 #328 的执行授权负责。

CREATE TABLE asset_lists (
    id                 TEXT PRIMARY KEY,
    request_key        TEXT NOT NULL UNIQUE,
    owner_id           TEXT NOT NULL,
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_session_id  TEXT REFERENCES go_chat_sessions(id) ON DELETE SET NULL,
    origin_message_id  TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    status             TEXT NOT NULL,
    version            INTEGER NOT NULL DEFAULT 0,
    brief_json         TEXT NOT NULL DEFAULT '{}',
    decided_at         TEXT,
    decided_by         TEXT NOT NULL DEFAULT '',
    decision_reason    TEXT NOT NULL DEFAULT '',
    superseded_by      TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    expires_at         TEXT NOT NULL
);

CREATE INDEX asset_lists_project_status ON asset_lists(project_id, status);
CREATE INDEX asset_lists_owner_updated ON asset_lists(owner_id, updated_at DESC);

-- 每个项目至多一份 confirmed：确认第二份时必须显式替换（旧 confirmed 先
-- superseded，再确认新的），部分唯一索引把这条产品规则落到库里。
CREATE UNIQUE INDEX asset_lists_confirmed_project ON asset_lists(project_id)
    WHERE status = 'confirmed';

CREATE TABLE asset_list_items (
    id                         TEXT PRIMARY KEY,
    owner_id                   TEXT NOT NULL,
    list_id                    TEXT NOT NULL REFERENCES asset_lists(id) ON DELETE CASCADE,
    position                   INTEGER NOT NULL,
    kind                       TEXT NOT NULL,
    name                       TEXT NOT NULL,
    description                TEXT NOT NULL DEFAULT '',
    references_json            TEXT NOT NULL DEFAULT '[]',
    route_id                   TEXT NOT NULL,
    route_json                 TEXT NOT NULL DEFAULT '[]',
    planned_step_count         INTEGER NOT NULL DEFAULT 0,
    planned_model3d_step_count INTEGER NOT NULL DEFAULT 0,
    state                      TEXT NOT NULL,
    state_reason               TEXT NOT NULL DEFAULT '',
    version                    INTEGER NOT NULL DEFAULT 0,
    session_id                 TEXT REFERENCES go_chat_sessions(id) ON DELETE SET NULL,
    project_asset_id           TEXT NOT NULL DEFAULT '',
    round                      INTEGER NOT NULL DEFAULT 0,
    result_json                TEXT NOT NULL DEFAULT '{}',
    review_json                TEXT NOT NULL DEFAULT '{}',
    created_at                 TEXT NOT NULL,
    updated_at                 TEXT NOT NULL
);

CREATE UNIQUE INDEX asset_list_items_list_position ON asset_list_items(list_id, position);
CREATE INDEX asset_list_items_list_state ON asset_list_items(list_id, state);
CREATE INDEX asset_list_items_owner_state ON asset_list_items(owner_id, state);