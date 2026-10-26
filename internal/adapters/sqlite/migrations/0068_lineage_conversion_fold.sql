-- 0068: 格式转换并入来源版本的起点（#330 第二轮，!341 审查）。
-- 这条迁移第一次执行的时刻（即升级到折叠逻辑的部署时刻）记为起点：此后新建的
-- 格式转换 Task 并入它转换的版本，不占版本号；此前已有的转换保留原来的节点和
-- 版本号（对话历史、工具结果与 Agent 回复里已经按号写过「3D vN」）。新库的起点
-- 就是建库时刻。只有一行，不再改写。
CREATE TABLE IF NOT EXISTS lineage_conversion_fold (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    since TEXT NOT NULL
);
INSERT OR IGNORE INTO lineage_conversion_fold (id, since) VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
