package sqlite

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

//go:embed migrations/0033_scene_objects.sql
var sceneMigrationForTest string

func sceneTestRepository(t *testing.T) (*Repository, *SceneRepository) {
	t.Helper()
	repo, err := Open(filepath.Join(t.TempDir(), "scene.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	// This new migration is registered by the app integration change. Keep this
	// focused package test independently runnable until that registration lands.
	if MigrationHead() < 33 {
		if _, err = repo.db.Exec(sceneMigrationForTest); err != nil {
			t.Fatal(err)
		}
	}
	sr, err := repo.SceneRepository(SQLiteSceneEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, pair := range [][2]string{{"owner-a", "session-a"}, {"owner-b", "session-b"}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, pair[1], pair[0], "name", "title", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"owner-a", "asset-a"}, {"owner-b", "asset-b"}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?)`, pair[1], pair[0], "key", "image/png", 1, "digest", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range [][3]string{{"owner-a", "session-a", "prepared-1"}, {"owner-b", "session-b", "prepared-b"}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_prepared_inputs(id,owner_id,session_id,input_hash,payload_json,created_at) VALUES (?,?,?,?,?,?)`, item[2], item[0], item[1], "digest", "{}", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	return repo, sr
}

func TestSucceededModelCandidateSurvivesMoveAndRestart(t *testing.T) {
	repo, sr := sceneTestRepository(t)
	ctx := context.Background()
	input := sceneCreate("asset-a")
	input.Object.Anchor = scene.Anchor{}
	if _, err := sr.Create(ctx, input); err != nil {
		t.Fatal(err)
	}
	prepared := generation.PreparedInput{ID: "prepared-source", OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ImageTo3D, OfferingID: "model-3d", OfferingRevision: "rev-a", SchemaVersion: "v1",
		NormalizedInput: json.RawMessage(`{"reference_assets":["asset-a"]}`), InputHash: "frozen-hash",
		SourceSceneObjectID: "object-a", SourceSceneRevision: 1, SourceSceneRepresentationID: "rep-1"}
	payload, _ := json.Marshal(prepared)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_prepared_inputs(id,owner_id,session_id,input_hash,payload_json,created_at) VALUES (?,?,?,?,?,?)`,
		prepared.ID, prepared.OwnerID, prepared.SessionID, prepared.InputHash, string(payload), "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, "task-model", "owner-a", "session-a", "image-to-3d", "{}", string(prepared.NormalizedInput), "hash", "one-call", "SUCCEEDED", 1, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, "attempt-model", "task-model", 1, "{}", "SUCCEEDED", "submit", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "model-asset", "task-model", "attempt-model", "model", 1, 0, "model-key", "model/gltf-binary", 32, "model-digest", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO execution_grants(request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,task_id,source,request_version,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "grant-model", "owner-a", "session-a", "model-3d", "rev-a", "prepared-source", "frozen-hash", "one-call", "one-operation", "bound", "task-model", "human_browser", 1, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 1},
		scene.Rect{X: 20, Width: 100, Height: 80}, scene.Anchor{}); err != nil {
		t.Fatal(err)
	}
	if _, err := sr.RegisterCandidate(ctx, scene.Candidate{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a",
		Representation: scene.Representation{ID: "rep-alt", ObjectID: "object-a", Kind: scene.Bitmap, AssetID: "asset-a",
			SourceRepresentationID: "rep-1", MetadataSchemaRevision: "v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sr.ApplyCandidate(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 2}, "rep-alt"); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := repo.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resumed, err := reopened.SceneRepository(SQLiteSceneEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	// A terminal task whose source object was removed must not permanently
	// block later successful outputs at the head of a bounded scan.
	poison := prepared
	poison.ID, poison.InputHash, poison.SourceSceneObjectID = "prepared-poison", "poison-hash", "deleted-object"
	poisonPayload, _ := json.Marshal(poison)
	if _, err := reopened.db.ExecContext(ctx, `INSERT INTO generation_prepared_inputs(id,owner_id,session_id,input_hash,payload_json,created_at) VALUES (?,?,?,?,?,?)`,
		poison.ID, poison.OwnerID, poison.SessionID, poison.InputHash, string(poisonPayload), "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, "task-poison", "owner-a", "session-a", "image-to-3d", "{}", string(poison.NormalizedInput), "poison", "poison-call", "SUCCEEDED", 1, "2025-01-01T00:00:00Z", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		"attempt-poison", "task-poison", 1, "{}", "SUCCEEDED", "poison-submit", "2025-01-01T00:00:00Z", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "model-poison", "task-poison", "attempt-poison", "model", 1, 0, "model-key", "model/gltf-binary", 32, "model-digest", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx, `INSERT INTO execution_grants(request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,task_id,source,request_version,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "grant-poison", "owner-a", "session-a", "model-3d", "rev-a", poison.ID, poison.InputHash, "poison-call", "poison-operation", "bound", "task-poison", "human_browser", 1, "2025-01-01T00:00:00Z", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if count, err := resumed.ReconcileTaskCandidates(ctx, 1); count != 0 || !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("poisoned first page count=%d err=%v", count, err)
	}
	count, err := resumed.ReconcileTaskCandidates(ctx, 1)
	if err != nil || count != 1 {
		t.Fatalf("restart reconciliation count=%d err=%v", count, err)
	}
	obj, err := resumed.Get(ctx, "owner-a", "session-a", "object-a")
	if err != nil {
		t.Fatal(err)
	}
	if obj.ActiveRepresentationID != "rep-alt" || obj.Rect.X != 20 || obj.Revision != 3 {
		t.Fatalf("candidate overwrote moved object: %+v", obj)
	}
	items, err := resumed.ListRepresentations(ctx, "owner-a", "session-a", "object-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[2].ID != scene.TaskCandidateID("task-model", "model-asset") ||
		items[2].SourceRepresentationID != "rep-1" || items[2].PreparedInputID != "prepared-source" {
		t.Fatalf("candidate lineage=%+v", items)
	}
	snap, err := resumed.Snapshot(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.ReconcileTaskCandidates(ctx, 20); err != nil {
		t.Fatal(err)
	}
	again, err := resumed.Snapshot(ctx, "owner-a", "session-a")
	if err != nil || again.Revision != snap.Revision {
		t.Fatalf("replay mutated scene revision: %+v %v", again, err)
	}
	if _, err := resumed.GetRepresentation(ctx, "owner-b", "session-b", "object-a", items[2].ID); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross owner candidate read: %v", err)
	}
}

func sceneCreate(asset string) scene.Create {
	return scene.Create{Object: scene.Object{ID: "object-a", OwnerID: "owner-a", SessionID: "session-a", Rect: scene.Rect{Width: 100, Height: 80}, Anchor: scene.Anchor{X: .5, Y: .5}}, Initial: scene.Representation{ID: "rep-1", ObjectID: "object-a", Kind: scene.Bitmap, AssetID: asset, MetadataSchemaRevision: "v1"}}
}

func assertSceneRevision(t *testing.T, repo *Repository, want int64) {
	t.Helper()
	var got int64
	if err := repo.db.QueryRow(`SELECT revision FROM scene_revisions WHERE session_id='session-a' AND owner_id='owner-a'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("scene revision=%d, want %d", got, want)
	}
}

func TestSceneAdmissionAndCandidateLineage(t *testing.T) {
	repo, sr := sceneTestRepository(t)
	ctx := context.Background()
	bad := sceneCreate("asset-b")
	if _, err := sr.Create(ctx, bad); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross-owner asset: %v", err)
	}
	created, err := sr.Create(ctx, sceneCreate("asset-a"))
	if err != nil {
		t.Fatal(err)
	}
	assertSceneRevision(t, repo, 1)
	if created.ID == created.ActiveRepresentationID || created.Revision != 1 {
		t.Fatalf("identity or revision: %+v", created)
	}
	if _, err = sr.Get(ctx, "owner-b", "session-b", created.ID); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross-owner object: %v", err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, "legacy-task", "owner-a", "text-to-image", "{}", "{}", "hash", "SUCCEEDED", 1, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	legacy := scene.Candidate{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", Representation: scene.Representation{ID: "rep-legacy", ObjectID: "object-a", Kind: scene.Bitmap, AssetID: "asset-a", SourceTaskID: "legacy-task", SourceRepresentationID: "rep-1", MetadataSchemaRevision: "v1"}}
	if _, err = sr.RegisterCandidate(ctx, legacy); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("legacy task acquired session provenance: %v", err)
	}
	candidate := legacy
	candidate.Representation.ID = "rep-2"
	candidate.Representation.SourceTaskID = ""
	candidate.Representation.PreparedInputID = "prepared-1"
	candidate.Representation.PreparedInputID = "prepared-b"
	if _, err = sr.RegisterCandidate(ctx, candidate); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross-owner prepared input accepted: %v", err)
	}
	candidate.Representation.PreparedInputID = "missing"
	if _, err = sr.RegisterCandidate(ctx, candidate); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("missing prepared input accepted: %v", err)
	}
	candidate.Representation.PreparedInputID = "prepared-1"
	if _, err = sr.RegisterCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	assertSceneRevision(t, repo, 2)
	moved, err := sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 1}, scene.Rect{X: 5, Width: 100, Height: 80}, scene.Anchor{X: .5, Y: .5})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Revision != 2 || moved.ActiveRepresentationID != "rep-1" {
		t.Fatalf("move replaced representation: %+v", moved)
	}
	assertSceneRevision(t, repo, 3)
	if _, err = sr.ApplyCandidate(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 1}, "rep-2"); !errors.Is(err, scene.ErrConflict) {
		t.Fatalf("stale candidate applied: %v", err)
	}
	reps, err := sr.ListRepresentations(ctx, "owner-a", "session-a", "object-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 || reps[1].SourceRepresentationID != "rep-1" || reps[1].PreparedInputID != "prepared-1" {
		t.Fatalf("lineage lost: %+v", reps)
	}
	if _, err = repo.db.ExecContext(ctx, `UPDATE scene_representations SET asset_id='asset-b' WHERE id='rep-2'`); err == nil {
		t.Fatal("immutable representation updated")
	}
	applied, err := sr.ApplyCandidate(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 2}, "rep-2")
	if err != nil {
		t.Fatal(err)
	}
	if applied.Revision != 3 || applied.ActiveRepresentationID != "rep-2" {
		t.Fatalf("apply: %+v", applied)
	}
	assertSceneRevision(t, repo, 4)
	deleted, err := sr.Delete(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedAt == nil || deleted.Revision != 4 {
		t.Fatalf("tombstone: %+v", deleted)
	}
	assertSceneRevision(t, repo, 5)
	if _, err = sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 4}, scene.Rect{Width: 10, Height: 10}, scene.Anchor{}); !errors.Is(err, scene.ErrConflict) {
		t.Fatalf("moved tombstone: %v", err)
	}
	candidate.Representation.ID = "rep-after-delete"
	if _, err = sr.RegisterCandidate(ctx, candidate); !errors.Is(err, scene.ErrConflict) {
		t.Fatalf("registered candidate on tombstone: %v", err)
	}
	assertSceneRevision(t, repo, 5)
}

// RFC3339Nano text has a variable-width fraction, so its byte order is not
// time order. Historical rows keep that encoding and must still list by instant.
func TestSceneRepresentationsListByInstantAcrossFractionWidths(t *testing.T) {
	repo, sr := sceneTestRepository(t)
	ctx := context.Background()
	created, err := sr.Create(ctx, sceneCreate("asset-a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{
		{"rep-z-micro", "2030-01-01T00:00:00.642662Z"},
		{"rep-y-milli", "2030-01-01T00:00:00.642Z"},
		{"rep-tie-b", "2030-01-01T00:00:01Z"},
		{"rep-x-whole", "2030-01-01T00:00:00Z"},
		{"rep-tie-a", "2030-01-01T00:00:01Z"},
	} {
		if _, err = repo.db.ExecContext(ctx, `INSERT INTO scene_representations (id,object_id,kind,asset_id,metadata_schema_revision,created_at) VALUES (?,?,?,?,?,?)`, row[0], created.ID, scene.Bitmap, "asset-a", "v1", row[1]); err != nil {
			t.Fatal(err)
		}
	}
	reps, err := sr.ListRepresentations(ctx, "owner-a", "session-a", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, rep := range reps {
		got = append(got, rep.ID)
	}
	want := []string{created.ActiveRepresentationID, "rep-x-whole", "rep-y-milli", "rep-z-micro", "rep-tie-a", "rep-tie-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("representation order=%v, want %v", got, want)
	}
}

func TestSceneSourceVerifierAndCandidateCAS(t *testing.T) {
	_, sr := sceneTestRepository(t)
	ctx := context.Background()
	created, err := sr.Create(ctx, sceneCreate("asset-a"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := scene.New(sr)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyPreparedSceneObject(ctx, "owner-a", "session-a", created.ID, 1, []byte(`{"reference_assets":["asset-a"]}`)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"reference_assets":["asset-b"]}`, `{"reference_assets":["asset-a","asset-b"]}`, `{}`} {
		if err := service.VerifyPreparedSceneObject(ctx, "owner-a", "session-a", created.ID, 1, []byte(input)); !errors.Is(err, scene.ErrConflict) {
			t.Fatalf("unbound source accepted %s: %v", input, err)
		}
	}
	if err := service.VerifyPreparedSceneObject(ctx, "owner-b", "session-b", created.ID, 1, []byte(`{"reference_assets":["asset-a"]}`)); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross owner source: %v", err)
	}
	if _, err := sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: created.ID, ExpectedRevision: 1}, scene.Rect{X: 2, Width: 100, Height: 80}, scene.Anchor{X: .5, Y: .5}); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyPreparedSceneObject(ctx, "owner-a", "session-a", created.ID, 1, []byte(`{"reference_assets":["asset-a"]}`)); !errors.Is(err, scene.ErrConflict) {
		t.Fatalf("stale scene source: %v", err)
	}
	_, err = sr.RegisterCandidate(ctx, scene.Candidate{OwnerID: "owner-a", SessionID: "session-a", ObjectID: created.ID,
		ExpectedObjectRevision: 1, ExpectedSourceRepresentationID: "rep-1",
		Representation: scene.Representation{ID: "candidate-stale", ObjectID: created.ID, Kind: scene.Model3D,
			AssetID: "asset-a", SourceRepresentationID: "rep-1", MetadataSchemaRevision: "v1"}})
	if !errors.Is(err, scene.ErrConflict) {
		t.Fatalf("candidate ignored concurrent move: %v", err)
	}
}

