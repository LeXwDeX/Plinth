package httpapi

import (
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestCacheStatusProjectionMatchesLegacyContract(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 7, 26, 19, 0, 0, 0, time.UTC)
	actual := projectCacheStatus(
		taskCatalog.Application().Runtime.Cache,
		cachemanagement.Status{
			Active: 1,
			Items: []cachemanagement.Operation{{
				Filename:   "preview.png",
				TaskID:     "task-a",
				TaskType:   "text-to-image",
				ProviderID: "openai",
				AssetKind:  "image",
				StartedAt:  started,
			}},
			Stats: cachemanagement.Stats{
				TotalFiles:     3,
				TotalSizeBytes: 1572864,
			},
		},
		started.Add(1499*time.Millisecond),
	)
	expected := map[string]any{
		"activeDownloads": 1,
		"activeItems": []any{map[string]any{
			"filename":   "preview.png",
			"elapsed":    int64(1),
			"taskId":     "task-a",
			"taskType":   "text-to-image",
			"providerId": "openai",
			"assetKind":  "image",
		}},
		"totalCachedFiles": int64(3),
		// Decimal MB like every size since #327: 1,572,864 bytes is 1.6 MB.
		"totalCacheSizeMB": 1.6,
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("cache status = %#v", actual)
	}
}

func TestCacheStatusProjectionChangesWithProductSchema(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Runtime.Cache
	schema.MillisecondsPerSecond = 500
	schema.BytesPerMegabyte = 1024
	schema.SizeDecimalPlaces = 2
	schema.Fields.ActiveDownloads = "running"
	schema.Fields.ActiveItems = "entries"
	schema.Fields.Filename = "file"
	schema.Fields.Elapsed = "age"
	schema.Fields.TaskID = "job"
	schema.Fields.TaskType = "kind"
	schema.Fields.ProviderID = "source"
	schema.Fields.AssetKind = "asset"
	schema.Fields.TotalCachedFiles = "files"
	schema.Fields.TotalCacheSizeMB = "size"
	started := time.Date(2026, 7, 26, 19, 0, 0, 0, time.UTC)
	actual := projectCacheStatus(
		schema,
		cachemanagement.Status{
			Active: 1,
			Items: []cachemanagement.Operation{{
				Filename:   "model.glb",
				TaskID:     "task-b",
				TaskType:   "text-to-3d",
				ProviderID: "meshy",
				AssetKind:  "model",
				StartedAt:  started,
			}},
			Stats: cachemanagement.Stats{
				TotalFiles:     2,
				TotalSizeBytes: 1536,
			},
		},
		started.Add(1499*time.Millisecond),
	)
	expected := map[string]any{
		"running": 1,
		"entries": []any{map[string]any{
			"file":   "model.glb",
			"age":    int64(3),
			"job":    "task-b",
			"kind":   "text-to-3d",
			"source": "meshy",
			"asset":  "model",
		}},
		"files": int64(2),
		"size":  1.5,
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("schema-driven cache status = %#v", actual)
	}
}
