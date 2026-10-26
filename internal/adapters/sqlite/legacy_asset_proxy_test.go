package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyAssetProxyRepositoryReadsAndUpdatesOnlyTaskData(
	t *testing.T,
) {
	repository, err := Open(filepath.Join(t.TempDir(), "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if _, err := repository.db.Exec(`
		CREATE TABLE task_cache (
			taskId TEXT PRIMARY KEY,
			taskType TEXT NOT NULL,
			status TEXT NOT NULL,
			providerId TEXT NOT NULL,
			data TEXT NOT NULL,
			cachedAt DATETIME NOT NULL,
			updatedAt DATETIME NOT NULL
		);
		INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		) VALUES (
			'task-1', 'image-to-3d', 'SUCCEEDED', 'meshy',
			'{"url":"old"}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatal(err)
	}

	record, found, err := repository.GetLegacyAssetTask(
		context.Background(),
		"task-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		record.TaskID != "task-1" ||
		record.TaskType != "image-to-3d" ||
		record.Status != "SUCCEEDED" ||
		record.ProviderID != "meshy" ||
		record.Data != `{"url":"old"}` {
		t.Fatalf("legacy task = %#v, found=%v", record, found)
	}
	if err := repository.UpdateLegacyAssetTaskData(
		context.Background(),
		record,
		`{"url":"fresh"}`,
	); err != nil {
		t.Fatal(err)
	}
	record, found, err = repository.GetLegacyAssetTask(
		context.Background(),
		"task-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		record.Status != "SUCCEEDED" ||
		record.TaskType != "image-to-3d" ||
		record.ProviderID != "meshy" ||
		record.Data != `{"url":"fresh"}` {
		t.Fatalf("updated legacy task = %#v, found=%v", record, found)
	}
}

func TestLegacyAssetProxyRepositoryToleratesMissingLegacyTable(
	t *testing.T,
) {
	repository, err := Open(filepath.Join(t.TempDir(), "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	_, found, err := repository.GetLegacyAssetTask(
		context.Background(),
		"task-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("found = true")
	}
}

func TestLegacyAssetProxyRepositoryFallsBackToNativeCurrentAttempt(
	t *testing.T,
) {
	repository, err := Open(filepath.Join(t.TempDir(), "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	offering := generation.Offering{
		ID:         "legacy//image-to-3d",
		Capability: generation.ImageTo3D,
	}
	offeringJSON, err := json.Marshal(offering)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`
		INSERT INTO generation_tasks (
			id, owner_id, capability, offering_json, input_json,
			request_hash, idempotency_key, state, progress,
			current_attempt_id, materialize_failures, version,
			created_at, updated_at, finished_at
		) VALUES (
			'task-native', 'owner-1', 'image-to-3d', ?, '{}',
			'', '', 'SUCCEEDED', 100,
			'attempt-native', 0, 7,
			'2026-07-01T00:00:00Z', '2026-07-02T00:00:00Z',
			'2026-07-02T00:00:00Z'
		);
		INSERT INTO generation_attempts (
			id, task_id, ordinal, offering_json, state, submission_key,
			upstream_provider_id, upstream_job_id, opaque_json,
			started_at, updated_at, finished_at
		) VALUES (
			'attempt-native', 'task-native', 1, ?,
			'UPSTREAM_SUCCEEDED', 'legacy-ledger:1',
			'meshy', 'remote-native',
			'{"thumbnail_url":"https://assets.test/old.png"}',
			'2026-07-01T00:00:00Z', '2026-07-02T00:00:00Z',
			'2026-07-02T00:00:00Z'
		)`,
		string(offeringJSON),
		string(offeringJSON),
	); err != nil {
		t.Fatal(err)
	}

	record, found, err := repository.GetLegacyAssetTask(
		context.Background(),
		"task-native",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		record.TaskID != "task-native" ||
		record.TaskType != "image-to-3d" ||
		record.Status != "SUCCEEDED" ||
		record.ProviderID != "meshy" ||
		record.AttemptID != "attempt-native" ||
		record.Offering.ID != offering.ID ||
		record.UpstreamJob == nil ||
		record.UpstreamJob.RemoteID != "remote-native" ||
		record.Data != `{"thumbnail_url":"https://assets.test/old.png"}` {
		t.Fatalf("native legacy task = %#v, found=%v", record, found)
	}
	if err := repository.UpdateLegacyAssetTaskData(
		context.Background(),
		record,
		`{"thumbnail_url":"https://assets.test/fresh.png"}`,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateLegacyAssetTaskData(context.Background(), record, `{"lost":"concurrent data"}`); !errors.Is(err, generation.RepositoryConflict) {
		t.Fatalf("stale snapshot update = %v", err)
	}
	record, found, err = repository.GetLegacyAssetTask(
		context.Background(),
		"task-native",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		record.Data != `{"thumbnail_url":"https://assets.test/fresh.png"}` {
		t.Fatalf("updated native legacy task = %#v, found=%v", record, found)
	}
	if err := repository.UpdateLegacyAssetTaskData(context.Background(), record, record.Data); err != nil {
		t.Fatal(err)
	}
	var taskVersion int64
	if err := repository.db.QueryRow(
		`SELECT version FROM generation_tasks WHERE id = 'task-native'`,
	).Scan(&taskVersion); err != nil {
		t.Fatal(err)
	}
	if taskVersion != 8 {
		t.Fatalf("task version = %d, want 8", taskVersion)
	}
}
