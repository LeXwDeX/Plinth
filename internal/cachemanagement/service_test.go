package cachemanagement_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
)

type fixedStats struct {
	stats cachemanagement.Stats
}

func (f fixedStats) CacheStats(
	context.Context,
) (cachemanagement.Stats, error) {
	return f.stats, nil
}

func TestStatusCombinesPersistedStatsAndActiveMaterializations(t *testing.T) {
	now := time.Date(2026, 7, 26, 19, 0, 0, 0, time.UTC)
	service, err := cachemanagement.New(cachemanagement.Config{
		Stats: fixedStats{stats: cachemanagement.Stats{
			TotalFiles:     3,
			TotalSizeBytes: 1572864,
		}},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	finishA := service.Begin(cachemanagement.Operation{
		Filename:   "preview.png",
		TaskID:     "task-a",
		TaskType:   "text-to-image",
		ProviderID: "openai",
		AssetKind:  "image",
	})
	now = now.Add(1500 * time.Millisecond)
	finishB := service.Begin(cachemanagement.Operation{
		Filename:   "model.glb",
		TaskID:     "task-b",
		TaskType:   "text-to-3d",
		ProviderID: "meshy",
		AssetKind:  "model",
	})
	now = now.Add(500 * time.Millisecond)

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Active != 2 ||
		status.Stats.TotalFiles != 3 ||
		status.Stats.TotalSizeBytes != 1572864 ||
		len(status.Items) != 2 ||
		status.Items[0].TaskID != "task-a" ||
		status.Items[0].StartedAt !=
			time.Date(2026, 7, 26, 19, 0, 0, 0, time.UTC) ||
		status.Items[1].TaskID != "task-b" {
		t.Fatalf("status = %#v", status)
	}
	finishA()
	finishA()
	status, err = service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Active != 1 || status.Items[0].TaskID != "task-b" {
		t.Fatalf("status after finish = %#v", status)
	}
	finishB()
}

func TestConcurrentBeginFinishAndStatusAreRaceSafe(t *testing.T) {
	service, err := cachemanagement.New(cachemanagement.Config{
		Stats: fixedStats{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 50; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			finish := service.Begin(cachemanagement.Operation{TaskID: "task"})
			_, _ = service.Status(context.Background())
			finish()
		}()
	}
	group.Wait()
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Active != 0 || len(status.Items) != 0 {
		t.Fatalf("status = %#v", status)
	}
}
