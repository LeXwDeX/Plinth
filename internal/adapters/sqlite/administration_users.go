package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

const administrationUserColumns = `
	id, username, password_hash, nickname, name, email, open_id, roles_json,
	is_admin, is_active, created_at, updated_at, deleted_at, last_login`

func (r *Repository) ListAdministrationUsers(
	ctx context.Context,
) ([]administration.User, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+administrationUserColumns+`
		FROM authentication_users
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出管理用户: %w", err)
	}
	defer rows.Close()
	users := []administration.User{}
	for rows.Next() {
		stored, exists, scanErr := scanAdministrationUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if exists {
			users = append(users, stored.User)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历管理用户: %w", err)
	}
	return users, nil
}

func (r *Repository) FindAdministrationUser(
	ctx context.Context,
	id string,
) (administration.StoredUser, bool, error) {
	return scanAdministrationUser(r.db.QueryRowContext(ctx, `
		SELECT `+administrationUserColumns+`
		FROM authentication_users
		WHERE id = ?`,
		id,
	))
}

func (r *Repository) FindAdministrationUserByUsername(
	ctx context.Context,
	username string,
) (administration.StoredUser, bool, error) {
	return scanAdministrationUser(r.db.QueryRowContext(ctx, `
		SELECT `+administrationUserColumns+`
		FROM authentication_users
		WHERE username = ?`,
		username,
	))
}

func (r *Repository) CreateAdministrationUser(
	ctx context.Context,
	stored administration.StoredUser,
) error {
	rolesJSON, err := json.Marshal(stored.Roles)
	if err != nil {
		return fmt.Errorf("编码管理用户角色: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO authentication_users (
			id, username, password_hash, nickname, name, email, roles_json,
			is_admin, is_active, created_at, updated_at, last_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stored.ID,
		stored.Username,
		stored.PasswordHash,
		stored.Nickname,
		stored.Name,
		stored.Email,
		string(rolesJSON),
		boolInteger(stored.Admin),
		boolInteger(stored.Active),
		formatTime(stored.CreatedAt),
		formatTime(stored.UpdatedAt),
		nullTimePointer(stored.LastLogin),
	)
	if err != nil {
		if isAdministrationUsernameConflict(err) {
			return administration.ErrDuplicateUsername
		}
		return fmt.Errorf("创建管理用户: %w", err)
	}
	return nil
}

func (r *Repository) UpdateAdministrationUser(
	ctx context.Context,
	stored administration.StoredUser,
	guards administration.UserGuards,
) error {
	rolesJSON, err := json.Marshal(stored.Roles)
	if err != nil {
		return fmt.Errorf("编码管理用户角色: %w", err)
	}
	return r.withAdministrationUserWrite(ctx, stored.ID, stored.Admin && stored.Active, guards, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
		UPDATE authentication_users
		SET password_hash = ?, nickname = ?, name = ?, email = ?,
			roles_json = ?, is_admin = ?, is_active = ?, updated_at = ?,
			last_login = ?
		WHERE id = ? AND deleted_at IS NULL`,
			stored.PasswordHash,
			stored.Nickname,
			stored.Name,
			stored.Email,
			string(rolesJSON),
			boolInteger(stored.Admin),
			boolInteger(stored.Active),
			formatTime(stored.UpdatedAt),
			nullTimePointer(stored.LastLogin),
			stored.ID,
		)
		if err != nil {
			return fmt.Errorf("更新管理用户: %w", err)
		}
		return requireAdministrationUser(result)
	})
}

func (r *Repository) SoftDeleteAdministrationUser(
	ctx context.Context,
	id string,
	at time.Time,
	guards administration.UserGuards,
) error {
	return r.withAdministrationUserWrite(ctx, id, false, guards, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
		UPDATE authentication_users
		SET deleted_at = ?, is_active = 0, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
			formatTime(at),
			formatTime(at),
			id,
		)
		if err != nil {
			return fmt.Errorf("软删除管理用户: %w", err)
		}
		return requireAdministrationUser(result)
	})
}

