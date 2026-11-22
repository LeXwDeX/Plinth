-- 0111: 平台自己发起的处理计划（#461）。processing_plans.origin 的 CHECK 只允许
-- agent 与 browser，SQLite 不能就地放宽 CHECK，所以像 0097 那样重建表：origin
-- 增加 delivery（平台在自动补齐格式时自己起的那一步，#457 D1）。其余每一列的
-- 类型、默认值与约束、它对会话的外键、以及两个索引都保持原样，既有行按列原值
-- 复制；没有别的表引用 processing_plans，因此重建不需要关闭外键。
--
-- origin 决定三件事：交付计划的步骤以平台的名义要授权（审计读起来是平台而不是
-- 某个浏览器）、它的授权不占会话预算（#457 D7），以及平台自己的在途计划数
-- （auto_completion.max_inflight_plans）只数它。旧库里没有交付计划，默认值
-- 'browser' 让旧行为不变。
CREATE TABLE processing_plans_delivery (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    source_task_id TEXT NOT NULL,
    source_asset_id TEXT NOT NULL,
    state TEXT NOT NULL,
    approved_credits INTEGER NOT NULL,
    request_hash TEXT NOT NULL,
    steps_json TEXT NOT NULL,
    failure TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version INTEGER NOT NULL,
    harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0),
    harness_digest TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT 'browser'
    CHECK (origin IN ('agent', 'browser', 'delivery')),
    FOREIGN KEY (session_id) REFERENCES go_chat_sessions(id) ON DELETE CASCADE
);

INSERT INTO processing_plans_delivery (
    id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits,
    request_hash, steps_json, failure, created_at, updated_at, version, harness_revision,
    harness_digest, origin
)
SELECT
    id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits,
    request_hash, steps_json, failure, created_at, updated_at, version, harness_revision,
    harness_digest, origin
FROM processing_plans;

DROP TABLE processing_plans;

ALTER TABLE processing_plans_delivery RENAME TO processing_plans;

CREATE INDEX processing_plans_owner_session ON processing_plans(owner_id, session_id, created_at DESC);
CREATE INDEX processing_plans_active ON processing_plans(state, updated_at) WHERE state IN ('PENDING', 'RUNNING');