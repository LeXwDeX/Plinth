-- 0058: 参考图压缩派生（#276）。为满足 Offering 上限而重新编码的图片另存为
-- 新的输入资产，记录来源资产、压缩方式与压缩前大小；原图不变。三列同为
-- NULL = 普通上传或旧数据。
ALTER TABLE generation_input_assets ADD COLUMN derived_from_asset_id TEXT;
ALTER TABLE generation_input_assets ADD COLUMN derivation_method TEXT;
ALTER TABLE generation_input_assets ADD COLUMN source_size_bytes INTEGER;
