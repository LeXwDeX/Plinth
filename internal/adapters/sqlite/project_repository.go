package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/project"
)

// ProjectRepository is the owner-scoped SQLite adapter for projects (#240).
// Every query joins or filters projects.owner_id, so a foreign owner observes
// ErrNotFound rather than another owner's project.
type ProjectRepository struct {
	db *sql.DB
}

func (r *Repository) ProjectRepository() *ProjectRepository {
	return &ProjectRepository{db: r.db}
}

// withProjectWrite reserves SQLite's writer lock before the limit/ownership
// reads, so concurrent creates cannot both pass a count check.
func (r *ProjectRepository) withProjectWrite(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *ProjectRepository) CreateProject(ctx context.Context, value project.Project, maximum int) (project.Project, error) {
	err := r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE owner_id = ?`, value.OwnerID).Scan(&count); err != nil {
			return fmt.Errorf("count projects: %w", err)
		}
		if count >= maximum {
			return project.ErrLimit
		}
		_, err := conn.ExecContext(ctx, `
			INSERT INTO projects (id, owner_id, name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			value.ID, value.OwnerID, value.Name, formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		return nil
	})
	if err != nil {
		return project.Project{}, err
	}
	return value, nil
}

func (r *ProjectRepository) ListProjects(ctx context.Context, ownerID string, limit int) ([]project.Project, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_id, name, created_at, updated_at
		FROM projects WHERE owner_id = ?
		ORDER BY updated_at DESC, id DESC LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	result := make([]project.Project, 0)
	for rows.Next() {
		value, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *ProjectRepository) GetProject(ctx context.Context, ownerID, projectID string) (project.Project, error) {
	value, err := scanProject(r.db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, created_at, updated_at
		FROM projects WHERE id = ? AND owner_id = ?`, projectID, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, project.ErrNotFound
	}
	if err != nil {
		return project.Project{}, err
	}
	return value, nil
}

func (r *ProjectRepository) RenameProject(ctx context.Context, ownerID, projectID, name string, at time.Time) (project.Project, error) {
	var result project.Project
	err := r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
			UPDATE projects SET name = ?, updated_at = ?
			WHERE id = ? AND owner_id = ?`, name, formatTime(at), projectID, ownerID)
		if err != nil {
			return fmt.Errorf("rename project: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return project.ErrNotFound
		}
		value, err := scanProject(conn.QueryRowContext(ctx, `
			SELECT id, owner_id, name, created_at, updated_at
			FROM projects WHERE id = ? AND owner_id = ?`, projectID, ownerID))
		if err != nil {
			return err
		}
		result = value
		return nil
	})
	if err != nil {
		return project.Project{}, err
	}
	return result, nil
}

// DeleteProject deletes the project, its cascaded guide/conventions/assets and
// unassigns its sessions. Sessions and their Task results are never deleted.
func (r *ProjectRepository) DeleteProject(ctx context.Context, ownerID, projectID string) error {
	return r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `DELETE FROM projects WHERE id = ? AND owner_id = ?`, projectID, ownerID)
		if err != nil {
			return fmt.Errorf("delete project: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return project.ErrNotFound
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE go_chat_sessions SET project_id = ''
			WHERE project_id = ? AND owner_id = ?`, projectID, ownerID); err != nil {
			return fmt.Errorf("unassign project sessions: %w", err)
		}
		return nil
	})
}

func (r *ProjectRepository) StyleGuide(ctx context.Context, ownerID, projectID string) (project.StyleGuide, error) {
	var guide project.StyleGuide
	var enabled int
	var text, palette, references string
	var updated sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT p.id, COALESCE(g.enabled, 0), COALESCE(g.text, ''),
		       COALESCE(g.palette, '[]'), COALESCE(g.references_json, '[]'), g.updated_at
		FROM projects p LEFT JOIN project_style_guides g ON g.project_id = p.id
		WHERE p.id = ? AND p.owner_id = ?`, projectID, ownerID).
		Scan(&guide.ProjectID, &enabled, &text, &palette, &references, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return project.StyleGuide{}, project.ErrNotFound
	}
	if err != nil {
		return project.StyleGuide{}, fmt.Errorf("read project style guide: %w", err)
	}
	if updated.Valid {
		guide.UpdatedAt, _ = parseTime(updated.String)
	}
	guide.Enabled = enabled != 0
	guide.Text = text
	guide.Palette = decodeProjectStrings(palette)
	guide.References = decodeProjectReferences(references)
	return guide, nil
}

