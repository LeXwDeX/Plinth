-- 0067: 参考图张数（#329 E）。Agent 图生 3D 等步骤所选模型收的参考图比本轮给的少时
-- （如 Meshy 7.1 只收 1 张），步骤创建时在这里记下实际用了几张、本轮共给了几张。
-- 等待批准的工具结果、批准卡和批准后的结果消息据此用工具的 reference_limit_note
-- 说明只用了前几张。两列都是 0 = 没有图被略去（含旧数据，旧步骤不补说明）。
ALTER TABLE agent_generation_operations ADD COLUMN reference_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_generation_operations ADD COLUMN reference_total INTEGER NOT NULL DEFAULT 0;
