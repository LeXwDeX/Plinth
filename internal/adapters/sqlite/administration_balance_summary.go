package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

var nativeBalanceSnapshotSource = `
	SELECT
		id,
		account_id,
		balance,
		captured_at,
		` + databaseUnixExpression("captured_at") + ` AS captured_unix
	FROM generation_balance_snapshots`

func (r *Repository) RecordBalanceSnapshot(
	ctx context.Context,
	snapshot administration.BalanceSnapshot,
) error {
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO generation_balance_snapshots (
			id, account_id, balance, captured_at
		) VALUES (?, ?, ?, ?)`,
		snapshot.ID,
		snapshot.AccountID,
		snapshot.Balance,
		formatTime(snapshot.CapturedAt.UTC()),
	); err != nil {
		return fmt.Errorf("记录管理余额快照: %w", err)
	}
	return nil
}

func (r *Repository) QueryBalanceSnapshots(
	ctx context.Context,
	since time.Time,
) ([]administration.BalanceSnapshot, error) {
	source, err := r.balanceSnapshotSource(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, account_id, balance, captured_at
		 FROM (`+source+`) AS snapshot
		 WHERE captured_unix >= ?
		 ORDER BY captured_unix ASC, id ASC`,
		unixSeconds(since),
	)
	if err != nil {
		return nil, fmt.Errorf("查询管理余额历史: %w", err)
	}
	history := make([]administration.BalanceSnapshot, 0)
	for rows.Next() {
		var snapshot administration.BalanceSnapshot
		var capturedAt string
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.AccountID,
			&snapshot.Balance,
			&capturedAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取管理余额历史: %w", err)
		}
		snapshot.CapturedAt, err = parseDatabaseTime(capturedAt)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("解析管理余额历史时间: %w", err)
		}
		history = append(history, snapshot)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历管理余额历史: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭管理余额历史结果集: %w", err)
	}
	return history, nil
}

func (r *Repository) balanceSnapshotSource(
	ctx context.Context,
) (string, error) {
	exists, err := r.tableExists(ctx, "balance_snapshots")
	if err != nil {
		return "", fmt.Errorf("检查旧余额快照表: %w", err)
	}
	if !exists {
		return nativeBalanceSnapshotSource, nil
	}
	legacySource := `
		SELECT
			legacy.id,
			'' AS account_id,
			legacy.balance,
			CAST(legacy.capturedAt AS TEXT) AS captured_at,
			` + databaseUnixExpression("legacy.capturedAt") + ` AS captured_unix
		FROM balance_snapshots AS legacy
		WHERE NOT EXISTS (
			SELECT 1
			FROM generation_balance_snapshots AS native
			WHERE native.id = legacy.id
		)`
	return nativeBalanceSnapshotSource +
		"\nUNION ALL\n" +
		legacySource, nil
}