func (r *ProjectRepository) SetStyleGuide(ctx context.Context, ownerID, projectID string, guide project.StyleGuide) (project.StyleGuide, error) {
	palette, err := encodeProjectStrings(guide.Palette)
	if err != nil {
		return project.StyleGuide{}, err
	}
	references, err := encodeProjectReferences(guide.References)
	if err != nil {
		return project.StyleGuide{}, err
	}
	var found bool
	err = r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
			INSERT INTO project_style_guides (project_id, enabled, text, palette, references_json, updated_at)
			SELECT id, ?, ?, ?, ?, ? FROM projects WHERE id = ? AND owner_id = ?
			ON CONFLICT(project_id) DO UPDATE SET
				enabled = excluded.enabled,
				text = excluded.text,
				palette = excluded.palette,
				references_json = excluded.references_json,
				updated_at = excluded.updated_at`,
			guide.Enabled, guide.Text, palette, references, formatTime(guide.UpdatedAt), projectID, ownerID)
		if err != nil {
			return fmt.Errorf("set project style guide: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return project.ErrNotFound
		}
		found = true
		return nil
	})
	if err != nil {
		return project.StyleGuide{}, err
	}
	if !found {
		return project.StyleGuide{}, project.ErrNotFound
	}
	guide.ProjectID = projectID
	return guide, nil
}

func (r *ProjectRepository) Conventions(ctx context.Context, ownerID, projectID string) (project.Conventions, error) {
	var conventions project.Conventions
	var brief, texts string
	var updated sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT p.id, COALESCE(c.brief_json, '{}'), COALESCE(c.texts_json, '{}'), c.updated_at
		FROM projects p LEFT JOIN project_conventions c ON c.project_id = p.id
		WHERE p.id = ? AND p.owner_id = ?`, projectID, ownerID).
		Scan(&conventions.ProjectID, &brief, &texts, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Conventions{}, project.ErrNotFound
	}
	if err != nil {
		return project.Conventions{}, fmt.Errorf("read project conventions: %w", err)
	}
	if updated.Valid {
		conventions.UpdatedAt, _ = parseTime(updated.String)
	}
	conventions.Brief = []byte(brief)
	conventions.Texts = decodeProjectTexts(texts)
	return conventions, nil
}

