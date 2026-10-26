package httpapi

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) legacyCacheStatus(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	status, err := s.cache.Status(request.Context())
	if err != nil {
		s.logger.ErrorContext(
			context.WithoutCancel(request.Context()),
			"读取缓存状态失败",
			"error",
			err,
		)
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.internal",
		)
		return
	}
	writeJSON(
		writer,
		http.StatusOK,
		projectCacheStatus(
			s.application.Runtime.Cache,
			status,
			s.clock(),
		),
	)
}

func projectCacheStatus(
	schema catalog.CacheRuntimeSchema,
	status cachemanagement.Status,
	now time.Time,
) map[string]any {
	fields := schema.Fields
	items := make([]any, 0, len(status.Items))
	for _, operation := range status.Items {
		elapsed := math.Round(
			float64(now.Sub(operation.StartedAt).Milliseconds()) /
				float64(schema.MillisecondsPerSecond),
		)
		items = append(items, map[string]any{
			fields.Filename:   operation.Filename,
			fields.Elapsed:    int64(elapsed),
			fields.TaskID:     operation.TaskID,
			fields.TaskType:   operation.TaskType,
			fields.ProviderID: operation.ProviderID,
			fields.AssetKind:  operation.AssetKind,
		})
	}
	decimalFactor := math.Pow10(schema.SizeDecimalPlaces)
	size := math.Round(
		float64(status.Stats.TotalSizeBytes)/
			float64(schema.BytesPerMegabyte)*
			decimalFactor,
	) / decimalFactor
	return map[string]any{
		fields.ActiveDownloads:  status.Active,
		fields.ActiveItems:      items,
		fields.TotalCachedFiles: status.Stats.TotalFiles,
		fields.TotalCacheSizeMB: size,
	}
}
