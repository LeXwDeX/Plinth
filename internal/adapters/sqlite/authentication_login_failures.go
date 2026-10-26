package sqlite

import (
	"context"
	"fmt"
	"time"
)

// CountLoginFailures 统计某键在 cutoff 之后的登录失败次数。
func (r *Repository) CountLoginFailures(
	ctx context.Context,
	clientKey string,
	cutoff time.Time,
) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM authentication_login_failures
		WHERE client_key = ? AND occurred_at > ?`,
		clientKey, formatTime(cutoff.UTC()),
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计登录失败: %w", err)
	}
	return count, nil
}

// RecordLoginFailure 记一次登录失败，并清掉该键窗口之外的记录。写入与清理
// 放在同一个调用里：只写不清的话，表会随尝试次数线性增长，而限流只需要
// 窗口内的计数。
func (r *Repository) RecordLoginFailure(
	ctx context.Context,
	clientKey string,
	at time.Time,
	pruneBefore time.Time,
) error {
	stamp := formatTime(at.UTC())
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM authentication_login_failures
		WHERE client_key = ? AND occurred_at <= ?`,
		clientKey, formatTime(pruneBefore.UTC()),
	); err != nil {
		return fmt.Errorf("清理过期登录失败: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO authentication_login_failures (client_key, occurred_at)
		VALUES (?, ?)`,
		clientKey, stamp,
	); err != nil {
		return fmt.Errorf("记录登录失败: %w", err)
	}
	return nil
}

// PruneLoginFailures 清理全局过期记录，防止该表无限增长。
func (r *Repository) PruneLoginFailures(
	ctx context.Context,
	cutoff time.Time,
) error {
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM authentication_login_failures
		WHERE occurred_at <= ?`,
		formatTime(cutoff.UTC()),
	); err != nil {
		return fmt.Errorf("清理登录失败记录: %w", err)
	}
	return nil
}
