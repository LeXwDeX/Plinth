package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	_ "modernc.org/sqlite"
)

func TestAdministrationGenerationQueryCombinesLegacyAndNativeRecords(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "generations.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)

	page, total, err := repository.QueryAdministrationGenerations(
		context.Background(),
		administration.GenerationQuery{Page: 1, PageSize: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("combined total=%d records=%#v", total, page)
	}
	if page[0].TaskID != "native-task" ||
		page[0].UserName != "Native Alice" ||
		page[0].EstimatedCredits != 7 ||
		len(page[0].Assets) != 1 ||
		page[0].Assets[0].ID != "native-image" {
		t.Fatalf("native projection = %#v", page[0])
	}
	if page[1].TaskID != "legacy-task" ||
		page[1].UserName != "Legacy Bob" ||
		page[1].ThumbnailURL != "https://assets.test/thumb.png" ||
		len(page[1].ResultURLs) != 1 ||
		page[1].ResultURLs[0] != "https://assets.test/image.png" {
		t.Fatalf("legacy projection = %#v", page[1])
	}
	// The Offering a native quote was frozen for travels with the record, so
	// fee item labels can be read through its current declaration.
	if page[0].Offering != frozenAdministrationOffering ||
		page[1].Offering != (administration.GenerationOffering{}) {
		t.Fatalf("frozen offerings = %#v / %#v", page[0].Offering, page[1].Offering)
	}

	from := time.Date(2026, time.July, 24, 11, 30, 0, 0, time.UTC)
	filtered, total, err := repository.QueryAdministrationGenerations(
		context.Background(),
		administration.GenerationQuery{
			Page:     1,
			PageSize: 10,
			UserID:   "native-user",
			TaskType: "text-to-image",
			Status:   "SUCCEEDED",
			From:     &from,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(filtered) != 1 ||
		filtered[0].TaskID != "native-task" {
		t.Fatalf("filtered total=%d records=%#v", total, filtered)
	}
}

func TestAdministrationGenerationDetailSupportsNativeAndLegacyCache(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "generation-detail.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)
	ctx := context.Background()

	native, found, err := repository.GetAdministrationGeneration(
		ctx,
		"native-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || native.TaskCache == nil ||
		native.TaskCache.ProviderID != "openai" ||
		string(native.TaskCache.Data) != `{"remote":"native"}` ||
		len(native.Ledger.Assets) != 1 ||
		native.Ledger.Offering != frozenAdministrationOffering {
		t.Fatalf("native detail found=%v detail=%#v", found, native)
	}

	legacy, found, err := repository.GetAdministrationGeneration(
		ctx,
		"legacy-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || legacy.TaskCache == nil ||
		legacy.TaskCache.ProviderID != "meshy" ||
		string(legacy.TaskCache.Data) !=
			`{"remote":"legacy","thumbnail_url":"https://assets.test/thumb.png"}` ||
		legacy.Ledger.Offering != (administration.GenerationOffering{}) {
		t.Fatalf("legacy detail found=%v detail=%#v", found, legacy)
	}

	_, found, err = repository.GetAdministrationGeneration(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("missing generation was reported as found")
	}
}

var frozenAdministrationOffering = administration.GenerationOffering{
	ID: "openai-image-v1", ProviderID: "openai", ModelID: "gpt-image-1", Capability: "text-to-image",
}

func seedAdministrationGenerations(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nativeAt := time.Date(
		2026,
		time.July,
		24,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	legacyAt := nativeAt.Add(-time.Hour)
	offering, err := json.Marshal(map[string]any{
		"id":                "openai-image-v1",
		"provider_id":       "openai",
		"model_id":          "gpt-image-1",
		"capability":        "text-to-image",
		"estimated_credits": 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO authentication_users (
				id, username, nickname, name, email, roles_json,
				is_admin, is_active, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, '["user"]', 0, 1, ?, ?)`,
			[]any{
				"native-user",
				"native-alice",
				"Native Alice",
				"",
				"alice@example.test",
				nativeAt.Format(time.RFC3339Nano),
				nativeAt.Format(time.RFC3339Nano),
			},
		},
		{
			`INSERT INTO generation_tasks (
				id, owner_id, capability, offering_json, input_json,
				request_hash, state, progress, current_attempt_id,
				version, created_at, updated_at, finished_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 100, ?, 1, ?, ?, ?)`,
			[]any{
				"native-task",
				"native-user",
				"text-to-image",
				string(offering),
				`{"prompt":"native cat"}`,
				"native-hash",
				"SUCCEEDED",
				"native-attempt",
				nativeAt.Format(time.RFC3339Nano),
				nativeAt.Format(time.RFC3339Nano),
				nativeAt.Format(time.RFC3339Nano),
			},
		},
		{
			`INSERT INTO generation_attempts (
				id, task_id, ordinal, offering_json, state, submission_key,
				opaque_json, started_at, updated_at, finished_at
			) VALUES (?, ?, 1, ?, 'UPSTREAM_SUCCEEDED', ?, ?, ?, ?, ?)`,
			[]any{
				"native-attempt",
				"native-task",
				string(offering),
				"native-submission",
				`{"remote":"native"}`,
				nativeAt.Format(time.RFC3339Nano),
				nativeAt.Format(time.RFC3339Nano),
				nativeAt.Format(time.RFC3339Nano),
			},
		},
		{
			`INSERT INTO generation_assets (
				id, task_id, attempt_id, role, required, ordinal,
				storage_key, media_type, size_bytes, sha256, created_at
			) VALUES (?, ?, ?, 'image', 1, 0, ?, 'image/png', 3, ?, ?)`,
			[]any{
				"native-image",
				"native-task",
				"native-attempt",
				"native/task/image.png",
				"abc",
				nativeAt.Format(time.RFC3339Nano),
			},
		},
		{
			`CREATE TABLE usage_ledger (
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
			)`,
			nil,
		},
		{
			`INSERT INTO usage_ledger (
				id, userId, userName, userEmail, taskType, taskId,
				providerId, estimatedCredits, taskStatus, params,
				resultUrls, createdAt, finishedAt
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-ledger",
				"legacy-user",
				"Legacy Bob",
				"bob@example.test",
				"image-to-3d",
				"legacy-task",
				"meshy",
				20,
				"SUCCEEDED",
				`{"prompt":"legacy dog"}`,
				`["https://assets.test/image.png"]`,
				legacyAt.UnixMilli(),
				legacyAt.UnixMilli(),
			},
		},
		{
			`INSERT INTO usage_ledger (
				id, userId, userName, userEmail, taskType, taskId,
				providerId, estimatedCredits, taskStatus, params,
				resultUrls, createdAt, finishedAt
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"duplicate-native-ledger",
				"native-user",
				"Duplicate",
				"",
				"text-to-image",
				"native-task",
				"openai",
				7,
				"SUCCEEDED",
				`{}`,
				`[]`,
				nativeAt.UnixMilli(),
				nativeAt.UnixMilli(),
			},
		},
		{
			`CREATE TABLE task_cache (
				taskId TEXT PRIMARY KEY,
				taskType TEXT NOT NULL,
				status TEXT NOT NULL,
				providerId TEXT NOT NULL,
				data TEXT NOT NULL,
				cachedAt,
				updatedAt
			)`,
			nil,
		},
		{
			`INSERT INTO task_cache (
				taskId, taskType, status, providerId, data, cachedAt, updatedAt
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-task",
				"image-to-3d",
				"SUCCEEDED",
				"meshy",
				`{"remote":"legacy","thumbnail_url":"https://assets.test/thumb.png"}`,
				legacyAt.UnixMilli(),
				legacyAt.UnixMilli(),
			},
		},
		{
			`CREATE TABLE asset_registry (
				id TEXT PRIMARY KEY,
				originalFilename TEXT,
				storagePath TEXT,
				mimeType TEXT,
				fileSize INTEGER
			)`,
			nil,
		},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed administration generations: %v", err)
		}
	}
	if _, err := db.Exec(
		`UPDATE usage_ledger
		 SET resultUrls = ?, params = ?
		 WHERE taskId = 'legacy-task'`,
		`["https://assets.test/image.png"]`,
		`{"prompt":"legacy dog"}`,
	); err != nil {
		t.Fatal(err)
	}
}
