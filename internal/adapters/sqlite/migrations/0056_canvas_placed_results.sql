-- 0056: 画板已放置结果记录（#239/#262）。与节点同事务、同 revision 写入；
-- NULL = 旧画板尚未初始化（由前端首次播种），JSON 数组 = 已初始化（可为空）。
ALTER TABLE go_canvas_projects ADD COLUMN placed_results_json TEXT;
