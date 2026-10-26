package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #185: Tripo's native OBJ export is a ZIP. Declared as such, the delivered
// archive is the OBJ row of the 格式 tab — never a paid conversion offer for a
// file the user already has — and it downloads as .obj.zip.
func TestTripoNativeOBJArchiveIsTheDeliveredOBJ(t *testing.T) {
	f := newFixture(t)
	f.addTask("segment", "owner", "tripo", "mesh-segmentation",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "fbx", mediaType: "model/fbx"},
		assetSpec{role: "converted_model", variant: "obj", mediaType: "application/zip"},
		assetSpec{role: "converted_model", variant: "stl", mediaType: "model/stl"},
		assetSpec{role: "converted_model", variant: "usdz", mediaType: "model/vnd.usdz+zip"},
		assetSpec{role: "converted_model", variant: "3mf", mediaType: "model/3mf"},
	)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "segment")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusAvailable || row.Conversion != nil {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	obj := formatByID(t, coverage, "obj")
	if obj.MediaType != "application/zip" {
		t.Fatalf("OBJ row = %#v", obj)
	}
	name, ok := f.service.DownloadFilename("segment", generation.Asset{Role: "converted_model", Variant: "obj", MediaType: "application/zip"})
	if !ok || name != "model-segment.obj.zip" {
		t.Fatalf("OBJ archive download name = %q %v", name, ok)
	}
}
