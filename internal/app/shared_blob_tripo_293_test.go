package app

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #293: the 2026-11-12 real check of tripo-v3.1-full/text-to-3d with output_formats
// GLTF and FBX found the model asset and converted.gltf byte-identical and read it
// as the same 14.7 MB file stored twice. By design the adapter fulfils the model role
// with the GLTF conversion (its self-contained GLB), so both slots come from one
// locator. The two asset records share one CAS blob: the storage key is the content
// digest plus the extension of the final media type, and both slots resolve to it.
// The whole stack runs against the fake upstream.
func TestTripoGltfConversionFulfilsTheModelAndConvertedSlotsOverOneBlob(t *testing.T) {
	harness, _, _ := lowPolyHarness(t)
	accepted := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a brass robot","authorize_extra_credits":true,"output_formats":["GLTF","FBX"]}`,
		"shared-blob-293", map[string]string{"output_formats": "the game set: GLB and FBX"})
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}
	slot := func(role, variant string) generation.Asset {
		t.Helper()
		for _, asset := range task.Assets {
			if asset.Role == role && asset.Variant == variant {
				return asset
			}
		}
		t.Fatalf("no %s/%q asset: %+v", role, variant, task.Assets)
		return generation.Asset{}
	}
	model, gltf, fbx := slot("model", ""), slot("converted_model", "converted.gltf"), slot("converted_model", "converted.fbx")

	// Two records, one stored file.
	if model.ID == gltf.ID {
		t.Fatalf("the slots share an asset ID %q", model.ID)
	}
	if model.SHA256 != gltf.SHA256 || model.Size != gltf.Size || model.StorageKey != gltf.StorageKey {
		t.Fatalf("model and converted.gltf are not one blob: model=%+v gltf=%+v", model, gltf)
	}
	if fbx.StorageKey == model.StorageKey || fbx.SHA256 == model.SHA256 {
		t.Fatalf("the FBX export is a different file: fbx=%+v", fbx)
	}

	// Each slot still downloads on its own, with the same bytes.
	var first []byte
	for _, asset := range []generation.Asset{model, gltf} {
		content, err := harness.module.OpenAsset(context.Background(), "owner-acme", task.ID, asset.ID)
		if err != nil {
			t.Fatalf("open %s/%s: %v", asset.Role, asset.Variant, err)
		}
		data, readErr := io.ReadAll(content.Body)
		_ = content.Body.Close()
		if readErr != nil || int64(len(data)) != asset.Size {
			t.Fatalf("%s/%s read %d bytes (err=%v), want %d", asset.Role, asset.Variant, len(data), readErr, asset.Size)
		}
		if first == nil {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("the two slots returned different bytes")
		}
		if content.Role != asset.Role || content.Variant != asset.Variant || content.SHA256 != asset.SHA256 {
			t.Fatalf("%s/%s content = %+v", asset.Role, asset.Variant, content)
		}
	}
}
