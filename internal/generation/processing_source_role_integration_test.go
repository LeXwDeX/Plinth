package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Raw PrepareInput must apply the same model-source rule as the public Task
// projection: only a successful Task's model output may fill a model-source
// parameter. A Splat delivery is not a model source for mesh processing.
func TestRetextureRejectsNonModelTaskOutput(t *testing.T) {
	ctx := context.Background()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	profile := product.Application().Runtime.InputAssets.ModelProfile
	if profile == nil {
		t.Fatal("default catalog has no model profile")
	}
	limits := profile.Limits
	policy := modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{
		MaxBytes: limits.MaxBytes, MaxJSONBytes: limits.MaxJSONBytes, MaxDepth: limits.MaxDepth,
		MaxElements: limits.MaxElements, MaxVertices: limits.MaxVertices,
		MaxTriangles: limits.MaxTriangles, MaxNodes: limits.MaxNodes, MaxWork: limits.MaxWork,
		MaxImagePixels: limits.MaxImagePixels, MaxTotalImagePixels: limits.MaxTotalImagePixels,
	}}
	h := newConfiguredHarness(t, mock.New(), nil, func(d *generation.Dependencies) {
		d.Catalog = product
		wrapped, err := modelsource.New(d.Inputs.(*inputassets.Service), policy)
		if err != nil {
			t.Fatal(err)
		}
		d.Inputs = wrapped
	})
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := h.assets.(*local.Store)
	insertOutput := func(taskID, assetID, role, variant, mediaType, payload string) {
		t.Helper()
		blob, err := store.PutBlob(ctx, bytes.NewReader([]byte(payload)), mediaType, 1<<20)
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
	insertOutput("splat-task", "splat-asset", "gaussian-splat", "splat", "application/vnd.art-ai.gaussian-splat", "splat-bytes")
	insertOutput("model-task", "model-asset", "model", "", "model/gltf-binary", "model-bytes")
	prepare := func(assetID string) (generation.PreparedInput, error) {
		body, err := json.Marshal(map[string]any{
			"source_asset":   assetID,
			"prompt":         "red",
			"target_formats": []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return h.module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: "owner", Capability: generation.Retexture,
			OfferingID: "meshy/processing/retexture", Input: body,
		})
	}
	preparedCount := func() int {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM generation_prepared_inputs`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if _, err := prepare("splat-asset"); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("Splat Task output was accepted as a model source: %v", err)
	}
	if count := preparedCount(); count != 0 {
		t.Fatalf("rejected Splat input persisted %d prepared inputs", count)
	}
	if _, err := prepare("model-asset"); err != nil {
		t.Fatalf("model Task output rejected by retexture: %v", err)
	}
	if count := preparedCount(); count != 1 {
		t.Fatalf("accepted model input persisted %d prepared inputs", count)
	}
}
