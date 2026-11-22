-- 0110: 平台自己发起的授权（#461）。execution_grants.source 的 CHECK 只允许
-- human_browser、agent_human_decision 与 access_key，SQLite 不能就地放宽 CHECK，
-- 所以像 0097 那样重建表：source 增加 delivery（平台在自动补齐格式时问自己
-- 要的那一步，#457 D1）；其余每一列的类型、默认值与约束、两个 UNIQUE、以及两个
-- 索引都保持原样，既有行按列原值复制。交付授权不花会话预算（#457 D7）由
-- executiongrant.Service 自己拒绝，不在库里重复约束。
--
-- 没有表以外键引用 execution_grants，execution_grants 自己也没有外键、触发器或
-- 视图依赖它，因此重建不需要关闭外键。表序按原来的物理列序，CHECK 与索引与原
-- 表一致（0097 之后的两个索引都重建）。
CREATE TABLE execution_grants_delivery (
    request_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    offering_id TEXT NOT NULL,
    offering_revision TEXT NOT NULL,
    prepared_input_id TEXT NOT NULL,
    prepared_input_hash TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    plan_step_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('pending','approved','claimed','bound','denied')),
    task_id TEXT UNIQUE,
    source TEXT NOT NULL CHECK (source IN ('human_browser','agent_human_decision','access_key','delivery')),
    request_version INTEGER NOT NULL CHECK (request_version > 0),
    decided_by TEXT,
    decided_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    trigger_turn_id TEXT NOT NULL DEFAULT '',
    harness_revision INTEGER NOT NULL DEFAULT 0 CHECK (harness_revision >= 0),
    harness_digest TEXT NOT NULL DEFAULT '',
    budget_eligible INTEGER NOT NULL DEFAULT 0 CHECK (budget_eligible IN (0, 1)),
    access_key_id TEXT NOT NULL DEFAULT '' CHECK ((source = 'access_key') = (access_key_id <> '')),
    CHECK ((status = 'bound' AND task_id IS NOT NULL) OR (status <> 'bound' AND task_id IS NULL)),
    UNIQUE (owner_id, idempotency_key),
    UNIQUE (owner_id, session_id, operation_id, plan_step_id)
);

INSERT INTO execution_grants_delivery (
    request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
    prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source,
    request_version, decided_by, decided_at, created_at, updated_at, trigger_turn_id,
    harness_revision, harness_digest, budget_eligible, access_key_id
)
SELECT
    request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
    prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source,
    request_version, decided_by, decided_at, created_at, updated_at, trigger_turn_id,
    harness_revision, harness_digest, budget_eligible, access_key_id
FROM execution_grants;

DROP TABLE execution_grants;

ALTER TABLE execution_grants_delivery RENAME TO execution_grants;

CREATE INDEX execution_grants_session_status ON execution_grants(owner_id, session_id, status);
CREATE INDEX execution_grants_trigger_turn ON execution_grants(trigger_turn_id)
    WHERE trigger_turn_id <> '';