-- 0057: 画布像素尺寸与坐标契约版本（#246）。与节点同事务、同 revision 写入；
-- 三列同为 NULL = 旧画布尚未迁移到像素坐标（由前端打开时一次性迁移后保存）。
ALTER TABLE go_canvas_projects ADD COLUMN canvas_width INTEGER;
ALTER TABLE go_canvas_projects ADD COLUMN canvas_height INTEGER;
ALTER TABLE go_canvas_projects ADD COLUMN coordinate_version INTEGER;
