package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

func TestCacheStatsCountDistinctRetainedGeneratedBlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache-stats.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id         string
		storageKey string
		size       int64
	}{
		{id: "asset-a", storageKey: "blobs/shared", size: 100},
		{id: "asset-b", storageKey: "blobs/shared", size: 100},
		{id: "asset-c", storageKey: "blobs/other", size: 250},
	} {
		if _, err := db.Exec(`
			INSERT INTO generation_assets (
				id, task_id, attempt_id, role, required, ordinal,
				storage_key, media_type, size_bytes, sha256, created_at
			) VALUES (?, ?, ?, ?, 1, 1, ?, ?, ?, ?, ?)`,
			row.id,
			"task-"+row.id,
			"attempt-"+row.id,
			"image",
			row.storageKey,
			"image/png",
			row.size,
			row.id,
			"2026-07-26T19:30:00Z",
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	stats, err := repository.CacheStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFiles != 2 || stats.TotalSizeBytes != 350 {
		t.Fatalf("stats = %#v", stats)
	}
}
