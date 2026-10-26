package modelsource_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"path/filepath"
	"testing"
)

func policy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Limits: modelinput.Limits{MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 64, MaxElements: 250000, MaxVertices: 500000, MaxTriangles: 500000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 16777216, MaxTotalImagePixels: 33554432}}
}
func fixture(t *testing.T) (*inputassets.Service, *modelsource.Service) {
	t.Helper()
	dir := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	blobs, err := local.New(filepath.Join(dir, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { blobs.Close() })
	native, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := modelsource.New(native, policy())
	if err != nil {
		t.Fatal(err)
	}
	return native, wrapped
}
func upload(t *testing.T, native *inputassets.Service, body []byte) generation.InputAsset {
	t.Helper()
	asset, err := native.UploadAsset(context.Background(), "owner", generation.InputAssetUpload{Body: bytes.NewReader(body), OriginalFilename: "mesh.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 10 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}
func offering(requireTextures bool) generation.Offering {
	return generation.Offering{Parameters: []generation.Parameter{{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{ProfileID: modelinput.ProfileID, RequireTextures: requireTextures}}}}
}
func values(asset generation.InputAsset) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"source_asset": string(asset.ID)})
	return raw
}
func TestResolveUploadedModelsChecksActualBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     []byte
		textures bool
		code     schema.ErrorCode
	}{
		{"static geometry", modelinputtest.Triangle(modelinputtest.Options{}), false, ""},
		{"textured geometry", modelinputtest.Triangle(modelinputtest.Options{Textured: true}), true, ""},
		{"malformed bytes", []byte("not a GLB"), false, "invalid_model_input"},
		{"missing texture", modelinputtest.Triangle(modelinputtest.Options{}), true, "unsupported_model_input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native, wrapped := fixture(t)
			asset := upload(t, native, tc.body)
			result, err := wrapped.Resolve(context.Background(), "owner", offering(tc.textures), values(asset))
			if tc.code != "" {
				var domain *generation.Error
				if !errors.As(err, &domain) || domain.Code != tc.code {
					t.Fatalf("want %s, got %v (assets=%v)", tc.code, err, result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result["source_asset"][0].ID != asset.ID || result["source_asset"][0].SHA256 != asset.SHA256 {
				t.Fatal("original upload identity changed")
			}
		})
	}
}
func TestResolveRequiresAnUploadedModelRoute(t *testing.T) {
	native, wrapped := fixture(t)
	asset := upload(t, native, modelinputtest.Triangle(modelinputtest.Options{}))
	route := offering(false)
	route.Parameters[0].UploadedModel = nil
	_, err := wrapped.Resolve(context.Background(), "owner", route, values(asset))
	var domain *generation.Error
	if !errors.As(err, &domain) || domain.Code != "unsupported_model_input" {
		t.Fatalf("undeclared uploaded route accepted: %v", err)
	}
}