func (r *ProjectRepository) SetConventions(ctx context.Context, ownerID, projectID string, conventions project.Conventions) (project.Conventions, error) {
	brief := conventions.Brief
	if len(brief) == 0 {
		brief = []byte("{}")
	}
	if !json.Valid(brief) {
		return project.Conventions{}, project.ErrInvalid
	}
	texts, err := encodeProjectTexts(conventions.Texts)
	if err != nil {
		return project.Conventions{}, err
	}
	err = r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
			INSERT INTO project_conventions (project_id, brief_json, texts_json, updated_at)
			SELECT id, ?, ?, ? FROM projects WHERE id = ? AND owner_id = ?
			ON CONFLICT(project_id) DO UPDATE SET
				brief_json = excluded.brief_json,
				texts_json = excluded.texts_json,
				updated_at = excluded.updated_at`,
			string(brief), texts, formatTime(conventions.UpdatedAt), projectID, ownerID)
		if err != nil {
			return fmt.Errorf("set project conventions: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return project.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return project.Conventions{}, err
	}
	conventions.ProjectID = projectID
	conventions.Brief = append([]byte(nil), brief...)
	conventions.Texts = decodeProjectTexts(texts)
	return conventions, nil
}

func (r *ProjectRepository) ListAssets(ctx context.Context, ownerID, projectID string, limit int) ([]project.Asset, error) {
	var exists int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ? AND owner_id = ?`, projectID, ownerID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read project for assets: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.project_id, a.owner_id, a.kind, a.name, a.description,
		       a.images_json, a.models_json, a.created_at, a.updated_at
		FROM project_assets a
		WHERE a.project_id = ? AND a.owner_id = ?
		ORDER BY a.updated_at DESC, a.id DESC LIMIT ?`, projectID, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list project assets: %w", err)
	}
	defer rows.Close()
	result := make([]project.Asset, 0)
	for rows.Next() {
		value, err := scanProjectAsset(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *ProjectRepository) GetAsset(ctx context.Context, ownerID, projectID, assetID string) (project.Asset, error) {
	value, err := scanProjectAsset(r.db.QueryRowContext(ctx, `
		SELECT a.id, a.project_id, a.owner_id, a.kind, a.name, a.description,
		       a.images_json, a.models_json, a.created_at, a.updated_at
		FROM project_assets a JOIN projects p ON p.id = a.project_id
		WHERE a.id = ? AND a.project_id = ? AND p.owner_id = ?`, assetID, projectID, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return project.Asset{}, project.ErrNotFound
	}
	if err != nil {
		return project.Asset{}, err
	}
	return value, nil
}

func (r *ProjectRepository) CreateAsset(ctx context.Context, projectID string, value project.Asset, maximum int) (project.Asset, error) {
	images, err := encodeProjectReferences(value.Images)
	if err != nil {
		return project.Asset{}, err
	}
	models, err := encodeProjectReferences(value.Models)
	if err != nil {
		return project.Asset{}, err
	}
	err = r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		var exists int
		err := conn.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ? AND owner_id = ?`, projectID, value.OwnerID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return project.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read project for asset: %w", err)
		}
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_assets WHERE project_id = ?`, projectID).Scan(&count); err != nil {
			return fmt.Errorf("count project assets: %w", err)
		}
		if count >= maximum {
			return project.ErrLimit
		}
		_, err = conn.ExecContext(ctx, `
			INSERT INTO project_assets (id, project_id, owner_id, kind, name, description,
			                            images_json, models_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.ID, projectID, value.OwnerID, value.Kind, value.Name, value.Description,
			images, models, formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
		if err != nil {
			return fmt.Errorf("create project asset: %w", err)
		}
		return nil
	})
	if err != nil {
		return project.Asset{}, err
	}
	value.ProjectID = projectID
	return value, nil
}

func (r *ProjectRepository) UpdateAsset(ctx context.Context, ownerID, projectID, assetID, kind, name, description string, at time.Time) (project.Asset, error) {
	var result project.Asset
	err := r.withProjectWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
			UPDATE project_assets SET kind = ?, name = ?, description = ?, updated_at = ?
			WHERE id = ? AND project_id = ? AND owner_id = ?`,
			kind, name, description, formatTime(at), assetID, projectID, ownerID)
		if err != nil {
			return fmt.Errorf("update project asset: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return project.ErrNotFound
		}
		value, err := scanProjectAsset(conn.QueryRowContext(ctx, `
			SELECT a.id, a.project_id, a.owner_id, a.kind, a.name, a.description,
			       a.images_json, a.models_json, a.created_at, a.updated_at
			FROM project_assets a JOIN projects p ON p.id = a.project_id
			WHERE a.id = ? AND a.project_id = ? AND p.owner_id = ?`, assetID, projectID, ownerID))
		if err != nil {
			return err
		}
		result = value
		return nil
	})
	if err != nil {
		return project.Asset{}, err
	}
	return result, nil
}

func (r *ProjectRepository) DeleteAsset(ctx context.Context, ownerID, projectID, assetID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM project_assets WHERE id = ? AND project_id = ? AND owner_id = ?`,
		assetID, projectID, ownerID)
	if err != nil {
		return false, fmt.Errorf("delete project asset: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func scanProject(scanner interface{ Scan(...any) error }) (project.Project, error) {
	var value project.Project
	var created, updated string
	if err := scanner.Scan(&value.ID, &value.OwnerID, &value.Name, &created, &updated); err != nil {
		return project.Project{}, err
	}
	value.CreatedAt, _ = parseTime(created)
	value.UpdatedAt, _ = parseTime(updated)
	return value, nil
}

func scanProjectAsset(scanner interface{ Scan(...any) error }) (project.Asset, error) {
	var value project.Asset
	var images, models, created, updated string
	if err := scanner.Scan(&value.ID, &value.ProjectID, &value.OwnerID, &value.Kind, &value.Name,
		&value.Description, &images, &models, &created, &updated); err != nil {
		return project.Asset{}, err
	}
	value.Images = decodeProjectReferences(images)
	value.Models = decodeProjectReferences(models)
	value.CreatedAt, _ = parseTime(created)
	value.UpdatedAt, _ = parseTime(updated)
	return value, nil
}

func encodeProjectStrings(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode project strings: %w", err)
	}
	return string(encoded), nil
}

