-- 0102: 清单项的验收决定记录（#329 329a）。
--
-- 每一版结果的决定追加一行，不覆盖历史：通过（approved）定版，打回
-- （returned）后该项轮次加一并重做。review_json 是决定时刻的验收快照，
-- 由 #329b 的通过/打回写入；329a 只读聚合，不写入本表。
--
-- reviewer_id 沿用清单决定的身份模型：真实浏览器会话的 principal id，当前
-- 即 owner_id（项目尚无成员制）。created_at 用固定宽度 UTC 编码
-- （formatTaskTime），保证 (list_id, item_id, created_at) 索引上的 TEXT
-- 排序就是时间序。
--
-- 只追加、无数据回填；旧二进制不读新表，回滚无需 down 迁移。

CREATE TABLE asset_list_item_reviews (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL,
    list_id     TEXT NOT NULL REFERENCES asset_lists(id) ON DELETE CASCADE,
    item_id     TEXT NOT NULL REFERENCES asset_list_items(id) ON DELETE CASCADE,
    round       INTEGER NOT NULL,
    verdict     TEXT NOT NULL CHECK (verdict IN ('approved', 'returned')),
    reason      TEXT NOT NULL DEFAULT '',
    reviewer_id TEXT NOT NULL DEFAULT '',
    review_json TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);

CREATE INDEX asset_list_item_reviews_item_created
    ON asset_list_item_reviews(list_id, item_id, created_at);
CREATE INDEX asset_list_item_reviews_owner_created
    ON asset_list_item_reviews(owner_id, created_at);