// Reserve the database writer before reading the administrator count. The
// admission decision remains atomic across connections and processes.
func (r *Repository) withAdministrationUserWrite(
	ctx context.Context,
	id string,
	retainsAdministrator bool,
	guards administration.UserGuards,
	mutate func(*sql.Conn) error,
) error {
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
	var activeAdministrator bool
	err = conn.QueryRowContext(ctx, `
		SELECT is_admin = 1 AND is_active = 1
		FROM authentication_users WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&activeAdministrator)
	if errors.Is(err, sql.ErrNoRows) {
		return administration.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("读取管理员状态: %w", err)
	}
	if activeAdministrator && !retainsAdministrator {
		var remaining int
		if err := conn.QueryRowContext(ctx, `
			SELECT count(*) FROM authentication_users
			WHERE is_admin = 1 AND is_active = 1 AND deleted_at IS NULL AND id <> ?`, id).
			Scan(&remaining); err != nil {
			return fmt.Errorf("统计可用管理员: %w", err)
		}
		if remaining < guards.MinimumActiveAdministrators {
			return administration.ErrLastAdministrator
		}
	}
	if err := mutate(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *Repository) RecordAudit(
	ctx context.Context,
	event administration.AuditEvent,
) error {
	detailJSON, err := json.Marshal(event.Detail)
	if err != nil {
		return fmt.Errorf("编码管理审计详情: %w", err)
	}
	if event.ID == "" {
		event.ID = "audit_" + secureToken()
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO administration_audit_events (
			id, action, actor_id, actor_name, resource_type, resource_id,
			detail_json, ip_address, user_agent, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Action,
		event.ActorID,
		event.ActorName,
		event.ResourceType,
		event.ResourceID,
		string(detailJSON),
		event.IPAddress,
		event.UserAgent,
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("写入管理审计事件: %w", err)
	}
	return nil
}

func (r *Repository) ListAdministrationAuditEvents(
	ctx context.Context,
) ([]administration.AuditEvent, error) {
	events, _, err := r.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{Page: 1, PageSize: 2_147_483_647},
	)
	return events, err
}

func (r *Repository) QueryAdministrationAuditEvents(
	ctx context.Context,
	query administration.AuditQuery,
) ([]administration.AuditEvent, int, error) {
	source, err := r.administrationAuditSource(ctx)
	if err != nil {
		return nil, 0, err
	}
	conditions := []string{"1 = 1"}
	args := []any{}
	if query.UserID != "" {
		conditions = append(conditions, "actor_id = ?")
		args = append(args, query.UserID)
	}
	if query.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, query.Action)
	}
	if query.From != nil {
		conditions = append(conditions, "created_unix >= ?")
		args = append(args, unixSeconds(*query.From))
	}
	if query.To != nil {
		conditions = append(conditions, "created_unix <= ?")
		args = append(args, unixSeconds(*query.To))
	}
	where := strings.Join(conditions, " AND ")
	countQuery := `SELECT COUNT(*) FROM (` + source + `) AS audit_events WHERE ` + where
	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计管理审计事件: %w", err)
	}
	offset := (query.Page - 1) * query.PageSize
	dataArgs := append(append([]any(nil), args...), query.PageSize, offset)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, action, actor_id, actor_name, resource_type, resource_id,
			detail_json, ip_address, user_agent, created_at
		FROM (`+source+`) AS audit_events
		WHERE `+where+`
		ORDER BY created_unix DESC, id DESC
		LIMIT ? OFFSET ?`,
		dataArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("列出管理审计事件: %w", err)
	}
	defer rows.Close()
	events := []administration.AuditEvent{}
	for rows.Next() {
		var event administration.AuditEvent
		var detailJSON string
		var createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.Action,
			&event.ActorID,
			&event.ActorName,
			&event.ResourceType,
			&event.ResourceID,
			&detailJSON,
			&event.IPAddress,
			&event.UserAgent,
			&createdAt,
		); err != nil {
			return nil, 0, fmt.Errorf("读取管理审计事件: %w", err)
		}
		if err := json.Unmarshal([]byte(detailJSON), &event.Detail); err != nil {
			return nil, 0, fmt.Errorf("解析管理审计详情: %w", err)
		}
		parsed, err := parseDatabaseTime(createdAt)
		if err != nil {
			return nil, 0, err
		}
		event.CreatedAt = parsed
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历管理审计事件: %w", err)
	}
	return events, total, nil
}

