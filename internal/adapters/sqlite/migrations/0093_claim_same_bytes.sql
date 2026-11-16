-- 0093: 声明字节相同的产物（#300）。Provider 可以在完成观察里说明“这个产物与同一
-- 观察里的另一个产物是同一个文件”（Tripo 的 GLTF 导出既是 converted.gltf 槽位，也是
-- 主 model 槽位），物化时按它只下载一次。该列保存被指向的产物 ID，空串表示没有
-- 声明；历史行为空串，零行为变化。资产恢复的来源摘要只在它非空时才计入这一列。
ALTER TABLE generation_artifact_claims ADD COLUMN same_bytes_as TEXT NOT NULL DEFAULT '';
