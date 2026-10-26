package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/canvas"
)

type CanvasImportStats struct {
	Projects ImportStats
	Nodes    ImportStats
}

// ImportCanvasHistory 在一个事务中按旧稳定 ID 幂等导入项目与节点。
func (r *Repository) ImportCanvasHistory(
	ctx context.Context,
	projects []canvas.Project,
	nodes []canvas.Node,
) (CanvasImportStats, error) {
	var result CanvasImportStats
	projectStats, err := r.importETLBatches(
		ctx,
		"canvas_project",
		len(projects),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, project := range projects[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"canvas_project",
					project.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				var sessionID any
				if strings.TrimSpace(project.SessionID) != "" {
					sessionID = project.SessionID
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO go_canvas_projects (
				id, owner_id, name, session_id, viewport, bg_style,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				owner_id = excluded.owner_id,
				name = excluded.name,
				session_id = excluded.session_id,
				viewport = excluded.viewport,
				bg_style = excluded.bg_style,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at`,
					project.ID,
					project.OwnerID,
					project.Name,
					sessionID,
					project.Viewport,
					project.BGStyle,
					formatTime(project.CreatedAt),
					formatTime(project.UpdatedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入 Canvas 项目 %s: %w",
						project.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Projects = projectStats
	nodeStats, err := r.importETLBatches(
		ctx,
		"canvas_node",
		len(nodes),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, node := range nodes[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"canvas_node",
					node.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO go_canvas_nodes (
				id, project_id, type, x, y, width, height, z_index,
				data_json, visible, locked, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(project_id, id) DO UPDATE SET
				type = excluded.type,
				x = excluded.x,
				y = excluded.y,
				width = excluded.width,
				height = excluded.height,
				z_index = excluded.z_index,
				data_json = excluded.data_json,
				visible = excluded.visible,
				locked = excluded.locked,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at`,
					node.ID,
					node.ProjectID,
					node.Type,
					node.X,
					node.Y,
					node.Width,
					node.Height,
					node.ZIndex,
					node.Data,
					boolInt(node.Visible),
					boolInt(node.Locked),
					formatTime(node.CreatedAt),
					formatTime(node.UpdatedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入 Canvas 节点 %s: %w",
						node.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Nodes = nodeStats
	return result, nil
}
