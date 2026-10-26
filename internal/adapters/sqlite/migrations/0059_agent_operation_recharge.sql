-- 0059: 重复计费防护（#294）。Agent 生成步骤创建时，本会话中同类的上一次
-- 尝试若已在上游完成并计费、结果却没有入库且此后没有处理，就在这里记下那个
-- Task；记下后该步骤不论批准方式都等用户在批准卡上确认，批准卡写明会再次
-- 计费。空串 = 没有这种前序失败（含旧数据）。
ALTER TABLE agent_generation_operations ADD COLUMN recharge_task_id TEXT NOT NULL DEFAULT '';
