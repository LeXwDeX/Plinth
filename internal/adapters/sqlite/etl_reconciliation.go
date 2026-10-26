package sqlite

import (
	"context"
	"fmt"
)

type ETLReconciliation struct {
	Users            int
	Tasks            int
	Attempts         int
	UsageLedger      int
	OutputAssets     int
	InputAssets      int
	ChatSessions     int
	ChatMessages     int
	CanvasProjects   int
	CanvasNodes      int
	BalanceSnapshots int
	AuditEvents      int
	LegacyRecords    int
	Credits          int
}

// ReconcileETL 只统计带 ETL 标记的原生业务行，因此 Go 服务在迁移期间
// 新写入的数据不会污染旧库对账数字。
func (r *Repository) ReconcileETL(
	ctx context.Context,
) (ETLReconciliation, error) {
	var result ETLReconciliation
	err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*)
			 FROM authentication_users AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'authentication_user'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM generation_tasks AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'generation_task'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM generation_attempts AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'generation_task'
			  AND item.source_id = row.task_id),
			(SELECT COUNT(*)
			 FROM generation_usage_ledger AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'usage_ledger'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM generation_assets AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'generation_task'
			  AND item.source_id = row.task_id),
			(SELECT COUNT(*)
			 FROM generation_input_assets AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'input_asset'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM go_chat_sessions AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'chat_session'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM go_chat_messages AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'chat_message'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM go_canvas_projects AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'canvas_project'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM go_canvas_nodes AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'canvas_node'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM generation_balance_snapshots AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'balance_snapshot'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM administration_audit_events AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'audit_event'
			  AND item.source_id = row.id),
			(SELECT COUNT(*)
			 FROM legacy_migration_records AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'legacy_migration_record'
			  AND item.source_id =
			      row.source_table || '/' || row.source_id),
			(SELECT COALESCE(SUM(row.estimated_credits), 0)
			 FROM generation_usage_ledger AS row
			 JOIN etl_import_items AS item
			   ON item.domain = 'usage_ledger'
			  AND item.source_id = row.id)`,
	).Scan(
		&result.Users,
		&result.Tasks,
		&result.Attempts,
		&result.UsageLedger,
		&result.OutputAssets,
		&result.InputAssets,
		&result.ChatSessions,
		&result.ChatMessages,
		&result.CanvasProjects,
		&result.CanvasNodes,
		&result.BalanceSnapshots,
		&result.AuditEvents,
		&result.LegacyRecords,
		&result.Credits,
	)
	if err != nil {
		return ETLReconciliation{}, fmt.Errorf("读取 ETL 原生对账: %w", err)
	}
	return result, nil
}
