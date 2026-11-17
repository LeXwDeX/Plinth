-- 0097: 授权来源「个人访问 Key」（#332）。execution_grants.source 的 CHECK 只允许
-- human_browser 与 agent_human_decision，SQLite 不能就地改 CHECK，所以重建表：
-- source 增加 access_key，并新增 access_key_id（来源为 access_key 的授权记录是哪把 Key
-- 发起的，其余来源为空串）。其余每一列的类型、默认值与约束，表级 CHECK、UNIQUE，
-- 以及两个索引都保持原样；既有行按列原值复制，只有新列取默认值 ''。
--
-- 没有任何表以外键引用 execution_grants，execution_grants 自己也没有外键，也没有触发器或视图
-- 依赖它，因此重建不需要关闭 foreign_keys。表序按原来的物理列序，新列在最后。
CREATE TABLE execution_grants_access_key (
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
    source TEXT NOT NULL CHECK (source IN ('human_browser','agent_human_decision','access_key')),
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

INSERT INTO execution_grants_access_key (
    request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
    prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source,
    request_version, decided_by, decided_at, created_at, updated_at, trigger_turn_id,
    harness_revision, harness_digest, budget_eligible
)
SELECT
    request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
    prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source,
    request_version, decided_by, decided_at, created_at, updated_at, trigger_turn_id,
    harness_revision, harness_digest, budget_eligible
FROM execution_grants;

DROP TABLE execution_grants;

ALTER TABLE execution_grants_access_key RENAME TO execution_grants;

CREATE INDEX execution_grants_session_status ON execution_grants(owner_id, session_id, status);
CREATE INDEX execution_grants_trigger_turn ON execution_grants(trigger_turn_id)
    WHERE trigger_turn_id <> '';
