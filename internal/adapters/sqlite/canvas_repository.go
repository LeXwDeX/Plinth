package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/canvas"
)

func (r *Repository) ListProjects(
	ctx context.Context,
	ownerID string,
) ([]canvas.ProjectSummary, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.name,
			COALESCE(p.session_id, ''),
			p.bg_style,
			p.revision,
			p.created_at,
			p.updated_at,
			COUNT(n.id)
		FROM go_canvas_projects AS p
		LEFT JOIN go_canvas_nodes AS n ON n.project_id = p.id
		WHERE p.owner_id = ?
		GROUP BY p.id
		ORDER BY p.updated_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("canvas list projects: %w", err)
	}
	defer rows.Close()
	projects := make([]canvas.ProjectSummary, 0)
	for rows.Next() {
		var (
			project   canvas.ProjectSummary
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(
			&project.ID,
			&project.Name,
			&project.SessionID,
			&project.BGStyle,
			&project.Revision,
			&createdAt,
			&updatedAt,
			&project.NodeCount,
		); err != nil {
			return nil, fmt.Errorf("canvas scan project summary: %w", err)
		}
		project.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("canvas parse created_at: %w", err)
		}
		project.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("canvas parse updated_at: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("canvas iterate projects: %w", err)
	}
	return projects, nil
}

func (r *Repository) CreateProject(
	ctx context.Context,
	project canvas.Project,
) (canvas.Project, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO go_canvas_projects (
			id,
			owner_id,
			name,
			session_id,
			owns_session,
			viewport,
			bg_style,
			coordinate_version,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		project.ID,
		project.OwnerID,
		project.Name,
		project.SessionID,
		project.OwnsSession,
		project.Viewport,
		project.BGStyle,
		nullableCoordinateVersion(project.CoordinateVersion),
		formatTime(project.CreatedAt),
		formatTime(project.UpdatedAt),
	)
	if err != nil {
		return canvas.Project{}, fmt.Errorf("canvas create project: %w", err)
	}
	if project.Nodes == nil {
		project.Nodes = []canvas.Node{}
	}
	project.Revision = 1
	return project, nil
}

func (r *Repository) GetProjectBySession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (canvas.Project, bool, error) {
	var projectID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM go_canvas_projects
		WHERE owner_id = ? AND session_id = ?`, ownerID, sessionID,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return canvas.Project{}, false, nil
	}
	if err != nil {
		return canvas.Project{}, false, fmt.Errorf("canvas find session project: %w", err)
	}
	return r.GetProject(ctx, ownerID, projectID)
}

func (r *Repository) GetProject(
	ctx context.Context,
	ownerID string,
	projectID string,
) (canvas.Project, bool, error) {
	var (
		project     canvas.Project
		sessionID   sql.NullString
		ownsSession bool
		placed      sql.NullString
		frameWidth  sql.NullInt64
		frameHeight sql.NullInt64
		frameCoord  sql.NullInt64
		createdAt   string
		updatedAt   string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT
			id,
			owner_id,
			name,
			session_id,
			owns_session,
			viewport,
			bg_style,
			revision,
			placed_results_json,
			canvas_width,
			canvas_height,
			coordinate_version,
			created_at,
			updated_at
		FROM go_canvas_projects
		WHERE id = ? AND owner_id = ?`,
		projectID,
		ownerID,
	).Scan(
		&project.ID,
		&project.OwnerID,
		&project.Name,
		&sessionID,
		&ownsSession,
		&project.Viewport,
		&project.BGStyle,
		&project.Revision,
		&placed,
		&frameWidth,
		&frameHeight,
		&frameCoord,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return canvas.Project{}, false, nil
	}
	if err != nil {
		return canvas.Project{}, false, fmt.Errorf("canvas get project: %w", err)
	}
	project.SessionID = sessionID.String
	project.OwnsSession = ownsSession
	project.PlacedResults, project.PlacedResultsKnown, err = decodeCanvasPlacedResults(placed)
	if err != nil {
		return canvas.Project{}, false, err
	}
	// 版本单独有值 = 新建的像素画布尚未定尺寸；三列同时有值才是已定尺寸的画布（#246）。
	if frameCoord.Valid {
		project.CoordinateVersion = int(frameCoord.Int64)
	}
	if frameWidth.Valid && frameHeight.Valid && frameCoord.Valid {
		project.Frame = &canvas.Frame{
			Width:             int(frameWidth.Int64),
			Height:            int(frameHeight.Int64),
			CoordinateVersion: int(frameCoord.Int64),
		}
	}
	project.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return canvas.Project{}, false, fmt.Errorf("canvas parse created_at: %w", err)
	}
	project.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return canvas.Project{}, false, fmt.Errorf("canvas parse updated_at: %w", err)
	}
	project.Nodes, err = r.listCanvasNodes(ctx, project.ID)
	if err != nil {
		return canvas.Project{}, false, err
	}
	return project, true, nil
}

