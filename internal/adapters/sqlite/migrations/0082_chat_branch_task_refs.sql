-- 0082: 分支任务只读来源（#186）。
--
-- 分支只获得祖先会话 Task 的读取授权，用来做 source_task / lineage /
-- 舞台恢复；它不复制 Task、Grant、预算或审批，也不改变任何授权判定。
-- (branch_session_id, task_id) 唯一，owner_id 与 task_id 的索引用于
-- 作用域查询与删除后的审计。

CREATE TABLE chat_branch_task_refs (
    branch_session_id TEXT NOT NULL REFERENCES go_chat_sessions(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL,
    task_id TEXT NOT NULL REFERENCES generation_tasks(id) ON DELETE CASCADE,
    source_session_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (branch_session_id, task_id)
);
CREATE INDEX chat_branch_task_refs_owner_task ON chat_branch_task_refs(owner_id, task_id);
CREATE INDEX chat_branch_task_refs_branch ON chat_branch_task_refs(owner_id, branch_session_id);

-- Backfill existing branches: every Task of every ancestor session becomes a
-- read-only ref of the branch. Recursive CTE follows parent_session_id and
-- UNION terminates a malformed cycle; owner_id is carried along and compared
-- on every hop so a cross-owner parent link grants nothing.
WITH RECURSIVE ancestry(branch_session_id, owner_id, ancestor_session_id) AS (
    SELECT id, owner_id, parent_session_id
    FROM go_chat_sessions
    WHERE parent_session_id <> ''
    UNION
    SELECT ancestry.branch_session_id, ancestry.owner_id, parent.parent_session_id
    FROM ancestry
    JOIN go_chat_sessions AS parent
      ON parent.id = ancestry.ancestor_session_id
     AND parent.owner_id = ancestry.owner_id
    WHERE parent.parent_session_id <> ''
)
INSERT OR IGNORE INTO chat_branch_task_refs
    (branch_session_id, owner_id, task_id, source_session_id, created_at)
SELECT ancestry.branch_session_id, task.owner_id, task.id, task.session_id,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM ancestry
JOIN generation_tasks AS task
  ON task.session_id = ancestry.ancestor_session_id
 AND task.owner_id = ancestry.owner_id
WHERE ancestry.ancestor_session_id <> ancestry.branch_session_id;