-- 0070: 图生 3D 来源事实（#80）。步骤准备时在这里冻结参考图的实际来源：
-- 原始选择数、三视图替换与去重后的候选数、最终冻结数、最终正面所在的参数
-- 与位置、正面 Task/Asset 成对身份，以及不保存 URL 的来源身份指纹。
-- 空值 = 历史未知（旧步骤不补数据、不按当前三视图或相同 SHA 猜来源）。
ALTER TABLE agent_generation_operations ADD COLUMN reference_source TEXT NOT NULL DEFAULT '';
