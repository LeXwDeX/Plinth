-- 0101: 清单长程推进的运行、项执行列与在场（#328 PR2）。
--
-- 一份清单至多一行 run：run 只记录调度状态与暂停原因，不复制清单内容；
-- list_id 唯一把这条规则落到库里。asset_list_items 追加执行列，全部带默认
-- 值，旧行可直接读取：last_turn_message_id 用于重启后对账最近一次服务端开
-- 轮，next_action_at 是退避时刻（空串 = 立即可推进，固定宽度 UTC 编码保证
-- TEXT 排序即时间序），block 是项状态之外的第二轴（待批准/步数上限；
-- paused 预留给后续）。asset_list_presence 是每个标签页的心跳，过期行由
-- runner 清理，不保留历史。
--
-- 只追加、无数据回填；旧二进制不读新表/新列，回滚无需 down 迁移。

CREATE TABLE asset_list_runs (
    id           TEXT PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    list_id      TEXT NOT NULL UNIQUE REFERENCES asset_lists(id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('idle', 'running', 'paused', 'stopped', 'completed')),
    pause_reason TEXT NOT NULL DEFAULT '' CHECK (pause_reason IN ('', 'user', 'presence', 'step_cap', 'failure')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX asset_list_runs_owner_status ON asset_list_runs(owner_id, status);
CREATE INDEX asset_list_runs_project_status ON asset_list_runs(project_id, status);

ALTER TABLE asset_list_items ADD COLUMN last_turn_message_id TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_list_items ADD COLUMN next_action_at TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_list_items ADD COLUMN block TEXT NOT NULL DEFAULT ''
    CHECK (block IN ('', 'awaiting_approval', 'paused', 'step_cap'));

-- 幂等创建/复用项子会话时按 session_id 反查项。
CREATE INDEX asset_list_items_session ON asset_list_items(session_id)
    WHERE session_id IS NOT NULL;

CREATE TABLE asset_list_presence (
    owner_id     TEXT NOT NULL,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tab_id       TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (owner_id, project_id, tab_id)
);

CREATE INDEX asset_list_presence_project_seen ON asset_list_presence(owner_id, project_id, last_seen_at);