package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

func TestBackfillStatusRepairsLegacySQLiteRecordsThroughAdministrationSeam(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "legacy-backfill.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE usage_ledger (
			id TEXT PRIMARY KEY,
			userId TEXT NOT NULL,
			userName TEXT NOT NULL,
			userEmail TEXT NOT NULL,
			taskType TEXT NOT NULL,
			taskId TEXT NOT NULL,
			providerId TEXT NOT NULL,
			estimatedCredits INTEGER NOT NULL,
			taskStatus TEXT NOT NULL,
			params TEXT NOT NULL,
			resultUrls TEXT NOT NULL,
			createdAt,
			finishedAt
		);
		CREATE TABLE task_cache (
			taskId TEXT PRIMARY KEY,
			taskType TEXT NOT NULL,
			status TEXT NOT NULL,
			providerId TEXT NOT NULL,
			data TEXT NOT NULL,
			cachedAt,
			updatedAt
		)`,
	); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 26, 7, 0, 0, 0, time.UTC)
	for _, record := range []struct {
		id         string
		taskID     string
		providerID string
	}{
		{id: "sync-ledger", taskID: "sync-task", providerID: "openai"},
		{id: "async-ledger", taskID: "async-task", providerID: "meshy"},
	} {
		if _, err := db.Exec(`
			INSERT INTO usage_ledger (
				id, userId, userName, userEmail, taskType, taskId,
				providerId, estimatedCredits, taskStatus, params,
				resultUrls, createdAt, finishedAt
			) VALUES (?, 'owner-1', 'Alice', 'alice@example.test',
				'text-to-image', ?, ?, 7, 'PENDING', '{}', '[]', ?, NULL
			)`,
			record.id,
			record.taskID,
			record.providerID,
			createdAt.UnixMilli(),
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		) VALUES (
			'async-task', 'image-to-3d', 'SUCCEEDED', 'meshy',
			'{"model_urls":{"glb":"https://assets.test/model.glb","fbx":"https://assets.test/model.fbx"},"thumbnail_url":"https://assets.test/thumb.png","texture_urls":[{"base_color":"https://assets.test/base.png"}]}',
			?, ?
		)`,
		createdAt.UnixMilli(),
		createdAt.UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	finishedAt := time.Date(2026, time.July, 26, 9, 0, 0, 0, time.UTC)
	module, err := administration.NewBackfillStatusModule(
		administration.BackfillStatusModuleConfig{
			Repository:      repository,
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
			Now: func() time.Time { return finishedAt },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := module.Backfill(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.SyncFixed != 1 ||
		result.AsyncFixed != 1 || result.Unchanged != 0 {
		t.Fatalf("result = %#v", result)
	}

	syncDetail, found, err := repository.GetAdministrationGeneration(
		context.Background(),
		"sync-task",
	)
	if err != nil || !found {
		t.Fatalf("sync detail found=%v err=%v", found, err)
	}
	if syncDetail.Ledger.TaskStatus != "SUCCEEDED" ||
		syncDetail.Ledger.FinishedAt == nil ||
		!syncDetail.Ledger.FinishedAt.Equal(finishedAt) {
		t.Fatalf("sync ledger = %#v", syncDetail.Ledger)
	}
	asyncDetail, found, err := repository.GetAdministrationGeneration(
		context.Background(),
		"async-task",
	)
	if err != nil || !found {
		t.Fatalf("async detail found=%v err=%v", found, err)
	}
	if asyncDetail.Ledger.TaskStatus != "SUCCEEDED" ||
		asyncDetail.Ledger.FinishedAt == nil ||
		!asyncDetail.Ledger.FinishedAt.Equal(finishedAt) {
		t.Fatalf("async ledger = %#v", asyncDetail.Ledger)
	}
	expectedURLs := []string{
		"https://assets.test/model.glb",
		"https://assets.test/model.fbx",
		"https://assets.test/thumb.png",
		"https://assets.test/base.png",
	}
	if len(asyncDetail.Ledger.ResultURLs) != len(expectedURLs) {
		t.Fatalf("result urls = %#v", asyncDetail.Ledger.ResultURLs)
	}
	for index, expected := range expectedURLs {
		if asyncDetail.Ledger.ResultURLs[index] != expected {
			t.Fatalf(
				"result urls[%d] = %q, want %q",
				index,
				asyncDetail.Ledger.ResultURLs[index],
				expected,
			)
		}
	}
}

func TestBackfillStatusTreatsAbsentLegacyTablesAsNoPendingWork(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "native-only.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	module, err := administration.NewBackfillStatusModule(
		administration.BackfillStatusModuleConfig{
			Repository:      repository,
			PendingStatus:   "PENDING",
			SucceededStatus: "SUCCEEDED",
			SyncProviderIDs: []string{"openai"},
			ResultFields: administration.BackfillResultFields{
				ModelURLs:    "model_urls",
				ThumbnailURL: "thumbnail_url",
				ImageURLs:    "image_urls",
				TextureURLs:  "texture_urls",
				BaseColor:    "base_color",
				VideoURL:     "video_url",
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
	if result != (administration.BackfillStatusResult{}) {
		t.Fatalf("result = %#v", result)
	}
}
