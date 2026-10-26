package catalog_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #329: the default Catalog follows Meshy's documentation as checked on
// 2026-10-22 (docs/provider-schema-architecture.md, Meshy baseline).

func meshy329Catalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog
}

func meshy329Offering(t *testing.T, taskCatalog *catalog.Catalog, id generation.OfferingID) generation.Offering {
	t.Helper()
	offering, ok := taskCatalog.Get(id)
	if !ok {
		t.Fatalf("offering %s is missing", id)
	}
	return offering
}

func meshy329Parameter(offering generation.Offering, name string) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func meshy329Options(t *testing.T, offering generation.Offering) map[string]any {
	t.Helper()
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	return options
}

// Only the newest models are offered for generation: Meshy 7 stays a disabled
// history record, Meshy 7.1 is the default, and Meshy T2.5 (Smart Topology)
// is its own non-default Offering for text and single-image generation only.
func TestMeshy329OffersOnlyCurrentGenerationModels(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for _, capability := range []generation.Capability{generation.TextTo3D, generation.ImageTo3D} {
		var meshyModels []string
		for _, offering := range taskCatalog.List(capability) {
			if offering.ProviderID == "meshy" {
				meshyModels = append(meshyModels, offering.ModelID)
			}
		}
		slices.Sort(meshyModels)
		want := []string{"meshy-7.1", "meshy-t2.5"}
		if capability == generation.ImageTo3D {
			want = []string{"meshy-7.1", "meshy-7.1-multiview", "meshy-t2.5"}
		}
		if !slices.Equal(meshyModels, want) {
			t.Fatalf("%s Meshy models = %v, want %v", capability, meshyModels, want)
		}
		input := `{"prompt":"castle"}`
		if capability == generation.ImageTo3D {
			input = `{"reference_assets":["asset-1"]}`
		}
		offering, _, err := taskCatalog.Normalize(capability, "", json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if offering.ModelID != "meshy-7.1" {
			t.Fatalf("%s default model = %s", capability, offering.ModelID)
		}
		history := meshy329Offering(t, taskCatalog, generation.OfferingID("meshy/meshy-7/"+string(capability)))
		if history.Enabled {
			t.Fatalf("%s is enabled", history.ID)
		}
	}
}

func TestMeshy329SmartTopologyOfferings(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for _, tc := range []struct {
		id      generation.OfferingID
		credits int
	}{
		// Text to 3D: Smart Topology preview 5 + refine at 2K 10.
		{"meshy/meshy-t2.5/text-to-3d", 15},
		// Image to 3D: Smart Topology with 8K textures 20.
		{"meshy/meshy-t2.5/image-to-3d", 20},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			offering := meshy329Offering(t, taskCatalog, tc.id)
			if !offering.Enabled || offering.Default || offering.Recommended || offering.ModelID != "meshy-t2.5" {
				t.Fatalf("identity = enabled %v default %v recommended %v model %s", offering.Enabled, offering.Default, offering.Recommended, offering.ModelID)
			}
			if offering.EstimatedCredits != tc.credits {
				t.Fatalf("estimated credits = %d, want %d", offering.EstimatedCredits, tc.credits)
			}
			polycount, ok := meshy329Parameter(offering, "target_polycount")
			if !ok || polycount.Minimum == nil || *polycount.Minimum != 100 || polycount.Maximum == nil || *polycount.Maximum != 30000 {
				t.Fatalf("target_polycount = %#v", polycount)
			}
			var defaultPolycount int
			if err := json.Unmarshal(polycount.Default, &defaultPolycount); err != nil || defaultPolycount != 15000 {
				t.Fatalf("target_polycount default = %s", polycount.Default)
			}
			modelType, ok := meshy329Parameter(offering, "model_type")
			if !ok || !modelType.Fixed || string(modelType.Default) != `"smart-topology"` {
				t.Fatalf("model_type = %#v", modelType)
			}
			// Smart Topology output is triangle-only and these fields are
			// ignored or rejected with it.
			for _, absent := range []string{"topology", "should_remesh", "geometry_resolution", "image_enhancement", "remove_lighting", "decimation_mode"} {
				if _, exists := meshy329Parameter(offering, absent); exists {
					t.Fatalf("Smart Topology declares %s", absent)
				}
			}
		})
	}
	if _, exists := taskCatalog.Get("meshy/meshy-t2.5/multi-image-to-3d"); exists {
		t.Fatal("Multi-Image to 3D does not support meshy-t2.5")
	}
}

