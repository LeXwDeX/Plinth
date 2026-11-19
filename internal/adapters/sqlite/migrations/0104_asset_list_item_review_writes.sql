-- 0104: 清单项验收的通过/打回写入（#329 329b）。
--
-- 进入待验收时由服务端生成 review_json 快照（0100 已有该列）；0102 的
-- 决定表只读，本迁移补齐写入所需的列：
--
-- - asset_list_items.accepted_at：通过时刻，空串表示尚未通过。
-- - asset_list_items.return_reason / return_note：最近一次打回的原因与说明。
--   同一个子会话的续轮靠它们把打回原因写进下一轮的用户消息；历史版本仍以
--   asset_list_item_reviews 的逐行记录为准。
-- - asset_list_item_reviews.reasons_json / note：打回时选中的原因 id 与说明。
--   reason 列继续保存给人看的合并文案，保持旧读取路径不变。
--
-- 只追加、无数据回填；旧二进制不读新列，回滚无需 down 迁移。

ALTER TABLE asset_list_items ADD COLUMN accepted_at TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_list_items ADD COLUMN return_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_list_items ADD COLUMN return_note TEXT NOT NULL DEFAULT '';

ALTER TABLE asset_list_item_reviews ADD COLUMN reasons_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE asset_list_item_reviews ADD COLUMN note TEXT NOT NULL DEFAULT '';