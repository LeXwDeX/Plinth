package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ImportInputAssets 按旧资产 ID 幂等导入已经固化到原生 CAS 的输入资产。
func (r *Repository) ImportInputAssets(
	ctx context.Context,
	assets []generation.InputAsset,
) (ImportStats, error) {
	return r.importETLBatches(
		ctx,
		"input_asset",
		len(assets),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, asset := range assets[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"input_asset",
					string(asset.ID),
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_input_assets (
				id, owner_id, storage_key, original_filename, asset_type,
				media_type, size_bytes, sha256, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				owner_id = excluded.owner_id,
				storage_key = excluded.storage_key,
				original_filename = excluded.original_filename,
				asset_type = excluded.asset_type,
				media_type = excluded.media_type,
				size_bytes = excluded.size_bytes,
				sha256 = excluded.sha256,
				created_at = excluded.created_at`,
					asset.ID,
					asset.OwnerID,
					asset.StorageKey,
					asset.OriginalFilename,
					asset.AssetType,
					asset.MediaType,
					asset.Size,
					asset.SHA256,
					formatTime(asset.CreatedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入输入资产 %s: %w",
						asset.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
}