func TestSceneSQLiteConcurrentCAS(t *testing.T) {
	_, sr := sceneTestRepository(t)
	ctx := context.Background()
	if _, err := sr.Create(ctx, sceneCreate("asset-a")); err != nil {
		t.Fatal(err)
	}
	mutation := scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 1}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(x float64) {
			defer wg.Done()
			_, err := sr.Move(ctx, mutation, scene.Rect{X: x, Width: 100, Height: 80}, scene.Anchor{})
			results <- err
		}(float64(i + 1))
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, scene.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	obj, err := sr.Get(ctx, "owner-a", "session-a", "object-a")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Revision != 2 {
		t.Fatalf("revision=%d", obj.Revision)
	}
}

func TestSceneGeneratedAssetRequiresReadyTaskInSameSession(t *testing.T) {
	repo, sr := sceneTestRepository(t)
	ctx := context.Background()
	_, err := repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,session_id,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "task-a", "owner-a", "text-to-image", "{}", "{}", "hash", "RUNNING", 1, "session-a", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, "attempt-a", "task-a", 1, "{}", "RUNNING", "submission-a", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "output-a", "task-a", "attempt-a", "primary", 1, 0, "key", "image/png", 1, "digest", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	input := sceneCreate("output-a")
	input.Initial.SourceTaskID = "task-a"
	if _, err = sr.Create(ctx, input); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("running task accepted: %v", err)
	}
	_, err = repo.db.ExecContext(ctx, `UPDATE generation_tasks SET state='SUCCEEDED' WHERE id='task-a'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sr.Create(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err = sr.RegisterCandidate(ctx, scene.Candidate{OwnerID: "owner-b", SessionID: "session-b", ObjectID: "object-a", Representation: scene.Representation{ID: "cross", ObjectID: "object-a", Kind: scene.Bitmap, AssetID: "output-a", MetadataSchemaRevision: "v1"}}); !errors.Is(err, scene.ErrNotFound) {
		t.Fatalf("cross-owner candidate: %v", err)
	}
}
