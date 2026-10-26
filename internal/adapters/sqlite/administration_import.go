package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

type AdministrationImportStats struct {
	BalanceSnapshots ImportStats
	AuditEvents      ImportStats
}

// ImportAdministrationHistory 在一个事务中按旧稳定 ID 幂等导入余额快照与
// 审计事件。余额账户来自 Product Schema，而不是 ETL 内的 Provider 分支。
func (r *Repository) ImportAdministrationHistory(
	ctx context.Context,
	snapshots []administration.BalanceSnapshot,
	events []administration.AuditEvent,
) (AdministrationImportStats, error) {
	var result AdministrationImportStats
	balanceStats, err := r.importETLBatches(
		ctx,
		"balance_snapshot",
		len(snapshots),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, snapshot := range snapshots[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"balance_snapshot",
					snapshot.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_balance_snapshots (
				id, account_id, balance, captured_at
			) VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				account_id = excluded.account_id,
				balance = excluded.balance,
				captured_at = excluded.captured_at`,
					snapshot.ID,
					snapshot.AccountID,
					snapshot.Balance,
					formatTime(snapshot.CapturedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入余额快照 %s: %w",
						snapshot.ID,
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
	result.BalanceSnapshots = balanceStats
	auditStats, err := r.importETLBatches(
		ctx,
		"audit_event",
		len(events),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, event := range events[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"audit_event",
					event.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				detailJSON, err := json.Marshal(event.Detail)
				if err != nil {
					return stats, fmt.Errorf(
						"编码审计事件 %s 详情: %w",
						event.ID,
						err,
					)
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO administration_audit_events (
				id, action, actor_id, actor_name, resource_type, resource_id,
				detail_json, ip_address, user_agent, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				action = excluded.action,
				actor_id = excluded.actor_id,
				actor_name = excluded.actor_name,
				resource_type = excluded.resource_type,
				resource_id = excluded.resource_id,
				detail_json = excluded.detail_json,
				ip_address = excluded.ip_address,
				user_agent = excluded.user_agent,
				created_at = excluded.created_at`,
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
				); err != nil {
					return stats, fmt.Errorf(
						"导入审计事件 %s: %w",
						event.ID,
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
	result.AuditEvents = auditStats
	return result, nil
}
