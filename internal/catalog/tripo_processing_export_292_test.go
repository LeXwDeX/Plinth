package catalog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #292: the files a Tripo processing step exports are written at the texture
// size the session purpose allows. Every Offering that exports declares one public
// parameter, bound to the export request, with the texture-size semantic the
// purpose budget (#129) bounds. The export stays lossless PNG, which is not the
// upstream default format, so the upstream bills each export as an advanced
// conversion at any size: the quote of a step does not depend on the size.

type exportOffering struct {
	id     string
	source string // the input parameter that names the step's source
	// total is the quote of a default step: the base price plus one advanced
	// export (10) per format, as before the size became a parameter.
	total int
	// extra are the other inputs a step needs.
	extra map[string]any
}

var processingExportOfferings = []exportOffering{
	{"tripo/processing/texture", "source_task", 80, map[string]any{"prompt": "silver"}},
	{"tripo/processing/rigging", "source_task", 35, nil},
	{"tripo/processing/rigging-v3", "source_task", 35, nil},
	{"tripo/geometry/decimate-triangle", "source_task", 85, nil},
	{"tripo/geometry/decimate-quad", "source_task", 90, nil},
	{"tripo/geometry/segment", "source_task", 90, nil},
	{"tripo/geometry/complete", "source_task", 100, nil},
	{"tripo/geometry/quick-cap", "source_task", 80, nil},
	{"tripo/geometry/smartsegment-image", "image", 135, nil},
	{"tripo/geometry/smartsegment-model", "source_asset", 105, nil},
}

