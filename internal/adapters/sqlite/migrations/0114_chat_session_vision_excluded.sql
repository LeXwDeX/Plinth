-- 0114: 会话级「视觉排除」集合（#522）。
--
-- vision_excluded 保存会话里被上游模型拒绝读取的图片（JSON 字符串数组）：每项是
-- 该图片 data URI 的 SHA-256 十六进制，按加入先后排列，条数由 Product Schema
-- context.vision.invalid_picture.maximum_excluded 限制。其后每一轮、重启或续跑后，
-- 视觉计划都跳过这些图片，不再让同一张坏图拖垮每一次 Agent 请求。旧会话从空列表
-- 开始，等于没有排除。

ALTER TABLE go_chat_sessions ADD COLUMN vision_excluded TEXT NOT NULL DEFAULT '[]';
