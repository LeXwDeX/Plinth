-- 0086: 会话级视觉锚（#250）。
--
-- visual_anchors 保存会话的锚点列表（JSON 数组）：每个锚点有 id、name、
-- kind（identity|style）、description、references（owner/session 校验过的
-- input asset id）与 origin（user|agent）。风格锁仍是独立的列：Agent 写入
-- 的视觉锚不会静默改写用户设置的风格锁。旧会话从空列表开始。

ALTER TABLE go_chat_sessions ADD COLUMN visual_anchors TEXT NOT NULL DEFAULT '[]';