func decodeProjectStrings(raw string) []string {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return []string{}
	}
	return values
}

func encodeProjectReferences(values []project.Reference) (string, error) {
	if values == nil {
		values = []project.Reference{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode project references: %w", err)
	}
	return string(encoded), nil
}

func decodeProjectReferences(raw string) []project.Reference {
	var values []project.Reference
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return []project.Reference{}
	}
	return values
}

func encodeProjectTexts(values map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode project texts: %w", err)
	}
	return string(encoded), nil
}

func decodeProjectTexts(raw string) map[string]string {
	values := map[string]string{}
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return map[string]string{}
	}
	return values
}

// ProjectAssetSource verifies an owner's input or finished-Task asset before a
// project stores its reference (#240). It never copies bytes.
type ProjectAssetSource struct {
	db *sql.DB
}

func (r *Repository) ProjectAssetSource() project.AssetSource {
	return ProjectAssetSource{db: r.db}
}

func (source ProjectAssetSource) Picture(ctx context.Context, ownerID string, reference project.Reference) (project.Reference, error) {
	if !reference.Valid() {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	if reference.InputAssetID != "" {
		var exists int
		err := source.db.QueryRowContext(ctx, `
			SELECT 1 FROM generation_input_assets
			WHERE id = ? AND owner_id = ? AND media_type LIKE 'image/%'
			  AND storage_key <> '' AND sha256 <> ''`,
			reference.InputAssetID, ownerID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return project.Reference{}, project.ErrReferenceInvalid
		}
		if err != nil {
			return project.Reference{}, err
		}
		return project.Reference{InputAssetID: reference.InputAssetID}, nil
	}
	var mediaType string
	err := source.db.QueryRowContext(ctx, `
		SELECT a.media_type FROM generation_assets a
		JOIN generation_tasks t ON t.id = a.task_id
		WHERE a.id = ? AND a.task_id = ? AND t.owner_id = ? AND t.state = 'SUCCEEDED'
		  AND a.storage_key <> '' AND a.sha256 <> ''`,
		reference.AssetID, reference.TaskID, ownerID).Scan(&mediaType)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	if err != nil {
		return project.Reference{}, err
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	return project.Reference{TaskID: reference.TaskID, AssetID: reference.AssetID}, nil
}

func (source ProjectAssetSource) Model(ctx context.Context, ownerID string, reference project.Reference) (project.Reference, error) {
	if !reference.Valid() {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	if reference.InputAssetID != "" {
		var mediaType string
		err := source.db.QueryRowContext(ctx, `
			SELECT media_type FROM generation_input_assets
			WHERE id = ? AND owner_id = ? AND storage_key <> '' AND sha256 <> ''
			  AND media_type NOT LIKE 'image/%'`,
			reference.InputAssetID, ownerID).Scan(&mediaType)
		if errors.Is(err, sql.ErrNoRows) {
			return project.Reference{}, project.ErrReferenceInvalid
		}
		if err != nil {
			return project.Reference{}, err
		}
		return project.Reference{InputAssetID: reference.InputAssetID}, nil
	}
	var role string
	err := source.db.QueryRowContext(ctx, `
		SELECT a.role FROM generation_assets a
		JOIN generation_tasks t ON t.id = a.task_id
		WHERE a.id = ? AND a.task_id = ? AND t.owner_id = ? AND t.state = 'SUCCEEDED'
		  AND a.storage_key <> '' AND a.sha256 <> ''`,
		reference.AssetID, reference.TaskID, ownerID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	if err != nil {
		return project.Reference{}, err
	}
	if role != "model" && role != "converted_model" {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	return project.Reference{TaskID: reference.TaskID, AssetID: reference.AssetID}, nil
}