func TestTripoProcessingExportsBindTheTextureSizeToThePurposeBudget(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := c.Application().Chat.Model3DUsage
	limit, bounded := usage.BudgetLimit(schema.SemanticTextureSize)
	if !bounded || limit.Bound != BudgetBoundMaxTextureSize {
		t.Fatalf("the texture size is not a bounded semantic: %+v", limit)
	}
	for _, test := range processingExportOfferings {
		t.Run(test.id, func(t *testing.T) {
			o, ok := c.Get(generation.OfferingID(test.id))
			if !ok || o.Revision != "2026-11-12.292.1" {
				t.Fatalf("Offering %s %s", o.ID, o.Revision)
			}
			var size *schema.Parameter
			for index := range o.Parameters {
				if o.Parameters[index].Name == "export_texture_size" {
					if size != nil {
						t.Fatal("the size is declared twice")
					}
					size = &o.Parameters[index]
				}
			}
			if size == nil {
				t.Fatal("the Offering declares no export texture size")
			}
			// A public, selectable choice the purpose budget fits: the Agent's plan
			// steps, the direct task API and the 加工 panel all carry it, because it
			// is not an advised parameter and not hidden or fixed.
			if size.Type != "select" || size.Semantic != schema.SemanticTextureSize || string(size.Default) != `"8k"` ||
				size.Fixed || size.AdjustmentPolicy != "" || size.WireName != "" || size.Required ||
				size.Presentation == nil || size.Presentation.Surface != schema.SurfaceProvider {
				t.Fatalf("export texture size declaration: %+v", size)
			}
			if !o.AgentParameterAllowed(*size) || !o.UserParameterAllowed(*size) {
				t.Fatal("the export texture size is not a parameter the Agent and the person may set")
			}
			var values []string
			wire := map[string]string{}
			for _, option := range size.Options {
				values = append(values, option.Value)
				wire[option.Value] = string(size.WireValues[option.Value])
				if _, measured := limit.OptionValues[option.Value]; !measured {
					t.Fatalf("option %s has no pixel size for the budget", option.Value)
				}
			}
			if !reflect.DeepEqual(values, []string{"2k", "4k", "8k"}) ||
				!reflect.DeepEqual(wire, map[string]string{"2k": "2048", "4k": "4096", "8k": "8192"}) {
				t.Fatalf("options %v wire %v", values, wire)
			}
			// What the budget measures is what the export is written with.
			for option, pixels := range limit.OptionValues {
				if mapped, declared := size.WireValues[option]; declared && string(mapped) != strconv.Itoa(int(pixels)) {
					t.Fatalf("option %s is %v pixels to the budget and %s to the upstream", option, pixels, mapped)
				}
			}

			var options struct {
				NativeConversion struct {
					Parameters map[string]any   `json:"parameters"`
					Bindings   []map[string]any `json:"bindings"`
				} `json:"native_conversion"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			native := options.NativeConversion
			if !reflect.DeepEqual(native.Bindings, []map[string]any{{"parameter": "export_texture_size", "wire": "texture_size"}}) {
				t.Fatalf("bindings = %v", native.Bindings)
			}
			if _, constant := native.Parameters["texture_size"]; constant {
				t.Fatal("the size is both a constant and a binding")
			}
			// Lossless PNG is the format of every export. It is not the upstream
			// default (JPEG), so the export is an advanced conversion at any size and
			// the legacy per-export price below is exactly right for every size.
			if native.Parameters["texture_format"] != "PNG" {
				t.Fatalf("export texture format = %v, want the fixed PNG", native.Parameters["texture_format"])
			}
		})
	}
}

// The size changes the files, not the price: the quote of a step is the same for
// every size, and the default quote is the one before the parameter existed.
func TestTripoProcessingExportPriceDoesNotDependOnTheTextureSize(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range processingExportOfferings {
		t.Run(test.id, func(t *testing.T) {
			o, ok := c.Get(generation.OfferingID(test.id))
			if !ok {
				t.Fatalf("missing %s", test.id)
			}
			if o.NativePricing != nil {
				t.Fatal("a processing export is priced by its cost declaration; the size is no price component")
			}
			var first int
			for _, size := range []string{"", "2k", "4k", "8k"} {
				input := map[string]any{test.source: "owned", "authorize_extra_credits": true}
				for name, value := range test.extra {
					input[name] = value
				}
				if size != "" {
					input["export_texture_size"] = size
				}
				encoded, _ := json.Marshal(input)
				// The Offering's own normalization, so the disabled quad decimation
				// is priced too.
				normalized, err := normalizeInput(o, encoded)
				if err != nil {
					t.Fatalf("size %q: %v", size, err)
				}
				total := o.EstimateTotalCredits(normalized)
				if total != test.total {
					t.Fatalf("size %q: quote %d, want %d", size, total, test.total)
				}
				if size == "" {
					first = total
					var frozen map[string]any
					if err := json.Unmarshal(normalized, &frozen); err != nil || frozen["export_texture_size"] != "8k" {
						t.Fatalf("the default was not frozen as 8k: %s", normalized)
					}
				}
				if total != first {
					t.Fatalf("size %q changed the quote", size)
				}
				if _, err := AuthorizeExtraCredits(o, normalized); err != nil {
					t.Fatalf("size %q: the consent no longer covers the exports: %v", size, err)
				}
			}
			// The export price is one advanced conversion (10) per format.
			if o.Cost == nil || o.Cost.ExtraCredits != 10 {
				t.Fatalf("export price = %+v", o.Cost)
			}
		})
	}
}

// The processing exports follow the same purposes as the generation exports
// (#278): where a game purpose asks the generation export for PNG at 2K, the
// processing export of the same session is PNG at the purpose's texture budget,
// which is the same 2K for the platforms the purpose names there.
func TestProcessingExportsAgreeWithTheGenerationExportOfTheSamePurpose(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := c.Application().Chat.Model3DUsage
	limit, _ := usage.BudgetLimit(schema.SemanticTextureSize)
	rigging, _ := c.Get("tripo/processing/rigging-v3")
	var size schema.Parameter
	for _, parameter := range rigging.Parameters {
		if parameter.Name == "export_texture_size" {
			size = parameter
		}
	}
	const generationOffering = "tripo/tripo-v3.1-multiview/image-to-3d"
	for _, profile := range usage.Profiles {
		override, declared := profile.ParameterOverrides[generationOffering]["export_texture_format"]
		if !declared {
			continue
		}
		// The purposes whose generation export is lossless PNG are the ones whose
		// processing export (always PNG) agrees with it.
		if string(override) != `"PNG"` {
			t.Fatalf("%s: the generation export format is %s", profile.ID, override)
		}
		generationSize := profile.ParameterOverrides[generationOffering]["export_texture_size"]
		maximum, _ := limit.Maximum(profile.Budget)
		fitted, fits := limit.Fit(rigging, size, size.Default, nil, maximum)
		if !fits {
			t.Fatalf("%s: the default size already fits a budget of %v", profile.ID, maximum)
		}
		if string(fitted) != string(generationSize) {
			t.Fatalf("%s: the generation export is %s, the processing export %s", profile.ID, generationSize, fitted)
		}
	}
}

// The outputs the purpose sizes are declared on the Offering: the exports that
// carry textures, never the native GLB (its resolution is the upstream's), the
// untextured STL and 3MF, the mask or the thumbnail.
func TestTripoProcessingExportsDeclareTheOutputsThePurposeSizes(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range processingExportOfferings {
		t.Run(test.id, func(t *testing.T) {
			o, ok := c.Get(generation.OfferingID(test.id))
			if !ok {
				t.Fatalf("missing %s", test.id)
			}
			sized := map[string]bool{}
			for _, output := range o.Outputs {
				if output.TextureSizeParameter == "" {
					continue
				}
				if output.TextureSizeParameter != "export_texture_size" || output.Variant == "" {
					t.Fatalf("output %s/%s declares %q", output.Role, output.Variant, output.TextureSizeParameter)
				}
				sized[output.Variant] = true
			}
			want := map[string]bool{"fbx": true, "obj": true, "usdz": true}
			if test.id == "tripo/processing/rigging" || test.id == "tripo/processing/rigging-v3" {
				want = map[string]bool{"fbx": true}
			}
			if !reflect.DeepEqual(sized, want) {
				t.Fatalf("sized outputs = %v, want %v", sized, want)
			}
		})
	}
}

// A sized output names a texture-size select of its own Offering, or the Schema
// does not load: the purpose would otherwise judge a deliverable by a value that
// is not its size.
func TestSizedOutputDeclarationFailsClosed(t *testing.T) {
	cases := map[string]string{
		"unknown parameter":        "no_such_parameter",
		"select without semantic":  "rig_type",
		"a task, not a size":       "source_task",
		"a boolean, not a size":    "authorize_extra_credits",
		"the fixed output formats": "output_formats",
	}
	for name, parameter := range cases {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(defaultCatalog, &document); err != nil {
				t.Fatal(err)
			}
			var fbx map[string]any
			for _, raw := range document["offerings"].([]any) {
				offering := raw.(map[string]any)
				if offering["id"] != "tripo/processing/rigging-v3" {
					continue
				}
				for _, rawOutput := range offering["outputs"].([]any) {
					if output := rawOutput.(map[string]any); output["variant"] == "fbx" {
						fbx = output
					}
				}
			}
			if fbx == nil {
				t.Fatal("the rigging FBX output is missing")
			}
			fbx["texture_size_parameter"] = parameter
			raw, _ := json.Marshal(document)
			if _, err := Load(bytes.NewReader(raw)); err == nil {
				t.Fatalf("a sized output naming %q was accepted", parameter)
			}
		})
	}
}

// The sentences the usage check says about a sized deliverable name exactly
// their own placeholders.
func TestExportTextureSentencesNameTheirPlaceholders(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	production := c.Application().Chat.ModelReview.Inspect.Production
	for _, want := range []string{"{native}", "{formats}", "{export}"} {
		if !strings.Contains(production.ExportTextureText, want) {
			t.Fatalf("export_texture_text %q lacks %s", production.ExportTextureText, want)
		}
	}
	for _, want := range []string{"{formats}", "{export}", "{maximum}"} {
		if !strings.Contains(production.ExportTextureFixText, want) {
			t.Fatalf("export_texture_fix_text %q lacks %s", production.ExportTextureFixText, want)
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"a note without the native edge": func(p map[string]any) {
			p["export_texture_text"] = "已按用途导出为 {export} 像素（{formats}）。"
		},
		"a note with a stranger":           func(p map[string]any) { p["export_texture_text"] = production.ExportTextureText + "{purpose}" },
		"a fix without the bound":          func(p map[string]any) { p["export_texture_fix_text"] = "{formats} 的 {export} 像素太大。" },
		"a fix with a placeholder to fill": func(p map[string]any) { p["export_texture_fix_text"] = production.ExportTextureFixText + "{native}" },
	} {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(defaultCatalog, &document); err != nil {
				t.Fatal(err)
			}
			mutate(productionDocument(document))
			raw, _ := json.Marshal(document)
			if _, err := Load(bytes.NewReader(raw)); err == nil {
				t.Fatal("an invalid sentence was accepted")
			}
		})
	}
}
