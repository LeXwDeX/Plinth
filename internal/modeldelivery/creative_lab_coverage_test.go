package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #189: a finished Creative Lab task lists the files it delivered as available
// in the format list, instead of every format (including the delivered GLB)
// as unavailable. Formats it did not deliver follow the ordinary conversion
// rules, the same as other 3D results (the #187 mesh-segmentation fix).
func TestCreativeLabResultsListTheirDeliveredFormats(t *testing.T) {
	f := newFixture(t)
	f.addTask("figure-build", "owner", "meshy", "creative-lab-build",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "obj", mediaType: "model/obj"},
		assetSpec{role: "material", variant: "mtl", mediaType: "text/plain"},
		assetSpec{role: "texture", variant: "1.base_color", mediaType: "image/png"},
	)
	// The keycap build delivers its OBJ inside a ZIP bundle (obj_zip).
	f.addTask("keycap-build", "owner", "meshy", "creative-lab-build",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "bundle", variant: "obj_zip", mediaType: "application/zip"},
	)
	// The collapsible toy delivers a required STL and a background GLB preview.
	f.addTask("collapsible", "owner", "meshy", "creative-lab-prototype",
		assetSpec{role: "model", variant: "stl", mediaType: "model/stl"},
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
	)
	cases := map[string]map[string]generation.AssetID{
		"figure-build": {"glb": "figure-build-a0", "obj": "figure-build-a1"},
		"keycap-build": {"glb": "keycap-build-a0", "obj": "keycap-build-a1"},
		"collapsible":  {"stl": "collapsible-a0", "glb": "collapsible-a1"},
	}
	for id, delivered := range cases {
		coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), generation.TaskID(id))
		if err != nil {
			t.Fatal(err)
		}
		if !coverage.Applicable {
			t.Fatalf("%s: coverage not applicable: %#v", id, coverage)
		}
		for format, asset := range delivered {
			row := formatByID(t, coverage, format)
			if row.Status != StatusAvailable || row.AssetID != asset || row.TaskID != generation.TaskID(id) {
				t.Fatalf("%s %s = %#v", id, format, row)
			}
		}
	}
}
