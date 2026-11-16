package modeldelivery

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #305: a Tripo rig delivers the GLB its conversion writes as the model and keeps
// the GLB the upstream returned beside it as the original file, under the role
// original_model. The format rows and the one-click ZIP are what the purpose asked
// for: they hold the converted GLB and the FBX, never the original, whose textures
// are the upstream's own resolution. The original stays a record of the task, read by
// its own asset ID. Both rules read the Schema's roles, not a provider.

const (
	convertedRigGLB = "CONVERTED-RIG-GLB"
	convertedRigFBX = "CONVERTED-RIG-FBX"
	nativeRigGLB    = "NATIVE-RIG-GLB"
)

func (f *fixture) riggedTask(id string) generation.Task {
	return f.addTask(id, "owner", "tripo", "rigging",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary", body: convertedRigGLB},
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx", body: convertedRigFBX},
		assetSpec{role: "original_model", variant: "glb", mediaType: "model/gltf-binary", body: nativeRigGLB},
		assetSpec{role: "thumbnail", variant: "rendered_image", mediaType: "image/png"})
}

func TestArchiveOfARigPacksTheConvertedModelAndNotTheOriginalFile(t *testing.T) {
	f := newFixture(t)
	task := f.riggedTask("rig-305")

	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)

	var bodies []string
	for name, body := range files {
		if name != "README.txt" {
			bodies = append(bodies, body)
		}
		if strings.Contains(body, nativeRigGLB) && name != "README.txt" {
			t.Fatalf("the original file is in the ZIP as %s", name)
		}
	}
	slices.Sort(bodies)
	if want := []string{convertedRigFBX, convertedRigGLB}; !slices.Equal(bodies, want) {
		t.Fatalf("the ZIP holds %v, want the converted GLB and the FBX only (%v)", bodies, want)
	}
	if readme := files["README.txt"]; strings.Contains(readme, "original") || strings.Contains(readme, "原始") {
		t.Fatalf("the README names the original file:\n%s", readme)
	}
}

func TestFormatRowsOfARigPointAtTheConvertedModelNotTheOriginalFile(t *testing.T) {
	f := newFixture(t)
	task := f.riggedTask("rig-305-rows")
	converted, fbx, original := task.Assets[0], task.Assets[1], task.Assets[2]

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row := formatByID(t, coverage, "glb"); row.Status != StatusAvailable || row.AssetID != converted.ID {
		t.Fatalf("glb row = %#v, want the converted model %s", row, converted.ID)
	}
	if row := formatByID(t, coverage, "fbx"); row.Status != StatusAvailable || row.AssetID != fbx.ID {
		t.Fatalf("fbx row = %#v, want the FBX %s", row, fbx.ID)
	}
	for _, row := range coverage.Formats {
		if row.AssetID == original.ID {
			t.Fatalf("format %s names the original file %s", row.ID, original.ID)
		}
	}
}
