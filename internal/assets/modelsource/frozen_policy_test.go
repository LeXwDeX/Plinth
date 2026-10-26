package modelsource_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func frozenLimits(p modelinput.Policy) *schema.ModelInputLimits {
	l := p.Limits
	return &schema.ModelInputLimits{MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements, MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork, MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels}
}
func frozenOffering() generation.Offering {
	v := offering(false)
	v.Parameters[0].UploadedModel.ProfileRevision = modelinput.ProfileRevision
	v.Parameters[0].UploadedModel.ProfileLimits = frozenLimits(policy())
	return v
}
func TestFrozenUploadPolicySurvivesCurrentPolicyChangeAndRestore(t *testing.T) {
	ctx := context.Background()
	native, _ := fixture(t)
	asset := upload(t, native, modelinputtest.Triangle(modelinputtest.Options{}))
	original := frozenOffering()
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored generation.Offering
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	current := policy()
	current.Limits.MaxVertices = 2
	restarted, err := modelsource.New(native, current)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.Inspect(ctx, "owner", asset.ID); !generation.IsCode(err, generation.ErrModelInputLimit) {
		t.Fatalf("global current policy not enforced: %v", err)
	}
	for _, accepted := range []bool{false, true} {
		var result map[string][]generation.InputAsset
		if accepted {
			result, err = restarted.ResolveAccepted(ctx, generation.Task{OwnerID: "owner", Offering: restored, Input: values(asset)})
		} else {
			result, err = restarted.Resolve(ctx, "owner", restored, values(asset))
		}
		if err != nil || len(result["source_asset"]) != 1 || result["source_asset"][0].SHA256 != asset.SHA256 {
			t.Fatalf("accepted=%v frozen policy changed: %v", accepted, err)
		}
	}
	legacy := offering(false)
	if _, err := restarted.Resolve(ctx, "owner", legacy, values(asset)); !generation.IsCode(err, generation.ErrModelInputLimit) {
		t.Fatalf("legacy did not retain current policy: %v", err)
	}
}
func TestFrozenUploadPolicyRejectsPartialInvalidAndWrongRevision(t *testing.T) {
	native, wrapped := fixture(t)
	asset := upload(t, native, modelinputtest.Triangle(modelinputtest.Options{}))
	for name, mutate := range map[string]func(*schema.UploadedModel){
		"revision only":        func(p *schema.UploadedModel) { p.ProfileLimits = nil },
		"limits only":          func(p *schema.UploadedModel) { p.ProfileRevision = "" },
		"invalid limits":       func(p *schema.UploadedModel) { p.ProfileLimits.MaxVertices = 0 },
		"unsupported revision": func(p *schema.UploadedModel) { p.ProfileRevision = "2" },
	} {
		t.Run(name, func(t *testing.T) {
			route := frozenOffering()
			mutate(route.Parameters[0].UploadedModel)
			if _, err := wrapped.Resolve(context.Background(), "owner", route, values(asset)); err == nil {
				t.Fatal("invalid frozen policy accepted")
			}
			if _, err := wrapped.ResolveAccepted(context.Background(), generation.Task{OwnerID: "owner", Offering: route, Input: values(asset)}); err == nil {
				t.Fatal("accepted task bypassed invalid frozen policy")
			}
		})
	}
}
func TestValidateRouteRejectsWrongFrozenProfileRevision(t *testing.T) {
	parameter := frozenOffering().Parameters[0]
	profile := modelinput.Profile{ProfileID: modelinput.ProfileID, Revision: "other", Size: 128}
	if err := modelsource.ValidateRoute(parameter, profile); err == nil {
		t.Fatal("route accepted a different validation revision")
	}
}

func TestCurrentUploadPolicyCannotBeBypassedByOldFrozenParameter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "uploads.db")
	repo, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	native, err := inputassets.NewWithConfig(repo, blobs, inputassets.Config{UploadValidator: modelsource.UploadValidator(policy(), "model", []string{".glb"}, []string{"model/gltf-binary"})})
	if err != nil {
		t.Fatal(err)
	}
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	asset := upload(t, native, raw)
	current := policy()
	current.Limits.MaxVertices = 2
	strict, err := inputassets.NewWithConfig(repo, blobs, inputassets.Config{UploadValidator: modelsource.UploadValidator(current, "model", []string{".glb"}, []string{"model/gltf-binary"})})
	if err != nil {
		t.Fatal(err)
	}
	service, err := modelsource.New(strict, current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strict.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "new.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 10 << 20}); !generation.IsCode(err, generation.ErrModelInputLimit) {
		t.Fatalf("new upload bypassed current limits: %v", err)
	}
	if _, profile, err := service.InspectForParameter(ctx, "owner", asset.ID, frozenOffering().Parameters[0]); err != nil || profile.SHA256 != asset.SHA256 {
		t.Fatalf("old owned bytes lost frozen contract: %v", err)
	}
	if _, _, err := service.InspectForParameter(ctx, "foreign", asset.ID, frozenOffering().Parameters[0]); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("frozen inspect bypassed owner: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]int{"generation_input_assets": 1, "generation_tasks": 0, "generation_prepared_inputs": 0, "execution_grants": 0} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s rows=%d want=%d err=%v", table, n, want, err)
		}
	}
}
