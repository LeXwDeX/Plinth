package sqlite

import (
	"context"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
)

func (r *Repository) CacheStats(
	ctx context.Context,
) (cachemanagement.Stats, error) {
	var stats cachemanagement.Stats
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM (
			SELECT storage_key, MAX(size_bytes) AS size_bytes
			FROM generation_assets
			GROUP BY storage_key
		)`).Scan(&stats.TotalFiles, &stats.TotalSizeBytes)
	if err != nil {
		return cachemanagement.Stats{}, fmt.Errorf("读取生成资产缓存统计: %w", err)
	}
	return stats, nil
}
