package modelsource_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeIdentityAndHistoricalTaskBoundary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, err := local.New(filepath.Join(dir, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	native, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := modelsource.New(native, policy())
	if err != nil {
		t.Fatal(err)
	}
	asset := upload(t, native, modelinputtest.Triangle(modelinputtest.Options{}))
	if _, _, err := wrapped.Inspect(ctx, "other", asset.ID); !schema.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("foreign inspect: %v", err)
	}
	// Metadata disagreeing with actual immutable bytes must be rejected.
	if _, err := db.Exec(`UPDATE generation_input_assets SET sha256 = ? WHERE id = ?`, "wrong", asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Resolve(ctx, "owner", offering(false), values(asset)); !schema.IsCode(err, generation.ErrConflict) {
		t.Fatalf("stale hash: %v", err)
	}
	if _, err := db.Exec(`UPDATE generation_input_assets SET sha256 = ?,size_bytes=size_bytes+1 WHERE id = ?`, asset.SHA256, asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Resolve(ctx, "owner", offering(false), values(asset)); !schema.IsCode(err, generation.ErrConflict) {
		t.Fatalf("stale size: %v", err)
	}
	// Historical task outputs can exceed the new native-upload profile and must not be parsed by it.
	blob, err := blobs.PutBlob(ctx, bytes.NewReader(bytes.Repeat([]byte("historical"), 1300000)), "model/gltf-binary", 20<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('old-task','owner','text-to-3d','{}','{}','hash','SUCCEEDED',1,'2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('old-attempt','old-task',1,'{}','SUCCEEDED','old-key','2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES ('old-asset','old-task','old-attempt','model',1,0,?,'model/gltf-binary',?,?,'2026-09-23T00:00:00Z')`, blob.StorageKey, blob.Size, blob.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	route := offering(false)
	route.Parameters[0].UploadedModel = nil
	raw := json.RawMessage(`{"source_asset":"old-asset"}`)
	got, err := wrapped.Resolve(ctx, "owner", route, raw)
	if err != nil || len(got["source_asset"]) != 1 {
		t.Fatalf("historical output route rejected: %v", err)
	}
	// Smart Segmentation freezes the 10 MiB native-upload profile but can
	// reference a larger successful Task output up to its own 150 MB asset
	// limit. The profile is applied only when OpenOwnedUpload finds a direct
	// upload; the Adapter separately verifies the projected source bytes.
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	smartSegment, ok := product.Get("tripo/geometry/smartsegment-model")
	if !ok || blob.Size <= smartSegment.Parameters[0].UploadedModel.ProfileLimits.MaxBytes {
		t.Fatal("Smart Segmentation profile boundary fixture is invalid")
	}
	projected, err := wrapped.Resolve(ctx, "owner", smartSegment, raw)
	if err != nil || len(projected["source_asset"]) != 1 || projected["source_asset"][0].ID != "old-asset" {
		t.Fatalf("successful Task asset over native-upload profile was rejected: %v", err)
	}
	strictRoute := offering(false)
	strictRoute.Parameters[0].UploadedModel.NativeOnly = true
	if _, err := wrapped.Resolve(ctx, "owner", strictRoute, raw); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("Task output reached a native-upload-only route: %v", err)
	}
	if _, _, err := wrapped.Inspect(ctx, "owner", "old-asset"); !schema.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("task output masqueraded as upload: %v", err)
	}
	_, err = db.Exec(`INSERT INTO generation_task_tombstones (task_id,actor_id,deleted_at) VALUES ('old-task','owner','2026-09-23T00:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Resolve(ctx, "owner", route, raw); err == nil {
		t.Fatal("tombstoned output accepted")
	}
}

// A persisted Task projects its delivered assets into later inputs. A model
// source parameter may only take that Task's model output: splat, texture or
// converted artifacts are not model sources and must be rejected before a
// prepared input or any paid provider request.
func TestTaskOutputRoleBoundaryForModelSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, err := local.New(filepath.Join(dir, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	native, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := modelsource.New(native, policy())
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	retexture, ok := product.Get("meshy/processing/retexture")
	if !ok {
		t.Fatal("retexture Offering is unavailable")
	}
	insertOutput := func(taskID, assetID, role, variant, mediaType string) {
		t.Helper()
		blob, err := blobs.PutBlob(ctx, bytes.NewReader(bytes.Repeat([]byte("output"), 16)), mediaType, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			taskID, "owner", "text-to-3d", "{}", "{}", taskID+"-hash", "SUCCEEDED", 1,
			"2026-10-18T00:00:00Z", "2026-10-18T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			taskID+"-attempt", taskID, 1, "{}", "SUCCEEDED", taskID+"-key",
			"2026-10-18T00:00:00Z", "2026-10-18T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,variant,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			assetID, taskID, taskID+"-attempt", role, variant, 1, 0, blob.StorageKey, mediaType,
			blob.Size, blob.SHA256, "2026-10-18T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	insertOutput("splat-task", "splat-asset", "gaussian-splat", "splat", "application/vnd.art-ai.gaussian-splat")
	insertOutput("model-task", "model-asset", "model", "", "model/gltf-binary")
	source := func(id string) json.RawMessage {
		raw, err := json.Marshal(map[string]string{"source_asset": id})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := wrapped.Resolve(ctx, "owner", retexture, source("splat-asset")); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("splat Task output reached a model-source parameter: %v", err)
	}
	got, err := wrapped.Resolve(ctx, "owner", retexture, source("model-asset"))
	if err != nil || len(got["source_asset"]) != 1 || got["source_asset"][0].ID != "model-asset" {
		t.Fatalf("model Task output rejected by a model-source parameter: %v", err)
	}
	// The same contract holds when a persisted Task is re-resolved before an
	// attempt; only the legacy no-contract parameter keeps its exception.
	if _, err := wrapped.ResolveAccepted(ctx, generation.Task{OwnerID: "owner", Offering: retexture, Input: source("splat-asset")}); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("accepted Task re-resolved a splat output as a model source: %v", err)
	}
	accepted, err := wrapped.ResolveAccepted(ctx, generation.Task{OwnerID: "owner", Offering: retexture, Input: source("model-asset")})
	if err != nil || len(accepted["source_asset"]) != 1 {
		t.Fatalf("accepted Task rejected its model output: %v", err)
	}
	// Parameters that declare no uploaded-model contract are not model
	// sources and keep their historical Task-output asset behavior.
	legacy := offering(false)
	legacy.Parameters[0].UploadedModel = nil
	if got, err := wrapped.Resolve(ctx, "owner", legacy, source("splat-asset")); err != nil || len(got["source_asset"]) != 1 {
		t.Fatalf("non-model asset parameter rejected a Task output: %v", err)
	}
}

type historicalStartProbe struct {
	*mock.Adapter
	starts int
}

func (p *historicalStartProbe) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	p.starts++
	return p.Adapter.Start(ctx, req)
}

func TestHistoricalNativeUploadAcceptedTaskKeepsFrozenDispatchContract(t *testing.T) {
	for _, stage := range []string{"queued", "retry", "submitting"} {
		t.Run(stage, func(t *testing.T) {
			for _, mode := range []struct {
				name                       string
				wrapped, declared, foreign bool
				starts                     int
			}{
				{"previous_native_resolver", false, false, false, 1},
				{"upgraded_historical", true, false, false, 1},
				{"new_profile_rejects", true, true, false, 0},
				{"foreign_historical_rejects", true, false, true, 0},
			} {
				t.Run(mode.name, func(t *testing.T) {
					ctx := context.Background()
					dir := t.TempDir()
					path := filepath.Join(dir, "test.db")
					repo, err := sqlite.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer repo.Close()
					db, err := sql.Open("sqlite", path)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					blobs, err := local.New(filepath.Join(dir, "cas"))
					if err != nil {
						t.Fatal(err)
					}
					defer blobs.Close()
					native, err := inputassets.New(repo, blobs)
					if err != nil {
						t.Fatal(err)
					}
					// Historical native uploads were not admitted under static-glb-v1. Their
					// accepted Tasks persist an Offering without that profile declaration.
					doc, bin := modelinputtest.Document(modelinputtest.Options{Textured: true})
					doc["extensionsUsed"] = []string{"KHR_materials_unlit"}
					doc["materials"].([]any)[0].(map[string]any)["extensions"] = map[string]any{"KHR_materials_unlit": map[string]any{}}
					asset := upload(t, native, modelinputtest.GLB(doc, bin))
					product, err := catalog.LoadDefault()
					if err != nil {
						t.Fatal(err)
					}
					frozen := offering(false)
					frozen.ID = "historical/remesh"
					frozen.ProviderID = "historical"
					frozen.Revision = "before-profile"
					frozen.Capability = generation.Remesh
					frozen.AdapterKey = "historical.mock"
					frozen.Enabled = true
					frozen.IdempotentSubmission = true
					frozen.Timeout = time.Minute
					frozen.PollInterval = time.Second
					frozen.Parameters[0].UploadedModel = nil
					if mode.declared {
						frozen.Parameters[0].UploadedModel = &schema.UploadedModel{ProfileID: "static-glb-v1"}
					}
					if mode.foreign {
						if _, err = db.Exec(`UPDATE generation_input_assets SET owner_id='foreign' WHERE id=?`, asset.ID); err != nil {
							t.Fatal(err)
						}
					}
					if mode.wrapped {
						strict, err := modelsource.New(native, policy())
						if err != nil {
							t.Fatal(err)
						}
						// A historical dispatch exception must never be usable by
						// ordinary new PrepareInput/Submit source resolution.
						if _, err = strict.Resolve(ctx, "owner", frozen, values(asset)); err == nil {
							t.Fatal("ordinary Resolve bypassed the new profile/owner contract")
						}
					}
					frozenJSON, err := json.Marshal(frozen)
					if err != nil {
						t.Fatal(err)
					}
					_, err = db.Exec(`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,next_action_at,created_at,updated_at) VALUES ('accepted','owner','remesh',?,?,'historical-hash','QUEUED',1,'2026-09-23T00:00:00Z','2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`, string(frozenJSON), string(values(asset)))
					if err != nil {
						t.Fatal(err)
					}
					if stage != "queued" {
						attemptState := "FAILED"
						if stage == "submitting" {
							attemptState = "SUBMITTING"
						}
						_, err = db.Exec(`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('previous','accepted',1,?,?,'previous-key','2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`, string(frozenJSON), attemptState)
						if err != nil {
							t.Fatal(err)
						}
						if stage == "submitting" {
							if _, err = db.Exec(`UPDATE generation_tasks SET state='RUNNING' WHERE id='accepted'`); err != nil {
								t.Fatal(err)
							}
						}
						if _, err = db.Exec(`UPDATE generation_tasks SET current_attempt_id='previous' WHERE id='accepted'`); err != nil {
							t.Fatal(err)
						}
					}
					var inputs generation.InputAssetResolver = native
					if mode.wrapped {
						inputs, err = modelsource.New(native, policy())
						if err != nil {
							t.Fatal(err)
						}
					}
					provider := &historicalStartProbe{Adapter: mock.New()}
					registry := providers.NewRegistry()
					if err = registry.Register(frozen.AdapterKey, provider); err != nil {
						t.Fatal(err)
					}
					module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: product, Providers: registry, Assets: blobs, Inputs: inputs})
					if err != nil {
						t.Fatal(err)
					}
					processed, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "history", BatchSize: 1, Concurrency: 1})
					if err != nil || processed != 1 {
						t.Fatalf("reconcile: %d %v", processed, err)
					}
					actual, err := module.Get(ctx, "owner", "accepted")
					if err != nil {
						t.Fatal(err)
					}
					if provider.starts != mode.starts || (mode.starts == 1 && actual.State == generation.TaskFailed) || (mode.starts == 0 && actual.State != generation.TaskFailed) {
						t.Fatalf("upgrade changed frozen accepted task: starts=%d state=%s failure=%+v", provider.starts, actual.State, actual.Failure)
					}
				})
			}
		})
	}
}
