-- 0063: 资产媒体类型数据修正的逐资产记录（#185，2026-10-22）。启动期修正
-- （internal/assetcorrection）在核对实际字节后才改写 generation_assets.media_type，
-- 并在同一事务写入本表：corrected 记录原标签（previous_media_type）以便回滚，
-- kept 记录字节不符而保持原样的资产。已有记录的资产不再处理，重复执行幂等。
-- 只建表，不改既有数据。
CREATE TABLE IF NOT EXISTS asset_media_type_corrections (
    correction          TEXT NOT NULL,
    asset_id            TEXT NOT NULL,
    task_id             TEXT NOT NULL,
    outcome             TEXT NOT NULL CHECK (outcome IN ('corrected', 'kept')),
    previous_media_type TEXT NOT NULL,
    media_type          TEXT NOT NULL,
    sha256              TEXT NOT NULL,
    detail              TEXT NOT NULL DEFAULT '',
    recorded_at         TEXT NOT NULL,
    PRIMARY KEY (correction, asset_id)
);
