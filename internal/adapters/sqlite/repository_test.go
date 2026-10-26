package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestEmbeddedMigrationsAreIdempotentAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "art-ai.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	asset := generation.InputAsset{
		ID:               "asset-metadata",
		OwnerID:          "owner-a",
		StorageKey:       "sha256/asset",
		OriginalFilename: "scene.glb",
		AssetType:        "model",
		MediaType:        "model/gltf-binary",
		Size:             42,
		SHA256:           "deadbeef",
		CreatedAt:        createdAt,
	}
	if err := second.CreateInputAsset(context.Background(), asset); err != nil {
		t.Fatal(err)
	}
	task := generation.Task{
		ID:         "task-tombstone",
		OwnerID:    "owner-a",
		Capability: generation.TextToImage,
		Offering: generation.Offering{
			ID:         "test/offering",
			ProviderID: "test",
			ModelID:    "test",
			Capability: generation.TextToImage,
		},
		Input:        json.RawMessage(`{"prompt":"temporary"}`),
		RequestHash:  "request-hash",
		State:        generation.TaskQueued,
		Progress:     0,
		NextActionAt: createdAt,
		Version:      1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
		Assets:       []generation.Asset{},
	}
	task = approveCreateFixture(t, second, task)
	if _, replayed, err := second.Create(
		context.Background(),
		task,
		generation.Event{
			TaskID:    task.ID,
			Type:      "task.submitted",
			Data:      json.RawMessage(`{}`),
			CreatedAt: createdAt,
		},
		0,
	); err != nil {
		t.Fatal(err)
	} else if replayed {
		t.Fatal("new task was unexpectedly replayed")
	}
	deletedAt := createdAt.Add(time.Minute)
	if err := second.Tombstone(context.Background(), generation.Tombstone{
		TaskID:          task.ID,
		ActorID:         "admin-user",
		ExpectedVersion: task.Version,
		DeletedAt:       deletedAt,
		Event: generation.Event{
			TaskID:    task.ID,
			Type:      "task.deleted",
			Data:      json.RawMessage(`{"actor_id":"admin-user"}`),
			CreatedAt: deletedAt,
		},
	}); err != nil {
		t.Fatal(err)
	}
	variantTask := generation.Task{
		ID:         "task-variant",
		OwnerID:    "owner-a",
		Capability: generation.Animation,
		Offering: generation.Offering{
			ID:         "test/animation",
			ProviderID: "test",
			ModelID:    "test",
			Capability: generation.Animation,
		},
		Input:        json.RawMessage(`{"action_id":1}`),
		RequestHash:  "variant-request-hash",
		State:        generation.TaskQueued,
		NextActionAt: createdAt,
		Version:      1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
		Assets:       []generation.Asset{},
	}
	variantTask = approveCreateFixture(t, second, variantTask)
	if _, replayed, err := second.Create(
		context.Background(),
		variantTask,
		generation.Event{
			TaskID:    variantTask.ID,
			Type:      "task.submitted",
			Data:      json.RawMessage(`{}`),
			CreatedAt: createdAt,
		},
		0,
	); err != nil {
		t.Fatal(err)
	} else if replayed {
		t.Fatal("variant task was unexpectedly replayed")
	}
	attempt := generation.Attempt{
		ID:            "attempt-variant",
		TaskID:        variantTask.ID,
		Ordinal:       1,
		Offering:      variantTask.Offering,
		State:         generation.AttemptUpstreamSucceeded,
		SubmissionKey: "variant-submission",
		Opaque:        json.RawMessage(`[{"identifier":"subtask-1"}]`),
		StartedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
	variantTask.State = generation.TaskSucceeded
	variantTask.Progress = 100
	variantTask.CurrentAttemptID = attempt.ID
	variantTask.Version++
	if err := second.Commit(context.Background(), generation.Commit{
		ExpectedVersion: 1,
		Task:            variantTask,
		Attempt:         &attempt,
		NewAssets: []generation.Asset{{
			ID:         "asset-animation-glb",
			TaskID:     variantTask.ID,
			AttemptID:  attempt.ID,
			Role:       "model",
			Variant:    "animation_glb_url",
			Required:   true,
			Ordinal:    1,
			StorageKey: "sha256/animation",
			MediaType:  "model/gltf-binary",
			Size:       84,
			SHA256:     "cafebabe",
			CreatedAt:  createdAt,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	stored, err := third.GetInputAsset(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OriginalFilename != asset.OriginalFilename ||
		stored.AssetType != asset.AssetType ||
		!stored.CreatedAt.Equal(createdAt) {
		t.Fatalf("stored asset = %#v", stored)
	}
	if _, err := third.Get(context.Background(), task.OwnerID, task.ID); !errors.Is(
		err,
		generation.RepositoryNotFound,
	) {
		t.Fatalf("get tombstoned task error = %#v", err)
	}
	storedVariantTask, err := third.Get(
		context.Background(),
		variantTask.OwnerID,
		variantTask.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedVariantTask.Assets) != 1 ||
		storedVariantTask.Assets[0].Variant != "animation_glb_url" {
		t.Fatalf("stored variant assets = %#v", storedVariantTask.Assets)
	}
	if string(storedVariantTask.ProviderData) != string(attempt.Opaque) {
		t.Fatalf(
			"stored provider data = %s, want %s",
			storedVariantTask.ProviderData,
			attempt.Opaque,
		)
	}
	page, err := third.List(
		context.Background(),
		generation.OwnTasks(task.OwnerID),
		generation.ListQuery{Limit: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 ||
		len(page.Tasks) != 1 ||
		page.Tasks[0].ID != variantTask.ID {
		t.Fatalf("tombstoned task visibility is incorrect: %#v", page)
	}

	auditDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer auditDB.Close()
	var actorID string
	var eventType string
	err = auditDB.QueryRow(`
		SELECT tombstone.actor_id, event.event_type
		FROM generation_task_tombstones tombstone
		JOIN generation_task_events event ON event.task_id = tombstone.task_id
		WHERE tombstone.task_id = ? AND event.event_type = ?`,
		task.ID,
		"task.deleted",
	).Scan(&actorID, &eventType)
	if err != nil {
		t.Fatal(err)
	}
	if actorID != "admin-user" || eventType != "task.deleted" {
		t.Fatalf("actor = %q, event = %q", actorID, eventType)
	}
}

func TestGeneratedOutputIsInputOnlyAfterOwnedTaskSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assets.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, fixture := range []struct{ taskID, state, owner string }{
		{"ready", "SUCCEEDED", "owner-a"},
		{"partial", "MATERIALIZING", "owner-a"},
		{"foreign", "SUCCEEDED", "owner-b"},
	} {
		_, err = database.Exec(`INSERT INTO generation_tasks
			(id, owner_id, capability, offering_json, input_json, request_hash,
			 state, version, created_at, updated_at)
			 VALUES (?, ?, 'text-to-3d', '{}', '{}', 'hash', ?, 1,
			 '2026-09-23T00:00:00Z', '2026-09-23T00:00:00Z')`,
			fixture.taskID, fixture.owner, fixture.state)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec(`INSERT INTO generation_attempts
			(id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
			 VALUES (?, ?, 1, '{}', 'SUCCEEDED', ?,
			 '2026-09-23T00:00:00Z', '2026-09-23T00:00:00Z')`,
			"attempt-"+fixture.taskID, fixture.taskID, "key-"+fixture.taskID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec(`INSERT INTO generation_assets
			(id, task_id, attempt_id, role, required, ordinal, storage_key,
			 media_type, size_bytes, sha256, created_at)
			 VALUES (?, ?, ?, 'model', 1, 0, ?, 'model/gltf-binary', 8, 'digest',
			 '2026-09-23T00:00:00Z')`,
			"asset-"+fixture.taskID, fixture.taskID, "attempt-"+fixture.taskID,
			"sha256/"+fixture.taskID)
		if err != nil {
			t.Fatal(err)
		}
	}
	assets, err := repository.LoadInputAssets(context.Background(), "owner-a", []generation.AssetID{
		"asset-ready", "asset-partial", "asset-foreign",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets["asset-ready"].StorageKey != "sha256/ready" {
		t.Fatalf("only owned completed output may be reused: %#v", assets)
	}
	_, err = database.Exec(`INSERT INTO generation_task_tombstones (task_id, actor_id, deleted_at)
		VALUES ('ready', 'admin-user', '2026-09-23T00:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	assets, err = repository.LoadInputAssets(context.Background(), "owner-a", []generation.AssetID{"asset-ready"})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 0 {
		t.Fatalf("deleted task output remains reusable: %#v", assets)
	}
}
