package catalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The endpoint contract is taken from the current Tripo Smart Segmentation
// documentation, not inferred from other Tripo segmentation Offerings.
func TestTripoSmartSegmentOfferingsAreDistinctAndEnabled(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Application().Runtime.Frontend.ModelProcessingCapabilities, "mesh-segmentation") {
		t.Fatal("model processing panel cannot discover mesh segmentation from Product Schema")
	}
	for _, test := range []struct {
		id, capability, segType, source string
		credits, total, maskMinimum     int
	}{
		// Official 2026-10-21 pricing: Smart Segmentation image 85, model 55;
		// each advanced format export (8192/PNG) 10.
		{"tripo/geometry/smartsegment-image", "image-to-3d", "image", "image", 85, 135, 1},
		{"tripo/geometry/smartsegment-model", "mesh-segmentation", "model", "source_asset", 55, 105, 0},
	} {
		t.Run(test.segType, func(t *testing.T) {
			o, ok := c.Get(generation.OfferingID(test.id))
			if !ok || !o.Enabled || o.Default || string(o.Capability) != test.capability || o.EstimatedCredits != test.credits || o.Revision != "2026-10-22.185.5" {
				t.Fatalf("incorrect Smart Segmentation Offering %s: %+v", test.id, o)
			}
			var options struct {
				Protocol         string `json:"protocol"`
				Endpoint         string `json:"endpoint"`
				UploadEndpoint   string `json:"upload_endpoint"`
				ObserveEndpoint  string `json:"observe_endpoint"`
				ConvertEndpoint  string `json:"convert_endpoint"`
				NativeConversion struct {
					SourceAlias    string         `json:"source_alias"`
					MaxSourceBytes int64          `json:"max_source_bytes"`
					Parameters     map[string]any `json:"parameters"`
					Formats        []struct {
						Format, Role, Variant, MediaType string
					} `json:"formats"`
				} `json:"native_conversion"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &options); err != nil || options.Protocol != "smartsegment" || options.Endpoint != "mesh/smartsegment" || options.UploadEndpoint != "files" || options.ObserveEndpoint != "tasks/{task_id}" || options.ConvertEndpoint != "models/convert" {
				t.Fatalf("incorrect Smart Segmentation route %s: %+v, %v", test.id, options, err)
			}
			// Exports consume the segmented GLB URL with the same fixed advanced
			// parameters as the other #185 geometry candidates.
			nc := options.NativeConversion
			wantParameters := map[string]any{"texture_size": float64(8192), "texture_format": "PNG", "with_animation": false, "bake": true, "fbx_preset": "blender"}
			if nc.SourceAlias != "seg_model_url" || nc.MaxSourceBytes != 150000000 || !reflect.DeepEqual(nc.Parameters, wantParameters) || len(nc.Formats) != 5 {
				t.Fatalf("%s native export declaration: %+v", test.id, nc)
			}
			var fixedType bool
			for _, p := range o.Parameters {
				if p.WireName == "seg_type" {
					fixedType = p.Fixed && string(p.Default) == `"`+test.segType+`"`
				}
			}
			if !fixedType {
				t.Fatalf("%s does not freeze seg_type=%s", test.id, test.segType)
			}
			if o.Cost == nil || o.Cost.BaseCredits != test.credits || o.Cost.ExtraCredits != 10 || o.Cost.SelectionParameter != "output_formats" || o.Cost.ConsentParameter != "authorize_extra_credits" || o.CostPolicy == nil || o.CostPolicy.MaxAuthorizedExtraCredits != 50 {
				t.Fatalf("%s cost declaration: %+v %+v", test.id, o.Cost, o.CostPolicy)
			}
			input := json.RawMessage(`{"` + test.source + `":"owned","authorize_extra_credits":true}`)
			if _, _, err := c.Normalize(o.Capability, o.ID, input); err != nil {
				t.Fatalf("enabled %s rejected a complete input: %v", test.id, err)
			}
			normalized, err := normalizeInput(o, input)
			if err != nil {
				t.Fatal(err)
			}
			if got := o.EstimateTotalCredits(normalized); got != test.total {
				t.Fatalf("%s full delivery quote %d, want %d", test.id, got, test.total)
			}
			for _, bad := range []string{`{"` + test.source + `":"owned"}`, `{"` + test.source + `":"owned","authorize_extra_credits":false}`} {
				if _, err := normalizeInput(o, json.RawMessage(bad)); err == nil {
					t.Fatalf("%s accepted unconsented delivery: %s", test.id, bad)
				}
			}
			// The export set is fixed: a narrower selection can never freeze a
			// cheaper partial delivery.
			partial := json.RawMessage(`{"` + test.source + `":"owned","authorize_extra_credits":true,"output_formats":["FBX"]}`)
			if normalized, err := normalizeInput(o, partial); err == nil && o.EstimateTotalCredits(normalized) != test.total {
				t.Fatalf("%s froze a partial export quote", test.id)
			}
			// GLB and five exports are required slots. The PNG mask is a separate
			// non-model slot: required in image mode (official example), and
			// enforced only when returned in model mode (no official example).
			want := map[string]struct {
				media   string
				minimum int
			}{
				"model:glb":            {"model/gltf-binary", 1},
				"converted_model:fbx":  {"model/fbx", 1},
				"converted_model:obj":  {"application/zip", 1},
				"converted_model:stl":  {"model/stl", 1},
				"converted_model:usdz": {"model/vnd.usdz+zip", 1},
				"converted_model:3mf":  {"model/3mf", 1},
				"mask:png":             {"image/png", test.maskMinimum},
			}
			if len(o.Outputs) != len(want) {
				t.Fatalf("%s has unexpected output count: %d", test.id, len(o.Outputs))
			}
			for _, output := range o.Outputs {
				expected, ok := want[output.Role+":"+output.Variant]
				if !ok || output.Delivery != "required" || output.Minimum == nil || *output.Minimum != expected.minimum || len(output.MediaTypes) != 1 || output.MediaTypes[0] != expected.media || output.MaxBytes <= 0 {
					t.Fatalf("%s output slot drifted: %+v", test.id, output)
				}
				delete(want, output.Role+":"+output.Variant)
			}
		})
	}
}

func TestTripoGeometryCompletionSourcesRequireSameSession(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const chinese = "仅接受本人同会话已成功的原生 Tripo 分割任务；补全全部部件，不接受文件、普通建模任务、Smart Segmentation 外层任务或任意部件名称。"
	const english = "Only accepts your own successful native Tripo segmentation task from this same session. All parts are completed; files, ordinary modeling tasks, Smart Segmentation outer tasks, and arbitrary part names are not accepted."
	if got := c.Application().Localization.Bundles["en"].OfferingTexts[chinese]; got != english {
		t.Fatalf("English same-session source description = %q", got)
	}
	for _, id := range []string{"tripo/geometry/complete", "tripo/geometry/quick-cap"} {
		o, ok := c.Get(generation.OfferingID(id))
		if !ok || !o.Enabled || o.Default {
			t.Fatalf("unexpected completion Offering state %s: %+v", id, o)
		}
		var source *generation.Parameter
		for i := range o.Parameters {
			if o.Parameters[i].Name == "source_task" {
				source = &o.Parameters[i]
				break
			}
		}
		if source == nil || !source.Required || !source.SameProvider || !source.SameSession {
			t.Fatalf("%s must bind a required same-provider, same-session source task: %+v", id, source)
		}
		if source.Description != chinese {
			t.Fatalf("%s source description must state the same-session boundary: %q", id, source.Description)
		}
	}
}
