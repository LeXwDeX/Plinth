package etl

import (
	"context"
	"database/sql"
	"fmt"
)

// legacyQuery allows every source loader and independent baseline query to share
// the same read-only transaction rather than observe different database versions.
type legacyQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type legacyBaseline struct {
	counts  map[string]int
	tasks   int
	credits int
}

func readLegacyBaseline(source legacyQuery) (legacyBaseline, error) {
	counts, err := countLegacyTables(source)
	if err != nil {
		return legacyBaseline{}, err
	}
	result := legacyBaseline{counts: counts}
	var invalid int
	if err := source.QueryRow(`SELECT COUNT(*) FROM usage_ledger WHERE taskId IS NULL OR trim(taskId) = ''`).Scan(&invalid); err != nil {
		return result, fmt.Errorf("检查旧账本 taskId: %w", err)
	}
	if invalid != 0 {
		return result, fmt.Errorf("旧账本存在 %d 行空 taskId，拒绝统计任务基线", invalid)
	}
	if err := source.QueryRow(`SELECT COUNT(DISTINCT taskId), COALESCE(SUM(estimatedCredits), 0) FROM usage_ledger`).Scan(&result.tasks, &result.credits); err != nil {
		return result, fmt.Errorf("统计旧账本任务和金额基线: %w", err)
	}
	return result, nil
}

// Auxiliary tables are represented by a migrated task/Attempt or by an isolated
// archive, not a one-to-one native table. Read the persisted target independently
// for each source identity; unrelated archives cannot compensate for a missing row.
func countTaskAuxiliaryTargets(ctx context.Context, source legacyQuery, targetPath string) (map[string]int, error) {
	target, err := sql.Open("sqlite", readOnlyDSN(targetPath))
	if err != nil {
		return nil, fmt.Errorf("打开辅表目标对账: %w", err)
	}
	defer target.Close()
	snapshot, err := target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("创建辅表目标对账快照: %w", err)
	}
	defer snapshot.Rollback()
	counts := map[string]int{}
	for _, table := range []string{"task_cache", "task_input_snapshot"} {
		rows, err := source.QueryContext(ctx, `SELECT auxiliary.taskId, ledger.taskId IS NOT NULL
 FROM `+table+` AS auxiliary
 LEFT JOIN (SELECT DISTINCT taskId FROM usage_ledger) AS ledger
   ON ledger.taskId = auxiliary.taskId
 ORDER BY auxiliary.taskId`)
		if err != nil {
			return nil, fmt.Errorf("读取旧 %s 对账身份: %w", table, err)
		}
		for rows.Next() {
			var id string
			var merged bool
			if err := rows.Scan(&id, &merged); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("读取旧 %s 对账行: %w", table, err)
			}
			var represented bool
			if merged {
				err = snapshot.QueryRowContext(ctx, `SELECT EXISTS(
     SELECT 1 FROM generation_tasks AS task
     JOIN etl_import_items AS item ON item.domain = 'generation_task' AND item.source_id = task.id
     WHERE task.id = ? AND EXISTS(SELECT 1 FROM generation_attempts WHERE task_id = task.id))`, id).Scan(&represented)
			} else {
				err = snapshot.QueryRowContext(ctx, `SELECT EXISTS(
     SELECT 1 FROM legacy_migration_records AS archive
     JOIN etl_import_items AS item ON item.domain = 'legacy_migration_record'
      AND item.source_id = archive.source_table || '/' || archive.source_id
     WHERE archive.source_table = ? AND archive.source_id = ?)`, table, id).Scan(&represented)
			}
			if err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("核实 %s 目标记录: %w", table, err)
			}
			if represented {
				counts[table]++
			}
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			return nil, fmt.Errorf("遍历旧 %s 对账行: %w", table, rowsErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭旧 %s 对账游标: %w", table, closeErr)
		}
	}
	return counts, nil
}
