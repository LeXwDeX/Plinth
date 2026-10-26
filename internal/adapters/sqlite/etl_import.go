package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const etlImportBatchSize = 250

type etlBatchObserverKey struct{}

// WithETLBatchObserver 为迁移恢复测试提供批次提交接缝。观察器只在事务
// 已成功提交后执行；返回错误可模拟进程在任意已提交批次后的中断。
func WithETLBatchObserver(
	ctx context.Context,
	observer func(domain string, committed int) error,
) context.Context {
	return context.WithValue(ctx, etlBatchObserverKey{}, observer)
}

type ImportStats struct {
	Migrated int
	Skipped  int
}

func (stats *ImportStats) add(other ImportStats) {
	stats.Migrated += other.Migrated
	stats.Skipped += other.Skipped
}

func claimETLImport(
	ctx context.Context,
	tx *sql.Tx,
	domain string,
	sourceID string,
) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO etl_import_items (domain, source_id, migrated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(domain, source_id) DO NOTHING`,
		domain,
		sourceID,
		formatTime(time.Now().UTC()),
	)
	if err != nil {
		return false, fmt.Errorf(
			"登记 ETL 项 %s/%s: %w",
			domain,
			sourceID,
			err,
		)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(
			"读取 ETL 项状态 %s/%s: %w",
			domain,
			sourceID,
			err,
		)
	}
	return rows == 1, nil
}

func (r *Repository) importETLBatches(
	ctx context.Context,
	domain string,
	total int,
	importBatch func(*sql.Tx, int, int) (ImportStats, error),
) (ImportStats, error) {
	var result ImportStats
	for start := 0; start < total; start += etlImportBatchSize {
		end := min(start+etlImportBatchSize, total)
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return result, fmt.Errorf("开始 ETL 批次 %d-%d: %w", start, end, err)
		}
		batch, err := importBatch(tx, start, end)
		if err != nil {
			_ = tx.Rollback()
			return result, err
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return result, fmt.Errorf("提交 ETL 批次 %d-%d: %w", start, end, err)
		}
		result.add(batch)
		observer, observing := ctx.Value(etlBatchObserverKey{}).(func(string, int) error)
		if observing {
			if err := observer(domain, end); err != nil {
				return result, fmt.Errorf(
					"ETL 域 %s 已提交 %d 行后中断: %w",
					domain,
					end,
					err,
				)
			}
		}
	}
	return result, nil
}
