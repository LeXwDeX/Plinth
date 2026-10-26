package generation

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// riggedResult is a Meshy Rigging result shaped like dev-test task_8dd90530
// (#326): the six basic-animation files come first, the required rigged
// character files last. The first GLB is a 79,600-byte skeleton.
func riggedResult() Task {
	task := Task{ID: "rig", State: TaskSucceeded, Capability: "rigging", Offering: Offering{ProviderID: "meshy"}}
	files := []struct {
		variant, media string
		size           int64
		required       bool
	}{
		{"basic_animations.running_armature_glb_url", "model/gltf-binary", 79600, false},
		{"basic_animations.running_glb_url", "model/gltf-binary", 3_000_000, false},
		{"basic_animations.running_fbx_url", "application/octet-stream", 3_000_000, false},
		{"basic_animations.walking_armature_glb_url", "model/gltf-binary", 79600, false},
		{"basic_animations.walking_glb_url", "model/gltf-binary", 3_000_000, false},
		{"basic_animations.walking_fbx_url", "application/octet-stream", 3_000_000, false},
		{"rigged_character_fbx_url", "application/octet-stream", 60_000_000, true},
		{"rigged_character_glb_url", "model/gltf-binary", 105_271_888, true},
	}
	for index, file := range files {
		task.Assets = append(task.Assets, Asset{ID: AssetID(file.variant), TaskID: task.ID, Role: "model", Variant: file.variant,
			MediaType: file.media, Size: file.size, Required: file.required, Ordinal: index + 1, StorageKey: "key/" + file.variant})
	}
	return task
}

var shippedPreference = []string{"glb", "gltf", "fbx", "obj", "stl"}

// #326: the main model of a result is a required output, never an attachment
// the result happens to list first, and of the required files the one the
// stage shows (GLB).
func TestPrimaryModelAssetIsTheRequiredCharacterNotAnAttachedSkeleton(t *testing.T) {
	primary, ok := PrimaryModelAsset(riggedResult(), shippedPreference)
	if !ok || primary.ID != "rigged_character_glb_url" {
		t.Fatalf("primary = %q, %v; want the required rigged character GLB", primary.ID, ok)
	}
	// Without a preference the required files still come first.
	if primary, _ := PrimaryModelAsset(riggedResult(), nil); !primary.Required {
		t.Fatalf("primary without preference = %q; want a required file", primary.ID)
	}
}

func TestPrimaryModelAssetKeepsTheVariantPreferenceAmongRequiredFiles(t *testing.T) {
	task := Task{ID: "meshy71", State: TaskSucceeded, Assets: []Asset{
		{ID: "3mf", Role: "model", Variant: "3mf", MediaType: "application/zip", Required: true, Ordinal: 1, StorageKey: "k"},
		{ID: "fbx", Role: "model", Variant: "fbx", MediaType: "application/octet-stream", Required: true, Ordinal: 2, StorageKey: "k"},
		{ID: "glb", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Required: true, Ordinal: 3, StorageKey: "k"},
		{ID: "unsaved", Role: "model", Variant: "glb", Required: true, Ordinal: 0},
		{ID: "thumb", Role: "thumbnail", MediaType: "image/png", Required: true, StorageKey: "k"},
	}}
	if primary, _ := PrimaryModelAsset(task, shippedPreference); primary.ID != "glb" {
		t.Fatalf("primary = %q; want the saved GLB", primary.ID)
	}
	if _, ok := PrimaryModelAsset(Task{Assets: []Asset{{ID: "thumb", Role: "thumbnail", StorageKey: "k"}}}, nil); ok {
		t.Fatal("a task without a saved model has no primary model")
	}
}

// A step that takes any saved file of the source binds the main model first;
// an attachment is used only when the main model does not fit.
func TestBindModelSourcePrefersTheMainModelOverAttachments(t *testing.T) {
	offering := modelSourceOffering(Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MaxBytes: 200_000_000})
	binding, err := BindModelSource(offering, riggedResult(), "", shippedPreference)
	if err != nil || binding.AssetID != "rigged_character_glb_url" {
		t.Fatalf("binding = %+v, %v; want the rigged character", binding, err)
	}
}
