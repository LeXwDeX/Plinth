package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
)

const authenticationUserColumns = `
	id, open_id, username, password_hash, nickname, name, email, roles_json,
	is_admin, is_active, deleted_at`

func (r *Repository) CreateBootstrapAdmin(
	ctx context.Context,
	record authentication.UserRecord,
	createdAt time.Time,
) error {
	roles, err := json.Marshal(record.Roles)
	if err != nil {
		return fmt.Errorf("编码认证用户角色: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO authentication_users (
			id, username, password_hash, nickname, name, email, roles_json,
			is_admin, is_active, created_at, updated_at, bootstrap_managed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		record.ID,
		record.Username,
		record.PasswordHash,
		record.Nickname,
		record.Name,
		record.Email,
		string(roles),
		boolInteger(record.Admin),
		boolInteger(record.Active),
		formatTime(createdAt),
		formatTime(createdAt),
	)
	if err != nil {
		return fmt.Errorf("创建认证管理员: %w", err)
	}
	return nil
}

func (r *Repository) FindActiveByUsername(
	ctx context.Context,
	username string,
) (authentication.UserRecord, bool, error) {
	return scanAuthenticationUser(r.db.QueryRowContext(
		ctx,
		`SELECT `+authenticationUserColumns+`
		FROM authentication_users
		WHERE username = ? AND is_active = 1 AND deleted_at IS NULL`,
		username,
	))
}

func (r *Repository) FindByUsername(
	ctx context.Context,
	username string,
) (authentication.UserRecord, bool, error) {
	return scanAuthenticationUser(r.db.QueryRowContext(
		ctx,
		`SELECT `+authenticationUserColumns+`
		FROM authentication_users
		WHERE username = ?`,
		username,
	))
}

func (r *Repository) FindByID(
	ctx context.Context,
	id string,
) (authentication.UserRecord, bool, error) {
	return scanAuthenticationUser(r.db.QueryRowContext(
		ctx,
		`SELECT `+authenticationUserColumns+`
		FROM authentication_users
		WHERE id = ?`,
		id,
	))
}

func (r *Repository) UpdateLastLogin(
	ctx context.Context,
	id string,
	at time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE authentication_users
		SET last_login = ?, updated_at = ?
		WHERE id = ?`,
		formatTime(at),
		formatTime(at),
		id,
	)
	if err != nil {
		return fmt.Errorf("更新认证用户最后登录时间: %w", err)
	}
	return requireAuthenticationUser(result)
}

func (r *Repository) UpdatePassword(
	ctx context.Context,
	id string,
	passwordHash string,
	at time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE authentication_users
		SET password_hash = ?, updated_at = ?
		WHERE id = ?`,
		passwordHash,
		formatTime(at),
		id,
	)
	if err != nil {
		return fmt.Errorf("更新认证用户密码: %w", err)
	}
	return requireAuthenticationUser(result)
}

type rowScanner interface {
	Scan(...any) error
}

func scanAuthenticationUser(
	row rowScanner,
) (authentication.UserRecord, bool, error) {
	var record authentication.UserRecord
	var rolesJSON string
	var admin int
	var active int
	var deletedAt sql.NullString
	err := row.Scan(
		&record.ID,
		&record.OpenID,
		&record.Username,
		&record.PasswordHash,
		&record.Nickname,
		&record.Name,
		&record.Email,
		&rolesJSON,
		&admin,
		&active,
		&deletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return authentication.UserRecord{}, false, nil
		}
		return authentication.UserRecord{}, false, fmt.Errorf(
			"读取认证用户: %w",
			err,
		)
	}
	if err := json.Unmarshal([]byte(rolesJSON), &record.Roles); err != nil {
		return authentication.UserRecord{}, false, fmt.Errorf(
			"解析认证用户角色: %w",
			err,
		)
	}
	record.Admin = admin != 0
	record.Active = active != 0
	record.Deleted = deletedAt.Valid
	return record, true, nil
}

func requireAuthenticationUser(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取认证用户更新结果: %w", err)
	}
	if affected == 0 {
		return errors.New("认证用户不存在")
	}
	return nil
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
