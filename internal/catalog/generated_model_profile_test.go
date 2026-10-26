package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #264: the Task-output conversion profile is declared in the Product Schema,
// sized for real generation results, kept server-side, and bound only to the
// package direction.
func TestGeneratedModelProfileIsDeclaredValidatedAndServerSide(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	generated := application.Runtime.InputAssets.GeneratedModelProfile
	upload := application.Runtime.InputAssets.ModelProfile
	if generated == nil || generated.ID != "generated-glb-v1" || generated.Revision != "1" {
		t.Fatalf("generated profile = %+v", generated)
	}
	l := generated.Limits
	if l.MaxBytes != 128<<20 || l.MaxVertices != 3000000 || l.MaxImagePixels != 8192*8192 || l.MaxTotalImagePixels < l.MaxImagePixels {
		t.Fatalf("generated limits = %+v", l)
	}
	if upload.ID != "static-glb-v1" || upload.Limits.MaxBytes != 10<<20 || upload.Limits.MaxVertices != 500000 || upload.Limits.MaxImagePixels != 16777216 {
		t.Fatalf("upload profile changed: %+v", upload.Limits)
	}
	public, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("generated_model_profile")) || bytes.Contains(public, []byte("generated-glb-v1")) {
		t.Fatal("the server-only conversion profile entered the public Schema")
	}
	offering, _ := product.Get("local/generated-glb/to-gltf")
	var options schema.ModelConversionOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	if options.Direction != schema.ModelConversionGLBToGLTFPackage || options.ProfileLimits == nil || *options.ProfileLimits != l || options.MaxConcurrent != 1 {
		t.Fatalf("package Offering did not freeze the generated profile: %+v", options)
	}
	if source := offering.Parameters[0]; source.UploadedModel != nil || source.MaxBytes != l.MaxBytes {
		t.Fatalf("package source must be a Task output within the profile: %+v", source)
	}
	if output := offering.Outputs[0]; output.MediaTypes[0] != "application/zip" || output.MaxBytes < l.MaxBytes {
		t.Fatalf("package output = %+v", output)
	}
	for name, mutate := range map[string]func(*InputAssetRuntimeSchema){
		"narrower than upload": func(r *InputAssetRuntimeSchema) { r.GeneratedModelProfile.Limits.MaxVertices = 1 },
		"same id as upload":    func(r *InputAssetRuntimeSchema) { r.GeneratedModelProfile.ID = r.ModelProfile.ID },
		"no upload profile":    func(r *InputAssetRuntimeSchema) { r.ModelProfile = nil },
		"json above bytes": func(r *InputAssetRuntimeSchema) {
			r.GeneratedModelProfile.Limits.MaxJSONBytes = r.GeneratedModelProfile.Limits.MaxBytes + 1
		},
		"no description": func(r *InputAssetRuntimeSchema) { r.GeneratedModelProfile.Description = " " },
	} {
		runtime := cloneApplication(application).Runtime.InputAssets
		mutate(&runtime)
		if validateGeneratedModelProfile(runtime) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Each direction freezes only its own profile.
	app := cloneApplication(application)
	for name, options := range map[string]map[string]any{
		"upload direction on generated profile": {"direction": "glb-to-gltf", "profile_id": "generated-glb-v1", "profile_revision": "1", "max_work": 1 << 20},
		"package direction on upload profile":   {"direction": "glb-to-gltf-package", "profile_id": "static-glb-v1", "profile_revision": "1", "max_work": 1 << 20, "max_concurrent": 1},
		"package direction wrong revision":      {"direction": "glb-to-gltf-package", "profile_id": "generated-glb-v1", "profile_revision": "2", "max_work": 1 << 20, "max_concurrent": 1},
	} {
		raw, _ := json.Marshal(options)
		candidate := schema.Offering{AdapterKey: "local.model", Capability: schema.FormatConversion, ProviderOptions: raw}
		if freezeLocalConversionProfile(&candidate, app) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
