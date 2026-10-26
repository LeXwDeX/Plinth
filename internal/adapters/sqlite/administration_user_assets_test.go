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

func TestAdministrationUserAssetsCombinesNativeAndLegacyActivity(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "user-assets.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)
	seedAdministrationUserAssets(t, path, repository)
	from := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)

	overview, found, err := repository.QueryAdministrationUserAssets(
		context.Background(),
		administration.UserAssetsQuery{
			UserID:          "native-user",
			PageSize:        10,
			UploadsPage:     1,
			GenerationsPage: 1,
			DownloadsPage:   1,
			From:            &from,
			To:              &to,
			DownloadActions: []string{"download_asset", "access_asset"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || overview.User.Username != "native-alice" {
		t.Fatalf("owner found=%v user=%+v", found, overview.User)
	}
	if overview.Summary.TotalUploads != 2 ||
		overview.Summary.TotalGenerations != 1 ||
		overview.Summary.TotalDownloads != 2 ||
		overview.Summary.TotalStorageBytes != 3072 {
		t.Fatalf(
			"summary = %+v downloads=%#v",
			overview.Summary,
			overview.Downloads.Data,
		)
	}
	if len(overview.Uploads.Data) != 2 ||
		overview.Uploads.Data[0].ID != "native-upload" ||
		overview.Uploads.Data[0].StoredFilename != "native.png" ||
		overview.Uploads.Data[1].ID != "legacy-upload" {
		t.Fatalf("uploads = %#v", overview.Uploads.Data)
	}
	if len(overview.Generations.Data) != 1 ||
		overview.Generations.Data[0].TaskID != "native-task" ||
		len(overview.Generations.Data[0].Assets) != 1 ||
		overview.Generations.Data[0].Offering != frozenAdministrationOffering {
		t.Fatalf("generations = %#v", overview.Generations.Data)
	}
	if len(overview.Downloads.Data) != 2 ||
		overview.Downloads.Data[0].ID != "go-access" ||
		overview.Downloads.Data[1].ID != "legacy-download" {
		t.Fatalf("downloads = %#v", overview.Downloads.Data)
	}

	_, found, err = repository.QueryAdministrationUserAssets(
		context.Background(),
		administration.UserAssetsQuery{
			UserID:          "missing",
			PageSize:        10,
			UploadsPage:     1,
			GenerationsPage: 1,
			DownloadsPage:   1,
			DownloadActions: []string{"download_asset", "access_asset"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("missing owner was reported as found")
	}
}

func TestListOwnedInputAssetsUsesOwnerScopeAndHidesDuplicateLegacyRows(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "account-resources.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)
	seedAdministrationUserAssets(t, path, repository)

	assets, total, err := repository.ListOwnedInputAssets(
		context.Background(),
		"native-user",
		1,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(assets) != 1 || assets[0].ID != "native-upload" {
		t.Fatalf("page1 assets=%+v total=%d", assets, total)
	}
	if assets[0].StorageKey != "uploads/native.png" {
		t.Fatalf("server-side asset key missing: %+v", assets[0])
	}
	assets, total, err = repository.ListOwnedInputAssets(
		context.Background(), "native-user", 2, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(assets) != 1 || assets[0].ID != "legacy-upload" {
		t.Fatalf("page2 assets=%+v total=%d", assets, total)
	}
	assets, total, err = repository.ListOwnedInputAssets(
		context.Background(), "other-user", 1, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(assets) != 0 {
		t.Fatalf("other owner assets=%+v total=%d", assets, total)
	}
	assets, total, err = repository.ListOwnedInputAssets(
		context.Background(), "native-user", 1, 101,
	)
	if err != nil || total != 2 || len(assets) != 2 {
		t.Fatalf("schema-sized page assets=%+v total=%d err=%v", assets, total, err)
	}
	maxInt := int(^uint(0) >> 1)
	if _, _, err := repository.ListOwnedInputAssets(
		context.Background(), "native-user", maxInt, 2,
	); err == nil {
		t.Fatal("overflowing page offset was accepted")
	}
}

func seedAdministrationUserAssets(
	t *testing.T,
	path string,
	repository *sqlite.Repository,
) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nativeAt := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	legacyAt := nativeAt.Add(-time.Hour)
	for _, statement := range []string{
		`ALTER TABLE asset_registry ADD COLUMN assetType TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE asset_registry ADD COLUMN storedFilename TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE asset_registry ADD COLUMN uploaderUserId TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE asset_registry ADD COLUMN createdAt`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO generation_input_assets (
			id, owner_id, storage_key, original_filename, asset_type,
			media_type, size_bytes, sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"native-upload",
		"native-user",
		"uploads/native.png",
		"native.png",
		"image",
		"image/png",
		1024,
		"native-sha",
		nativeAt.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, storagePath, uploaderUserId, createdAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-upload",
		"image",
		"legacy.png",
		"legacy-stored.png",
		"image/png",
		2048,
		"legacy/legacy-stored.png",
		"native-user",
		legacyAt.UnixMilli(),
		"native-upload",
		"image",
		"duplicate.png",
		"duplicate.png",
		"image/png",
		4096,
		"legacy/duplicate.png",
		"native-user",
		legacyAt.UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			userId TEXT NOT NULL,
			userName TEXT NOT NULL DEFAULT '',
			resourceType TEXT NOT NULL DEFAULT '',
			resourceId TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '{}',
			ipAddress TEXT NOT NULL DEFAULT '',
			userAgent TEXT NOT NULL DEFAULT '',
			createdAt
		)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO audit_logs (
			id, action, userId, userName, resourceType, resourceId,
			detail, createdAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-download",
		"download_asset",
		"native-user",
		"Native Alice",
		"task_asset",
		"native-task",
		`{"source":"legacy"}`,
		legacyAt.UnixMilli(),
		"legacy-login",
		"login",
		"native-user",
		"Native Alice",
		"session",
		"session-1",
		`{}`,
		legacyAt.UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	var seededAction string
	var seededUserID string
	var seededCreatedAt string
	if err := db.QueryRow(`
		SELECT action, userId, CAST(createdAt AS TEXT)
		FROM audit_logs
		WHERE id = 'legacy-download'
	`).Scan(&seededAction, &seededUserID, &seededCreatedAt); err != nil {
		t.Fatal(err)
	}
	if seededAction != "download_asset" ||
		seededUserID != "native-user" {
		t.Fatalf(
			"seeded legacy audit action=%q user=%q created=%q",
			seededAction,
			seededUserID,
			seededCreatedAt,
		)
	}
	if err := repository.RecordAudit(
		context.Background(),
		administration.AuditEvent{
			ID:           "go-access",
			Action:       "access_asset",
			ActorID:      "native-user",
			ActorName:    "Native Alice",
			ResourceType: "input_asset",
			ResourceID:   "native-upload",
			Detail:       map[string]any{"source": "go"},
			CreatedAt:    nativeAt.Add(15 * time.Minute),
		},
	); err != nil {
		t.Fatal(err)
	}
}