func (r *Repository) UpdateProject(
	ctx context.Context,
	project canvas.Project,
) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_canvas_projects
		SET name = ?, viewport = ?, bg_style = ?, updated_at = ?
		WHERE id = ? AND owner_id = ?`,
		project.Name,
		project.Viewport,
		project.BGStyle,
		formatTime(project.UpdatedAt),
		project.ID,
		project.OwnerID,
	)
	if err != nil {
		return false, fmt.Errorf("canvas update project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("canvas update project rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *Repository) DeleteProjectAndSession(
	ctx context.Context,
	ownerID string,
	projectID string,
) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("canvas begin delete transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var sessionID sql.NullString
	var ownsSession bool
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, owns_session
		FROM go_canvas_projects
		WHERE id = ? AND owner_id = ?`,
		projectID,
		ownerID,
	).Scan(&sessionID, &ownsSession)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("canvas lookup project for delete: %w", err)
	}
	if ownsSession && sessionID.Valid && sessionID.String != "" {
		if _, err := deleteSessionAndDependents(ctx, tx, ownerID, sessionID.String); err != nil {
			return false, fmt.Errorf("canvas delete linked session: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM go_canvas_projects
		WHERE id = ? AND owner_id = ?`,
		projectID,
		ownerID,
	)
	if err != nil {
		return false, fmt.Errorf("canvas delete project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("canvas delete project rows affected: %w", err)
	}
	if affected == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("canvas commit delete transaction: %w", err)
	}
	return true, nil
}

func (r *Repository) ReplaceNodes(
	ctx context.Context,
	ownerID string,
	projectID string,
	expectedRevision int64,
	nodes []canvas.Node,
	placedResults *[]string,
	frame *canvas.Frame,
	maximumBitmapNodes int,
) (bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("canvas acquire replace nodes connection: %w", err)
	}
	defer conn.Close()
	// The write lock must precede the revision read so concurrent saves observe
	// the committed revision and the bitmap limit check shares the same transaction.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, fmt.Errorf("canvas begin replace nodes transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	// B15 乐观锁：expectedRevision > 0 时校验项目 revision，另一
	// 窗口已先保存则拒绝后写覆盖。
	var (
		revision      int64
		storedVersion sql.NullInt64
	)
	err = conn.QueryRowContext(ctx, `
		SELECT revision, coordinate_version
		FROM go_canvas_projects
		WHERE id = ? AND owner_id = ?`,
		projectID,
		ownerID,
	).Scan(&revision, &storedVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("canvas authorize node replacement: %w", err)
	}
	if expectedRevision > 0 && revision != expectedRevision {
		return false, canvas.ErrRevisionConflict
	}
	// #246：像素画布（已记录坐标版本，无论是否已定尺寸）只接受带尺寸的保存；不带尺寸的写入
	// 来自仍按旧 512 单位写节点的页面，会把旧坐标混进像素画布。只有旧画布（版本为 NULL）保留旧行为。
	if frame == nil && storedVersion.Valid {
		return false, canvas.ErrCanvasFrameRequired
	}
	bitmapCount := 0
	for _, node := range nodes {
		if node.Type == "image" {
			bitmapCount++
		}
	}
	if bitmapCount > maximumBitmapNodes {
		return false, canvas.ErrBitmapLimitExceeded
	}
	if _, err := conn.ExecContext(
		ctx,
		`DELETE FROM go_canvas_nodes WHERE project_id = ?`,
		projectID,
	); err != nil {
		return false, fmt.Errorf("canvas clear nodes: %w", err)
	}
	for _, node := range nodes {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO go_canvas_nodes (
				id,
				project_id,
				type,
				x,
				y,
				width,
				height,
				z_index,
				data_json,
				visible,
				locked,
				created_at,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			node.ID,
			projectID,
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
			return false, fmt.Errorf("canvas insert node %q: %w", node.ID, err)
		}
	}
	if frame != nil {
		// 画布像素尺寸与坐标契约版本同事务写入（#246），revision 只在下方递增一次。
		if _, err := conn.ExecContext(
			ctx,
			`UPDATE go_canvas_projects
			SET canvas_width = ?, canvas_height = ?, coordinate_version = ?
			WHERE id = ?`,
			frame.Width,
			frame.Height,
			frame.CoordinateVersion,
			projectID,
		); err != nil {
			return false, fmt.Errorf("canvas write frame: %w", err)
		}
	}
	if placedResults == nil {
		if _, err := conn.ExecContext(
			ctx,
			`UPDATE go_canvas_projects SET revision = revision + 1 WHERE id = ?`,
			projectID,
		); err != nil {
			return false, fmt.Errorf("canvas bump revision: %w", err)
		}
	} else {
		// 已放置结果清单与节点同事务、同一次 revision 递增写入（#239/#262）。
		keys := *placedResults
		if keys == nil {
			keys = []string{}
		}
		encoded, err := json.Marshal(keys)
		if err != nil {
			return false, fmt.Errorf("canvas encode placed results: %w", err)
		}
		if _, err := conn.ExecContext(
			ctx,
			`UPDATE go_canvas_projects
			SET revision = revision + 1, placed_results_json = ?
			WHERE id = ?`,
			string(encoded),
			projectID,
		); err != nil {
			return false, fmt.Errorf("canvas bump revision with placed results: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return false, fmt.Errorf("canvas commit replace nodes transaction: %w", err)
	}
	committed = true
	return true, nil
}

// nullableCoordinateVersion 把 0（旧画布或迁移导入）存为 NULL。
func nullableCoordinateVersion(version int) any {
	if version <= 0 {
		return nil
	}
	return version
}

// decodeCanvasPlacedResults 区分 NULL（旧画板未初始化）与 JSON 数组（已初始化，可为空）。
func decodeCanvasPlacedResults(column sql.NullString) ([]string, bool, error) {
	if !column.Valid {
		return nil, false, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(column.String), &keys); err != nil {
		return nil, false, fmt.Errorf("canvas decode placed results: %w", err)
	}
	if keys == nil {
		return nil, false, nil
	}
	return keys, true, nil
}

func (r *Repository) listCanvasNodes(
	ctx context.Context,
	projectID string,
) ([]canvas.Node, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			project_id,
			type,
			x,
			y,
			width,
			height,
			z_index,
			data_json,
			visible,
			locked,
			created_at,
			updated_at
		FROM go_canvas_nodes
		WHERE project_id = ?
		ORDER BY z_index ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("canvas list nodes: %w", err)
	}
	defer rows.Close()
	nodes := make([]canvas.Node, 0)
	for rows.Next() {
		var (
			node      canvas.Node
			visible   int
			locked    int
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(
			&node.ID,
			&node.ProjectID,
			&node.Type,
			&node.X,
			&node.Y,
			&node.Width,
			&node.Height,
			&node.ZIndex,
			&node.Data,
			&visible,
			&locked,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("canvas scan node: %w", err)
		}
		node.Visible = visible != 0
		node.Locked = locked != 0
		node.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("canvas parse node created_at: %w", err)
		}
		node.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("canvas parse node updated_at: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("canvas iterate nodes: %w", err)
	}
	return nodes, nil
}
