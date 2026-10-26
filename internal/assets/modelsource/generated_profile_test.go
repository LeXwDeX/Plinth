package modelsource_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func generatedPolicy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.GeneratedProfileID, Revision: modelinput.GeneratedProfileRevision, Limits: modelinput.Limits{MaxBytes: 128 << 20, MaxJSONBytes: 16 << 20, MaxDepth: 64, MaxElements: 1000000, MaxVertices: 3000000, MaxTriangles: 4000000, MaxNodes: 10000, MaxWork: 1 << 30, MaxImagePixels: 8192 * 8192, MaxTotalImagePixels: 4 * 8192 * 8192}}
}

// #264: the Task-output conversion profile is never an upload profile. No
// upload path accepts it, whether it is wired as the current policy, frozen
// into a declaration, or handed to the upload validator.
func TestGeneratedProfileNeverValidatesAnUpload(t *testing.T) {
	if err := generatedPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	if generatedPolicy().Uploadable() || !policy().Uploadable() {
		t.Fatal("only the static profile is an upload profile")
	}
	widened := policy()
	widened.Limits.MaxImagePixels, widened.Limits.MaxTotalImagePixels = 8192*8192, 8192*8192
	if widened.Validate() == nil {
		t.Fatal("upload profile may not raise its full-decode raster ceiling to 8192 square")
	}
	native, wrapped := fixture(t)
	if _, err := modelsource.New(native, generatedPolicy()); err == nil {
		t.Fatal("model source accepted a non-upload policy")
	}
	asset := upload(t, native, modelinputtest.Triangle(modelinputtest.Options{Indexed: true}))
	parameter := offering(false).Parameters[0]
	parameter.UploadedModel = &schema.UploadedModel{ProfileID: modelinput.GeneratedProfileID, ProfileRevision: modelinput.GeneratedProfileRevision, ProfileLimits: frozenLimits(generatedPolicy())}
	if _, _, err := wrapped.InspectForParameter(context.Background(), "owner", asset.ID, parameter); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("frozen generated profile validated an upload: %v", err)
	}
	if _, err := wrapped.Resolve(context.Background(), "owner", generation.Offering{Parameters: []generation.Parameter{parameter}}, values(asset)); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("resolver accepted an upload under the generated profile: %v", err)
	}
	// A Task-output-only source declares no uploaded-model route; a direct
	// upload is refused at resolution before any Provider sees it.
	taskOnly := generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MediaTypes: []string{"model/gltf-binary"}}
	if _, err := wrapped.Resolve(context.Background(), "owner", generation.Offering{Parameters: []generation.Parameter{taskOnly}}, values(asset)); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("Task-output-only source accepted a direct upload: %v", err)
	}
	path := filepath.Join(t.TempDir(), "mesh.glb")
	if err := os.WriteFile(path, modelinputtest.Triangle(modelinputtest.Options{}), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	validate := modelsource.UploadValidator(generatedPolicy(), "model", []string{".glb"}, []string{"model/gltf-binary"})
	if err := validate(context.Background(), file, generation.InputAssetUpload{Body: bytes.NewReader(nil), OriginalFilename: "mesh.glb", AssetType: "model", MediaType: "model/gltf-binary"}); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("upload validator accepted the generated profile: %v", err)
	}
}

// The static upload limit is unchanged: a generation-sized GLB is still
// refused at upload, before it can reach any conversion.
func TestUploadStillRejectsAGenerationSizedModel(t *testing.T) {
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 722, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192}}})
	path := filepath.Join(t.TempDir(), "large.glb")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	validate := modelsource.UploadValidator(policy(), "model", []string{".glb"}, []string{"model/gltf-binary"})
	if err := validate(context.Background(), file, generation.InputAssetUpload{OriginalFilename: "large.glb", AssetType: "model", MediaType: "model/gltf-binary"}); !schema.IsCode(err, generation.ErrModelInputLimit) {
		t.Fatalf("upload profile accepted a generation-sized model: %v", err)
	}
	if _, err := modelinput.ValidateGLBBytes(context.Background(), raw, generatedPolicy()); err != nil {
		t.Fatalf("the same model must fit the generated profile: %v", err)
	}
}