func (r *Repository) administrationAuditSource(
	ctx context.Context,
) (string, error) {
	const goSource = `
		SELECT id, action, actor_id, actor_name, resource_type, resource_id,
			detail_json, ip_address, user_agent, created_at,
			unixepoch(created_at, 'subsec') AS created_unix
		FROM administration_audit_events`
	var legacyTableCount int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'audit_logs'`,
	).Scan(&legacyTableCount); err != nil {
		return "", fmt.Errorf("检查旧审计表: %w", err)
	}
	if legacyTableCount == 0 {
		return goSource, nil
	}
	const legacySource = `
		SELECT id, action, userId AS actor_id, userName AS actor_name,
			resourceType AS resource_type, resourceId AS resource_id,
			detail AS detail_json, ipAddress AS ip_address,
			userAgent AS user_agent, CAST(createdAt AS TEXT) AS created_at,
			CASE
				WHEN typeof(createdAt) IN ('integer', 'real') THEN
					CASE
						WHEN createdAt > 100000000000
						THEN createdAt / 1000.0
						ELSE createdAt
					END
				ELSE unixepoch(createdAt, 'subsec')
			END AS created_unix
		FROM audit_logs`
	return goSource + "\nUNION ALL\n" + legacySource, nil
}

func unixSeconds(value time.Time) float64 {
	return float64(value.UnixNano()) / float64(time.Second)
}

func parseDatabaseTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	numeric, err := strconv.ParseFloat(value, 64)
	if err == nil {
		if numeric > 100_000_000_000 {
			return time.UnixMilli(int64(numeric)).UTC(), nil
		}
		seconds := int64(numeric)
		nanoseconds := int64(
			(numeric - float64(seconds)) * float64(time.Second),
		)
		return time.Unix(seconds, nanoseconds).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("解析数据库时间 %q", value)
}

func scanAdministrationUser(
	row rowScanner,
) (administration.StoredUser, bool, error) {
	var stored administration.StoredUser
	var rolesJSON string
	var admin int
	var active int
	var createdAt string
	var updatedAt string
	var deletedAt sql.NullString
	var lastLogin sql.NullString
	var openID sql.NullString
	err := row.Scan(
		&stored.ID,
		&stored.Username,
		&stored.PasswordHash,
		&stored.Nickname,
		&stored.Name,
		&stored.Email,
		&openID,
		&rolesJSON,
		&admin,
		&active,
		&createdAt,
		&updatedAt,
		&deletedAt,
		&lastLogin,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return administration.StoredUser{}, false, nil
		}
		return administration.StoredUser{}, false, fmt.Errorf(
			"读取管理用户: %w",
			err,
		)
	}
	if err := json.Unmarshal([]byte(rolesJSON), &stored.Roles); err != nil {
		return administration.StoredUser{}, false, fmt.Errorf(
			"解析管理用户角色: %w",
			err,
		)
	}
	stored.Admin = admin != 0
	stored.Active = active != 0
	stored.Deleted = deletedAt.Valid
	if openID.Valid && strings.TrimSpace(openID.String) != "" {
		value := openID.String
		stored.OpenID = &value
	}
	stored.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return administration.StoredUser{}, false, err
	}
	stored.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return administration.StoredUser{}, false, err
	}
	if lastLogin.Valid {
		parsed, parseErr := parseTime(lastLogin.String)
		if parseErr != nil {
			return administration.StoredUser{}, false, parseErr
		}
		stored.LastLogin = &parsed
	}
	return stored, true, nil
}

func requireAdministrationUser(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取管理用户更新结果: %w", err)
	}
	if affected == 0 {
		return administration.ErrUserNotFound
	}
	return nil
}

func isAdministrationUsernameConflict(err error) bool {
	return strings.Contains(
		err.Error(),
		"UNIQUE constraint failed: authentication_users.username",
	)
}
