package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LegacyMigrationRecordImport 保存无法安全投影为原生业务实体、但又不能
// 丢弃的旧记录。归档不向业务接口暴露，也不伪造 owner 或外键。
type LegacyMigrationRecordImport struct {
	SourceTable string
	SourceID    string
	Reason      string
	Payload     json.RawMessage
}

func (r *Repository) ImportLegacyMigrationRecords(
	ctx context.Context,
	records []LegacyMigrationRecordImport,
) (ImportStats, error) {
	for index, record := range records {
		if strings.TrimSpace(record.SourceTable) == "" ||
			strings.TrimSpace(record.SourceID) == "" ||
			strings.TrimSpace(record.Reason) == "" ||
			!json.Valid(record.Payload) {
			return ImportStats{}, fmt.Errorf(
				"历史迁移归档[%d] 字段不完整或 payload 不是合法 JSON",
				index,
			)
		}
	}
	return r.importETLBatches(
		ctx,
		"legacy_migration_record",
		len(records),
		func(tx *sql.Tx, start, end int) (ImportStats, error) {
			var stats ImportStats
			for _, record := range records[start:end] {
				sourceKey := record.SourceTable + "/" + record.SourceID
				claimed, err := claimETLImport(
					ctx,
					tx,
					"legacy_migration_record",
					sourceKey,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO legacy_migration_records (
						source_table, source_id, reason,
						payload_json, archived_at
					) VALUES (?, ?, ?, ?, ?)`,
					record.SourceTable,
					record.SourceID,
					record.Reason,
					string(record.Payload),
					formatTime(time.Now().UTC()),
				); err != nil {
					return stats, fmt.Errorf(
						"导入历史迁移归档 %s/%s: %w",
						record.SourceTable,
						record.SourceID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
}
