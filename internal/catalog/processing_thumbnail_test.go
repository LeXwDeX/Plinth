package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #324 (from #187 note 98985): Meshy Retexture results had no preview on the version strip
// and the chat card, while Meshy's Retexture task object carries `thumbnail_url` (as the
// remesh object does, and as the acceptance run read upstream). The platform renders no 3D
// thumbnails of its own; a 3D result gets one only when its Offering maps the provider's.
// Retexture declares the same optional background preview slot remesh has.
func TestMeshyRetextureMapsTheUpstreamThumbnail(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"meshy/processing/retexture", "meshy/processing/remesh"} {
		offering, ok := product.Get(schema.OfferingID(id))
		if !ok {
			t.Fatalf("%s missing", id)
		}
		slot := false
		for _, output := range offering.Outputs {
			if output.Role == "thumbnail" && output.Variant == "primary" && output.Minimum != nil && *output.Minimum == 0 && output.Delivery == "background" {
				slot = true
			}
		}
		var options struct {
			ResultAssets []struct {
				Path, Role, Mode, Variant string
			} `json:"result_assets"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		mapped := false
		for _, asset := range options.ResultAssets {
			if asset.Path == "thumbnail_url" && asset.Role == "thumbnail" && asset.Mode == "scalar" && asset.Variant == "primary" {
				mapped = true
			}
		}
		if !slot || !mapped {
			t.Errorf("%s: thumbnail slot=%v mapped=%v", id, slot, mapped)
		}
	}
}
