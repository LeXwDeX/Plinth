-- 0088: 付费加工步骤的修正归属（#246）。操作冻结时记录它修正的结果任务，
-- 这样回合结束后才获批的步骤仍能精确地把这次修正计入该结果的自检记录。
-- 历史操作没有该字段，默认空字符串表示不修正任何结果。
ALTER TABLE agent_generation_operations ADD COLUMN correction_task_id TEXT NOT NULL DEFAULT '';