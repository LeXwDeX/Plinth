-- An Agent approval that outlives its turn. detached_at is set when the turn
-- stopped waiting and left the grant pending; the grant then stays approvable
-- until its Schema expiry. policy_evaluated_at records that the session
-- approval policy already looked at the grant once, so a later mode switch can
-- never approve a grant that was left waiting. settled_at is set once the
-- decision's outcome (the denial, or the finished task) was posted into the
-- session.
ALTER TABLE agent_generation_operations ADD COLUMN detached_at TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_generation_operations ADD COLUMN policy_evaluated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_generation_operations ADD COLUMN settled_at TEXT NOT NULL DEFAULT '';

CREATE INDEX agent_generation_operations_unsettled ON agent_generation_operations(settled_at)
WHERE detached_at <> '' AND settled_at = '';
