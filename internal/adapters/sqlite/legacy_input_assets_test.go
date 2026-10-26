package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

func TestRepositoryReadsLegacyInputAssetMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-input.db")
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
		CREATE TABLE asset_registry (
			id TEXT PRIMARY KEY,
			assetType TEXT NOT NULL,
			originalFilename TEXT NOT NULL,
			storedFilename TEXT NOT NULL,
			mimeType TEXT NOT NULL,
			fileSize INTEGER NOT NULL,
			sourceSha256 TEXT NOT NULL,
			storedSha256 TEXT NOT NULL,
			storagePath TEXT NOT NULL,
			uploaderUserId TEXT NOT NULL,
			uploaderEmail TEXT NOT NULL DEFAULT '',
			createdAt TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	const createdAt = "2026-07-26T08:30:00Z"
	if _, err := db.Exec(`
		INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-asset",
		"image",
		"source.png",
		"stored.png",
		"image/png",
		23,
		"source-sha",
		"stored-sha",
		"image/ab/cd/stored.png",
		"owner-1",
		"owner@example.test",
		createdAt,
	); err != nil {
		t.Fatal(err)
	}

	asset, err := repository.GetLegacyInputAsset(
		context.Background(),
		"legacy-asset",
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedCreatedAt, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != "legacy-asset" ||
		asset.OwnerID != "owner-1" ||
		asset.StorageKey != "image/ab/cd/stored.png" ||
		asset.OriginalFilename != "source.png" ||
		asset.AssetType != "image" ||
		asset.MediaType != "image/png" ||
		asset.Size != 23 ||
		asset.SHA256 != "stored-sha" ||
		!asset.CreatedAt.Equal(expectedCreatedAt) {
		t.Fatalf("asset = %#v", asset)
	}
}
