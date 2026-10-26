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
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// A successful public Task's delivered GLB and a direct upload have different
// admission boundaries. Both still enter Smart Segmentation through the same
// public PrepareInput path and freeze the actual source asset evidence.
func TestSmartSegmentPrepareLargeDeliveredGLBAndDirectUploadBoundary(t *testing.T) {
	const offeringID = "tripo/geometry/smartsegment-model"
	ctx := context.Background()
	// The shipped Offering is enabled with its legacy discovery record (#185).
	product, err := catalog.Load(bytes.NewReader(catalogtest.DefaultCatalogBytes(t, offeringID)))
	if err != nil {
		t.Fatal(err)
	}
	profile := product.Application().Runtime.InputAssets.ModelProfile
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

	doc, bin := modelinputtest.Document(modelinputtest.Options{Indexed: true})
	bin = append(bin, make([]byte, 11<<20)...)
	doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(bin)
	raw := modelinputtest.GLB(doc, bin)
	if int64(len(raw)) <= limits.MaxBytes {
		t.Fatal("test GLB does not cross the native-upload profile limit")
	}
	store := h.assets.(*local.Store)
	blob, err := store.PutBlob(ctx, bytes.NewReader(raw), "model/gltf-binary", 150000000)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('source-task','owner','text-to-3d','{}','{}','hash','SUCCEEDED',1,'2026-10-17T00:00:00Z','2026-10-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('source-attempt','source-task',1,'{}','SUCCEEDED','source-key','2026-10-17T00:00:00Z','2026-10-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES ('source-asset','source-task','source-attempt','model',1,0,?,'model/gltf-binary',?,?,'2026-10-17T00:00:00Z')`, blob.StorageKey, blob.Size, blob.SHA256); err != nil {
		t.Fatal(err)
	}
	prepare := func(owner, asset string) (generation.PreparedInput, error) {
		// The full delivery includes the five declared exports, so the
		// frozen input carries the full-quote consent (2026-10-21 contract).
		body, err := json.Marshal(map[string]any{"source_asset": asset, "authorize_extra_credits": true})
		if err != nil {
			t.Fatal(err)
		}
		return h.module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: generation.OwnerID(owner), Capability: generation.Capability("mesh-segmentation"),
			OfferingID: offeringID, Input: body,
		})
	}
	prepared, err := prepare("owner", "source-asset")
	if err != nil {
		t.Fatalf("successful Task asset over direct-upload limit rejected: %v", err)
	}
	if len(prepared.Assets) != 1 || prepared.Assets[0].ID != "source-asset" ||
		prepared.Assets[0].Size != blob.Size || prepared.Assets[0].SHA256 != blob.SHA256 ||
		prepared.Assets[0].MediaType != "model/gltf-binary" || len(prepared.Dependencies) != 0 {
		t.Fatalf("prepared source evidence does not match delivered GLB: %+v", prepared)
	}
	unconsented, _ := json.Marshal(map[string]string{"source_asset": "source-asset"})
	if _, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner", Capability: generation.Capability("mesh-segmentation"),
		OfferingID: offeringID, Input: unconsented,
	}); err == nil {
		t.Fatal("full-delivery export cost prepared without consent")
	}
	if _, err := prepare("other", "source-asset"); err == nil {
		t.Fatalf("foreign Task output accepted: %v", err)
	}

	// The public direct-upload ceiling remains the platform's 10 MiB budget.
	if _, err := h.inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{
		Body: bytes.NewReader(raw), OriginalFilename: "large.glb", AssetType: "model",
		MediaType: "model/gltf-binary", MaxBytes: limits.MaxBytes,
	}); err == nil {
		t.Fatalf("oversized direct upload passed the public size limit: %v", err)
	}
	// Even if a stored direct upload bypassed the HTTP size check, PrepareInput
	// must enforce the frozen native model profile before any paid dispatch.
	upload, err := h.inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{
		Body: bytes.NewReader(raw), OriginalFilename: "large.glb", AssetType: "model",
		MediaType: "model/gltf-binary", MaxBytes: 150000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepare("owner", string(upload.ID)); err == nil {
		t.Fatalf("direct upload over frozen model profile passed PrepareInput: %v", err)
	}
}