// Official prices (https://docs.meshy.ai/en/api/pricing, 2026-10-22).
func TestMeshy329PricesFollowOfficialTable(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for id, credits := range map[generation.OfferingID]int{
		"meshy/meshy-7.1/text-to-3d":           40, // preview 20 + Ultra 5 + refine 8K 15
		"meshy/meshy-7.1/image-to-3d":          40, // 8K textures 35 + Ultra 5
		"meshy/nano-banana-pro/text-to-image":  9,
		"meshy/nano-banana-pro/image-to-image": 9,
		"meshy/nano-banana-2/text-to-image":    6,
		"meshy/nano-banana-2/image-to-image":   6,
		"meshy/processing/retexture":           15, // 8K
		"meshy/processing/remesh":              5,
		"meshy/processing/rigging":             5,
		"meshy/processing/animation":           3,
	} {
		if got := meshy329Offering(t, taskCatalog, id).EstimatedCredits; got != credits {
			t.Errorf("%s estimated credits = %d, want %d", id, got, credits)
		}
	}
}

func TestMeshy329PromptLimitsFollowDocumentation(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for _, tc := range []struct {
		id        generation.OfferingID
		parameter string
		max       int
	}{
		{"meshy/meshy-7.1/text-to-3d", "prompt", 800},
		{"meshy/meshy-7.1/text-to-3d", "texture_prompt", 800},
		{"meshy/meshy-t2.5/text-to-3d", "prompt", 800},
		{"meshy/processing/retexture", "prompt", 800},
		{"meshy/prime/text-to-motion", "prompt", 400},
	} {
		parameter, ok := meshy329Parameter(meshy329Offering(t, taskCatalog, tc.id), tc.parameter)
		if !ok || parameter.MaxLength != tc.max {
			t.Errorf("%s %s max_length = %d, want %d", tc.id, tc.parameter, parameter.MaxLength, tc.max)
		}
	}
}

// Remesh v1 deprecated its resize and convert fields in favour of the Resize
// and Convert APIs; Retexture is pinned to Meshy 7, its newest model.
func TestMeshy329ProcessingWireFields(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	remesh := meshy329Offering(t, taskCatalog, "meshy/processing/remesh")
	for _, deprecated := range []string{"resize_height", "resize_longest_side", "auto_size", "origin_at"} {
		if _, exists := meshy329Parameter(remesh, deprecated); exists {
			t.Errorf("remesh still declares deprecated %s", deprecated)
		}
	}
	// convert_format_only went with #330: format completion uses Meshy
	// Convert, and no exemption is left (meshy_convert_330_test.go).
	if _, exists := meshy329Parameter(remesh, "convert_format_only"); exists {
		t.Fatal("remesh still declares convert_format_only")
	}
	for _, entry := range taskCatalog.Application().ProviderDeprecations {
		if len(entry.Exemptions) != 0 {
			t.Errorf("unexpected exemptions %#v", entry.Exemptions)
		}
	}
	retexture := meshy329Offering(t, taskCatalog, "meshy/processing/retexture")
	options := meshy329Options(t, retexture)
	if options["model_parameter"] != "ai_model" || options["model_parameter_value"] != "meshy-7" {
		t.Fatalf("retexture model = %v / %v", options["model_parameter"], options["model_parameter_value"])
	}
	style, ok := meshy329Parameter(retexture, "style_asset")
	if !ok || style.MaxBytes != 20000000 || !slices.Contains(style.MediaTypes, "image/webp") {
		t.Fatalf("style_asset = %#v", style)
	}
	multiview := meshy329Offering(t, taskCatalog, "meshy/meshy-7.1-multiview/image-to-3d")
	texture, ok := meshy329Parameter(multiview, "texture_assets")
	if !ok || texture.Type != "asset-array" || texture.WireName != "texture_image_urls" || texture.MinItems != 1 || texture.MaxItems != 4 ||
		texture.MaxBytes != 20000000 || texture.Required || !slices.Contains(texture.MediaTypes, "image/webp") {
		t.Fatalf("texture_assets = %#v", texture)
	}
}

