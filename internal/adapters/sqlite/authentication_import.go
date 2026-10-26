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

// AuthenticationUserImport 是 ETL 从旧生产库带来的完整用户行：
// UserRecord 之外补齐时间戳字段（UserRecord 仅承载 Deleted 布尔语义）。
type AuthenticationUserImport struct {
	Record    authentication.UserRecord
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	LastLogin *time.Time
}

// ImportAuthenticationUsers 幂等导入用户：按 id 整行覆盖（upsert），
// 重跑不产生重复；username 已被其他 id 占用时整体失败并回滚，
// 由操作者裁决冲突（旧生产数据是真源，不做静默合并）。
func (r *Repository) ImportAuthenticationUsers(
	ctx context.Context,
	users []AuthenticationUserImport,
) (ImportStats, error) {
	return r.importETLBatches(
		ctx,
		"authentication_user",
		len(users),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, user := range users[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"authentication_user",
					user.Record.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				roles := user.Record.Roles
				if roles == nil {
					roles = []string{}
				}
				rolesJSON, err := json.Marshal(roles)
				if err != nil {
					return stats, fmt.Errorf(
						"编码用户 %s 角色: %w",
						user.Record.ID,
						err,
					)
				}
				var holderID string
				err = tx.QueryRowContext(ctx,
					`SELECT id FROM authentication_users WHERE username = ?`,
					user.Record.Username,
				).Scan(&holderID)
				if err == nil && holderID != user.Record.ID {
					replaced, replaceErr := replaceManagedBootstrap(
						ctx,
						tx,
						holderID,
						user,
					)
					if replaceErr != nil {
						return stats, replaceErr
					}
					if !replaced {
						return stats, fmt.Errorf(
							"用户名 %q 已被目标库用户 %s 占用（导入用户 %s）",
							user.Record.Username,
							holderID,
							user.Record.ID,
						)
					}
				} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return stats, fmt.Errorf(
						"检查用户名 %q 占用: %w",
						user.Record.Username,
						err,
					)
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO authentication_users (
				id, open_id, username, password_hash, nickname, name,
				email, roles_json, is_admin, is_active, created_at,
				updated_at, deleted_at, last_login, bootstrap_managed
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(id) DO UPDATE SET
				open_id = excluded.open_id,
				username = excluded.username,
				password_hash = excluded.password_hash,
				nickname = excluded.nickname,
				name = excluded.name,
				email = excluded.email,
				roles_json = excluded.roles_json,
				is_admin = excluded.is_admin,
				is_active = excluded.is_active,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				deleted_at = excluded.deleted_at,
				last_login = excluded.last_login,
				bootstrap_managed = 0`,
					user.Record.ID,
					user.Record.OpenID,
					user.Record.Username,
					user.Record.PasswordHash,
					user.Record.Nickname,
					user.Record.Name,
					user.Record.Email,
					string(rolesJSON),
					boolInteger(user.Record.Admin),
					boolInteger(user.Record.Active),
					formatTime(user.CreatedAt),
					formatTime(user.UpdatedAt),
					nullableTime(user.DeletedAt),
					nullableTime(user.LastLogin),
				); err != nil {
					return stats, fmt.Errorf(
						"导入用户 %s: %w",
						user.Record.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
}

func replaceManagedBootstrap(
	ctx context.Context,
	tx *sql.Tx,
	holderID string,
	incoming AuthenticationUserImport,
) (bool, error) {
	if !incoming.Record.Admin {
		return false, nil
	}
	var managed int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT bootstrap_managed
		 FROM authentication_users
		 WHERE id = ?`,
		holderID,
	).Scan(&managed); err != nil {
		return false, fmt.Errorf("检查启动管理员 %s: %w", holderID, err)
	}
	if managed == 0 {
		return false, nil
	}
	var references int
	if err := tx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM generation_tasks WHERE owner_id = ?) +
			(SELECT COUNT(*) FROM generation_input_assets WHERE owner_id = ?) +
			(SELECT COUNT(*) FROM generation_task_tombstones WHERE actor_id = ?) +
			(SELECT COUNT(*) FROM go_chat_sessions WHERE owner_id = ?) +
			(SELECT COUNT(*) FROM go_chat_turns WHERE owner_id = ?) +
			(SELECT COUNT(*) FROM go_canvas_projects WHERE owner_id = ?) +
			(SELECT COUNT(*) FROM administration_audit_events
			 WHERE actor_id = ?)`,
		holderID,
		holderID,
		holderID,
		holderID,
		holderID,
		holderID,
		holderID,
	).Scan(&references); err != nil {
		return false, fmt.Errorf("检查启动管理员 %s 关联数据: %w", holderID, err)
	}
	if references != 0 {
		return false, fmt.Errorf(
			"启动管理员 %s 已产生 %d 条关联数据，拒绝用旧用户 %s 替换",
			holderID,
			references,
			incoming.Record.ID,
		)
	}
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM authentication_users
		 WHERE id = ? AND bootstrap_managed = 1`,
		holderID,
	); err != nil {
		return false, fmt.Errorf("移除未使用的启动管理员 %s: %w", holderID, err)
	}
	return true, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}
