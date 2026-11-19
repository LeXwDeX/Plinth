-- 0103: 裁图来源记录（#331）。一行对应一张裁成 list_crop 的输入资产，
-- 记录它来自哪张源图、哪个归一化区域和哪个像素矩形。
--
-- 源图不建外键：源图删除后 provenance 与裁图保留，读取时容忍源缺失。
-- source_asset_id 是调用方引用的源资产 ID（普通输入资产，或 Task 产物）；
-- source_kind 是源形态快照（input_asset / task_asset）；Task 产物先复制成
-- 本人输入资产再裁剪，source_input_asset_id 记这次裁剪实际读取的输入资产，
-- 保证同一源图跨调用去重时身份稳定。
--
-- 唯一索引让同一源图的同一像素矩形只对应一张裁图；插入冲突时回读已有行，
-- 并发调用不会产生重复资产。region_json 只是归一化区域的展示快照，去重与
-- 几何判断以像素列为准。
--
-- 只追加、无数据回填；旧二进制不读新表，回滚无需 down 迁移。

CREATE TABLE input_asset_crops (
    crop_asset_id         TEXT PRIMARY KEY
                          REFERENCES generation_input_assets(id) ON DELETE CASCADE,
    owner_id              TEXT NOT NULL,
    source_asset_id       TEXT NOT NULL,
    source_kind           TEXT NOT NULL CHECK (source_kind IN ('input_asset', 'task_asset')),
    source_input_asset_id TEXT NOT NULL,
    source_width          INTEGER NOT NULL,
    source_height         INTEGER NOT NULL,
    region_json           TEXT NOT NULL,
    x0                    INTEGER NOT NULL,
    y0                    INTEGER NOT NULL,
    x1                    INTEGER NOT NULL,
    y1                    INTEGER NOT NULL,
    width                 INTEGER NOT NULL,
    height                INTEGER NOT NULL,
    sha256                TEXT NOT NULL,
    created_at            TEXT NOT NULL
);

CREATE UNIQUE INDEX input_asset_crops_source_rect
    ON input_asset_crops(owner_id, source_asset_id, x0, y0, x1, y1);
CREATE INDEX input_asset_crops_owner_source_created
    ON input_asset_crops(owner_id, source_asset_id, created_at);
CREATE INDEX input_asset_crops_owner_created
    ON input_asset_crops(owner_id, created_at DESC);