// The single-image Image to 3D endpoint documents image_url only; several
// views go to the Multi-Image Offering. Retexture documents no
// negative_prompt. Both are in the Schema list, so an Offering that would send
// them does not load.
func TestMeshy329SendsOnlyDocumentedImageAndRetextureFields(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for _, id := range []generation.OfferingID{"meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/image-to-3d"} {
		offering := meshy329Offering(t, taskCatalog, id)
		references, ok := meshy329Parameter(offering, "reference_assets")
		if !ok || references.MinItems != 1 || references.MaxItems != 1 || references.WireName == "image_urls" {
			t.Errorf("%s reference_assets = %#v", id, references)
		}
		if _, _, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"reference_assets":["a","b"]}`)); err == nil {
			t.Errorf("%s accepted two reference images", id)
		}
	}
	multiview := meshy329Offering(t, taskCatalog, "meshy/meshy-7.1-multiview/image-to-3d")
	if references, _ := meshy329Parameter(multiview, "reference_assets"); references.WireName != "image_urls" || references.MaxItems != 4 {
		t.Fatalf("multi-image reference_assets = %#v", references)
	}
	retexture := meshy329Offering(t, taskCatalog, "meshy/processing/retexture")
	if _, exists := meshy329Parameter(retexture, "negative_prompt"); exists {
		t.Fatal("retexture still declares negative_prompt")
	}
	listed := map[string][]string{}
	for _, entry := range taskCatalog.Application().ProviderDeprecations {
		for _, field := range entry.Parameters {
			listed[field.Name] = field.Endpoints
		}
	}
	if !slices.Equal(listed["negative_prompt"], []string{"retexture"}) || !slices.Equal(listed["image_urls"], []string{"image-to-3d"}) {
		t.Fatalf("Schema list: negative_prompt %v, image_urls %v", listed["negative_prompt"], listed["image_urls"])
	}
}

// Model sources go to Meshy by data URI. The documentation allows 100 MB as a
// data URI (400 MB only from an http(s) URL), so the raw file stays at 70 MiB:
// its base64 data URI (about 97.9 MB) is still under 100 MB.
func TestMeshy329ModelSourceLimitsFollowDataURIBound(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	const limit = int64(70 << 20)
	for _, id := range []generation.OfferingID{"meshy/processing/retexture", "meshy/processing/remesh", "meshy/processing/rigging"} {
		offering := meshy329Offering(t, taskCatalog, id)
		asset, ok := meshy329Parameter(offering, "source_asset")
		if !ok || asset.MaxBytes != limit {
			t.Errorf("%s source_asset max_bytes = %d", id, asset.MaxBytes)
		}
		inline, _ := meshy329Options(t, offering)["inline_model_input"].(map[string]any)
		if bytes, _ := inline["max_bytes"].(float64); int64(bytes) != limit {
			t.Errorf("%s inline_model_input.max_bytes = %v", id, inline["max_bytes"])
		}
		encoded := int64(len("data:application/octet-stream;base64,")) + (limit+2)/3*4
		if encoded > 100_000_000 {
			t.Fatalf("data URI of %d bytes is %d characters", limit, encoded)
		}
	}
}

// The mock [mock:large_model] GLB reproduces a model too large for Meshy's
// processing (the #321 and #326 browser checks): it has to stay above the
// Meshy data-URI bound, and within what Tripo Smart Segmentation and the local
// conversion accept.
func TestMeshy329MockLargeModelStaysAboveMeshyBound(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for _, id := range []generation.OfferingID{"meshy/processing/retexture", "meshy/processing/remesh", "meshy/processing/rigging"} {
		asset, _ := meshy329Parameter(meshy329Offering(t, taskCatalog, id), "source_asset")
		if int64(mock.LargeModelBytes)-4096 <= asset.MaxBytes {
			t.Errorf("mock large model (%d bytes) is not above %s's %d", mock.LargeModelBytes, id, asset.MaxBytes)
		}
	}
	for _, id := range []generation.OfferingID{"tripo/geometry/smartsegment-model", "local/generated-glb/to-gltf"} {
		asset, _ := meshy329Parameter(meshy329Offering(t, taskCatalog, id), "source_asset")
		if int64(mock.LargeModelBytes) > asset.MaxBytes {
			t.Errorf("mock large model (%d bytes) is over %s's %d", mock.LargeModelBytes, id, asset.MaxBytes)
		}
	}
}

func meshy329SourceTask(id generation.TaskID, offering generation.Offering, size int64) generation.Task {
	return generation.Task{
		ID: id, OwnerID: "owner", State: generation.TaskSucceeded, Capability: offering.Capability,
		Offering: offering, CreatedAt: time.Now().Add(-time.Hour),
		Assets: []generation.Asset{{ID: generation.AssetID("asset-" + string(id)), Role: "model", Variant: "glb", StorageKey: "cas/" + string(id), MediaType: "model/gltf-binary", Size: size}},
	}
}

// Retexture and Remesh document Text to 3D, Image to 3D, Remesh and Retexture
// tasks as input_task_id; Rigging lists none, and Animation takes only Rigging
// tasks. A source the documentation does not list (Multi-Image to 3D) goes as
// the saved model file, and a service that cannot take that file says so.
func TestMeshy329ProcessingAcceptsOnlyDocumentedSourceTasks(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	sources := map[string]generation.Offering{}
	for _, id := range []generation.OfferingID{
		"meshy/meshy-7.1/text-to-3d", "meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/text-to-3d",
		"meshy/meshy-t2.5/image-to-3d", "meshy/meshy-7/text-to-3d", "meshy/meshy-7/image-to-3d",
		"meshy/meshy-7.1-multiview/image-to-3d", "meshy/processing/remesh", "meshy/processing/retexture",
	} {
		sources[string(id)] = meshy329Offering(t, taskCatalog, id)
	}
	for _, target := range []generation.OfferingID{"meshy/processing/retexture", "meshy/processing/remesh", "meshy/processing/rigging"} {
		offering := meshy329Offering(t, taskCatalog, target)
		for id, source := range sources {
			binding, err := generation.BindModelSource(offering, meshy329SourceTask("task-1", source, 1<<20), "", nil)
			if err != nil {
				t.Fatalf("%s from %s: %v", target, id, err)
			}
			want := "source_task"
			if id == "meshy/meshy-7.1-multiview/image-to-3d" {
				want = "source_asset"
			}
			if binding.Parameter != want {
				t.Errorf("%s from %s binds %s, want %s", target, id, binding.Parameter, want)
			}
		}
		// A Multi-Image result larger than the file bound cannot go at all.
		large := meshy329SourceTask("task-2", sources["meshy/meshy-7.1-multiview/image-to-3d"], 71<<20)
		if _, err := generation.BindModelSource(offering, large, "", nil); !isInvalidInput(err) {
			t.Errorf("%s accepted an oversized Multi-Image file: %v", target, err)
		}
	}
	animation := meshy329Offering(t, taskCatalog, "meshy/processing/animation")
	source, _ := meshy329Parameter(animation, "source_task")
	if !slices.Equal(source.TaskOfferings, []generation.OfferingID{"meshy/processing/rigging"}) {
		t.Fatalf("animation task_offerings = %v", source.TaskOfferings)
	}
}

func isInvalidInput(err error) bool {
	var domain *generation.Error
	return errors.As(err, &domain) && domain.Code == generation.ErrInvalidInput
}
