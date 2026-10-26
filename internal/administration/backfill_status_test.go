package administration_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

type backfillStatusMemoryAdapter struct {
	records []administration.LegacyPendingUsage
	caches  map[string]administration.LegacyTaskCache
}

func (a *backfillStatusMemoryAdapter) ListPendingLegacyUsage(
	_ context.Context,
	status string,
) ([]administration.LegacyPendingUsage, error) {
	result := make([]administration.LegacyPendingUsage, 0)
	for _, record := range a.records {
		if record.Status == status {
			result = append(result, record)
		}
	}
	return result, nil
}

func (a *backfillStatusMemoryAdapter) GetLegacyTaskCache(
	_ context.Context,
	taskID string,
) (administration.LegacyTaskCache, bool, error) {
	cache, found := a.caches[taskID]
	return cache, found, nil
}

func (a *backfillStatusMemoryAdapter) UpdateLegacyUsageStatus(
	_ context.Context,
	update administration.LegacyUsageStatusUpdate,
) error {
	for index := range a.records {
		if a.records[index].ID == update.ID {
			a.records[index].Status = update.Status
			break
		}
	}
	return nil
}

func TestBackfillStatusRepairsSyncAndTerminalAsyncLegacyUsage(t *testing.T) {
	adapter := &backfillStatusMemoryAdapter{
		records: []administration.LegacyPendingUsage{
			{ID: "sync", TaskID: "sync-task", ProviderID: "openai", Status: "PENDING"},
			{ID: "success", TaskID: "success-task", ProviderID: "meshy", Status: "PENDING"},
			{ID: "failed", TaskID: "failed-task", ProviderID: "tripo", Status: "PENDING"},
			{ID: "active", TaskID: "active-task", ProviderID: "matting", Status: "PENDING"},
			{ID: "missing", TaskID: "missing-task", ProviderID: "meshy", Status: "PENDING"},
		},
		caches: map[string]administration.LegacyTaskCache{
			"success-task": {
				Status: "SUCCEEDED",
				Data:   `{"image_urls":["https://asset.test/image.png"]}`,
			},
			"failed-task": {Status: "FAILED", Data: `{}`},
			"active-task": {Status: "PENDING", Data: `{}`},
		},
	}
	module, err := administration.NewBackfillStatusModule(
		administration.BackfillStatusModuleConfig{
			Repository:      adapter,
			PendingStatus:   "PENDING",
			SucceededStatus: "SUCCEEDED",
			SyncProviderIDs: []string{"openai", "gemini"},
			ResultFields: administration.BackfillResultFields{
				ModelURLs:    "model_urls",
				ThumbnailURL: "thumbnail_url",
				ImageURLs:    "image_urls",
				TextureURLs:  "texture_urls",
				BaseColor:    "base_color",
				VideoURL:     "video_url",
			},
			Now: func() time.Time {
				return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := module.Backfill(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 5 ||
		result.SyncFixed != 1 ||
		result.AsyncFixed != 2 ||
		result.Unchanged != 2 {
		t.Fatalf("result = %#v", result)
	}
	second, err := module.Backfill(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 2 ||
		second.SyncFixed != 0 ||
		second.AsyncFixed != 0 ||
		second.Unchanged != 2 {
		t.Fatalf("second result = %#v", second)
	}
}
