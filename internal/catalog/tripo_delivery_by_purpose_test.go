package catalog_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #278: the formats a Tripo task delivers follow the purpose. The format set is
// a frozen input (output_formats); the converted-model slots the Task must
// deliver are declared in the Offering with required_when on that input, and
// the kernel pins the decision into the Task's Offering. The export options
// (texture format and size, pivot, FBX preset) default to the upstream defaults,
// so a task that asks for nothing special is priced and requested exactly as it
// was before the options existed; a purpose opts into the advanced export in its
// own declaration. These tests hold the Schema side: the slot declarations, the
// per-purpose sets and options, the export option declarations and their price.

type exportOffering struct {
	id         string
	capability schema.Capability
	input      map[string]any
	// defaultTotal is the quote of the default input, as it was on main.
	defaultTotal string
}

// The three Offerings that export selected formats after the model exists.
var exportOfferings = []exportOffering{
	{"tripo/tripo-v3.1-full/text-to-3d", schema.TextTo3D, map[string]any{"prompt": "a crate"}, "90"},
	{"tripo/tripo-v3.1-full/image-to-3d", schema.ImageTo3D, map[string]any{"reference_assets": []string{"asset-1"}}, "100"},
	{"tripo/tripo-v3.1-multiview/image-to-3d", schema.ImageTo3D, map[string]any{
		"reference_assets": []string{"asset-1", "asset-2"}, "reference_views": []string{"front", "left"}}, "100"},
}

var allFormats = []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}

func loadDefaultCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func withInput(base map[string]any, changes map[string]any) json.RawMessage {
	merged := map[string]any{"authorize_extra_credits": true}
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range changes {
		merged[name] = value
	}
	encoded, _ := json.Marshal(merged)
	return encoded
}

// requiredConvertedVariants is the set of converted-model slots a frozen input
// requires: every required slot whose required_when holds, which is what the
// kernel pins into the Task's Offering (minimum 0 for the others).
func requiredConvertedVariants(t *testing.T, offering schema.Offering, frozen json.RawMessage) []string {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(frozen, &values); err != nil {
		t.Fatal(err)
	}
	var variants []string
	for _, output := range offering.Outputs {
		if output.Role != "converted_model" || output.Delivery != schema.AssetDeliveryRequired {
			continue
		}
		if output.RequiredWhen == nil || schema.ConditionMatches(*output.RequiredWhen, values) {
			variants = append(variants, output.Variant)
		}
	}
	slices.Sort(variants)
	return variants
}

func variantsOf(formats ...string) []string {
	variants := make([]string, 0, len(formats))
	for _, format := range formats {
		variants = append(variants, "converted."+strings.ToLower(format))
	}
	slices.Sort(variants)
	return variants
}

// conversionCharge is what a frozen input pays for its conversions.
func conversionCharge(t *testing.T, offering schema.Offering, frozen json.RawMessage) string {
	t.Helper()
	quote, err := offering.EvaluateNativeQuote(frozen)
	if err != nil || quote == nil {
		t.Fatalf("%s: quote %+v err %v", offering.ID, quote, err)
	}
	for _, charge := range quote.Components {
		if charge.ID == "format_conversions" {
			return charge.Amount
		}
	}
	return ""
}

func TestConvertedSlotsAreRequiredExactlyForTheSelectedFormats(t *testing.T) {
	c := loadDefaultCatalog(t)
	for _, tc := range exportOfferings {
		t.Run(tc.id, func(t *testing.T) {
			for name, formats := range map[string][]string{
				"every format":         allFormats,
				"game: GLB and FBX":    {"GLTF", "FBX"},
				"print: GLB, STL, 3MF": {"GLTF", "STL", "3MF"},
				"GLB only":             {"GLTF"},
			} {
				offering, frozen, err := c.Normalize(tc.capability, schema.OfferingID(tc.id),
					withInput(tc.input, map[string]any{"output_formats": formats}))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got, want := requiredConvertedVariants(t, offering, frozen), variantsOf(formats...); !slices.Equal(got, want) {
					t.Errorf("%s: required converted slots = %v, want %v", name, got, want)
				}
			}
			// The default is the display set: all six, as before.
			offering, frozen, err := c.Normalize(tc.capability, schema.OfferingID(tc.id), withInput(tc.input, nil))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := requiredConvertedVariants(t, offering, frozen), variantsOf(allFormats...); !slices.Equal(got, want) {
				t.Errorf("default: required converted slots = %v, want %v", got, want)
			}
			// The GLB of the model role comes from the GLTF conversion, so its slot is
			// never conditional; every other slot is conditional on its own format.
			for _, output := range offering.Outputs {
				if output.Role != "converted_model" {
					continue
				}
				format := strings.ToUpper(strings.TrimPrefix(output.Variant, "converted."))
				var want *schema.ParameterCondition
				if format != "GLTF" {
					want = &schema.ParameterCondition{Parameter: "output_formats", Operator: "contains", Value: json.RawMessage(`"` + format + `"`)}
				}
				if (output.RequiredWhen == nil) != (want == nil) ||
					(want != nil && (output.RequiredWhen.Parameter != want.Parameter || output.RequiredWhen.Operator != want.Operator ||
						string(output.RequiredWhen.Value) != string(want.Value))) {
					t.Errorf("%s: required_when = %+v, want %+v", output.Variant, output.RequiredWhen, want)
				}
			}
		})
	}
}

func TestAnUnrequiredFormatSelectionIsRefusedAtNormalization(t *testing.T) {
	c := loadDefaultCatalog(t)
	for _, tc := range exportOfferings {
		for name, formats := range map[string][]string{
			"without the GLB conversion": {"FBX", "STL"},
			"empty":                      {},
			"repeated":                   {"GLTF", "FBX", "FBX"},
			"unknown":                    {"GLTF", "BLEND"},
		} {
			if _, _, err := c.Normalize(tc.capability, schema.OfferingID(tc.id),
				withInput(tc.input, map[string]any{"output_formats": formats})); err == nil {
				t.Errorf("%s %s: accepted %v", tc.id, name, formats)
			}
		}
	}
}

// The price follows the selection and the export options. A conversion is the
// basic one (5 credits) while every export option sits on its upstream default;
// it becomes the advanced one (10 credits) as soon as the texture format, the
// texture size or the pivot is off its default (Tripo pricing page, read
// 2026-11-12: "any non-default value" of texture_format, texture_size,
// pivot_to_center_bottom, ... is billed at 10 credits). The FBX preset is not on
// that list, and the default input is priced as it was before the options existed.
func TestConversionsArePricedBasicUnlessAnAdvancedExportOptionIsSet(t *testing.T) {
	c := loadDefaultCatalog(t)
	advanced := map[string]any{"export_texture_format": "PNG", "export_texture_size": "2k", "export_pivot_bottom_center": true}
	for _, tc := range exportOfferings {
		t.Run(tc.id, func(t *testing.T) {
			offering, _ := c.Get(schema.OfferingID(tc.id))
			if quote, err := offering.DefaultNativeQuote(nil); err != nil || quote == nil || quote.TotalAmount != tc.defaultTotal {
				t.Fatalf("default quote = %+v err %v, want the pre-#278 total %s", quote, err, tc.defaultTotal)
			}
			type priced struct {
				name    string
				formats []string
				changes map[string]any
				want    string
			}
			for _, test := range []priced{
				{"display set, upstream defaults", allFormats, nil, "30"},
				{"game set, upstream defaults", []string{"GLTF", "FBX"}, nil, "10"},
				{"print set, upstream defaults", []string{"GLTF", "STL", "3MF"}, nil, "15"},
				{"GLB only, upstream defaults", []string{"GLTF"}, nil, "5"},
				{"game set, the game purpose's export", []string{"GLTF", "FBX"}, advanced, "20"},
				{"display set, the game purpose's export", allFormats, advanced, "60"},
				{"PNG alone", []string{"GLTF", "FBX"}, map[string]any{"export_texture_format": "PNG"}, "20"},
				{"a smaller texture alone", []string{"GLTF", "FBX"}, map[string]any{"export_texture_size": "2k"}, "20"},
				{"a larger texture alone", []string{"GLTF", "FBX"}, map[string]any{"export_texture_size": "8k"}, "20"},
				{"the bottom pivot alone", []string{"GLTF", "FBX"}, map[string]any{"export_pivot_bottom_center": true}, "20"},
				{"the explicit upstream defaults", []string{"GLTF", "FBX"}, map[string]any{
					"export_texture_format": "JPEG", "export_texture_size": "4k", "export_pivot_bottom_center": false, "export_fbx_preset": "blender"}, "10"},
				{"another FBX preset alone", []string{"GLTF", "FBX"}, map[string]any{"export_fbx_preset": "mixamo"}, "10"},
			} {
				changes := map[string]any{"output_formats": test.formats}
				for name, value := range test.changes {
					changes[name] = value
				}
				offering, frozen, err := c.Normalize(tc.capability, schema.OfferingID(tc.id), withInput(tc.input, changes))
				if err != nil {
					t.Fatalf("%s: %v", test.name, err)
				}
				if got := conversionCharge(t, offering, frozen); got != test.want {
					t.Errorf("%s: conversions priced %q, want %s", test.name, got, test.want)
				}
			}
			upper, err := offering.UpperNativeQuote()
			if err != nil || upper == nil {
				t.Fatalf("upper quote %+v err %v", upper, err)
			}
			for _, charge := range upper.Components {
				if charge.ID == "format_conversions" && charge.Amount != "60" {
					t.Errorf("the upper bound prices the conversions at %s, want six advanced ones (60)", charge.Amount)
				}
			}
		})
	}
}

// Each purpose names its delivered format set and export options in the Schema.
// The game purposes ask for the GLB and FBX only, with lossless PNG textures at
// 2K and the origin at the bottom centre (the advanced export); print keeps STL
// and 3MF next to the GLB on the basic conversion (its STL and 3MF carry no
// texture); display and web/AR stay exactly what they were.
func TestEveryPurposeDeclaresItsFormatSet(t *testing.T) {
	c := loadDefaultCatalog(t)
	type want struct {
		formats []string
		texture string
		size    string
		pivot   bool
		charge  string
	}
	expected := map[string]want{
		"render":         {allFormats, "JPEG", "4k", false, "30"},
		"web_ar":         {allFormats, "JPEG", "4k", false, "30"},
		"game_character": {[]string{"GLTF", "FBX"}, "PNG", "2k", true, "20"},
		"game_prop":      {[]string{"GLTF", "FBX"}, "PNG", "2k", true, "20"},
		"print":          {[]string{"GLTF", "STL", "3MF"}, "JPEG", "4k", false, "15"},
	}
	exports := map[string]exportOffering{}
	for _, tc := range exportOfferings {
		exports[tc.id] = tc
	}
	seen := map[string]int{}
	for _, profile := range c.Application().Chat.Model3DUsage.Profiles {
		want, declared := expected[profile.ID]
		if !declared {
			t.Fatalf("purpose %q has no expectation for its format set", profile.ID)
		}
		for _, ids := range profile.RecommendedOfferings {
			for _, id := range ids {
				tc, exporting := exports[id]
				if !exporting {
					continue
				}
				seen[profile.ID]++
				overrides := map[string]any{}
				for name, raw := range profile.ParameterOverrides[id] {
					var value any
					if err := json.Unmarshal(raw, &value); err != nil {
						t.Fatal(err)
					}
					overrides[name] = value
				}
				offering, frozen, err := c.Normalize(tc.capability, schema.OfferingID(id), withInput(tc.input, overrides))
				if err != nil {
					t.Fatalf("%s / %s: %v", profile.ID, id, err)
				}
				var values struct {
					Formats []string `json:"output_formats"`
					Texture string   `json:"export_texture_format"`
					Size    string   `json:"export_texture_size"`
					Pivot   bool     `json:"export_pivot_bottom_center"`
				}
				if err := json.Unmarshal(frozen, &values); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(values.Formats, want.formats) || values.Texture != want.texture || values.Size != want.size || values.Pivot != want.pivot {
					t.Errorf("%s / %s: formats %v texture %s %s pivot %v, want %v %s %s %v", profile.ID, id,
						values.Formats, values.Texture, values.Size, values.Pivot, want.formats, want.texture, want.size, want.pivot)
				}
				if got := conversionCharge(t, offering, frozen); got != want.charge {
					t.Errorf("%s / %s: conversions priced %q, want %s", profile.ID, id, got, want.charge)
				}
			}
		}
	}
	for profile := range expected {
		if seen[profile] == 0 {
			t.Errorf("purpose %q recommends no exporting Tripo Offering, so its format set is untested", profile)
		}
	}
}

// Every export request is written from the frozen Offering: four bound inputs
// (texture format, texture size, pivot, FBX preset, the preset for FBX only),
// each left out while it equals the upstream default. The options are Offering
// parameters the purpose and the Agent can see, never provider code, and their
// defaults are the upstream defaults, so an untouched Offering asks for the plain
// conversion it always asked for.
func TestExportOptionsAreDeclaredInTheOffering(t *testing.T) {
	c := loadDefaultCatalog(t)
	type binding struct {
		wire            string
		upstreamDefault string
		formats         []string
	}
	want := map[string]binding{
		"export_texture_format":      {"texture_format", `"JPEG"`, nil},
		"export_texture_size":        {"texture_size", `4096`, nil},
		"export_pivot_bottom_center": {"pivot_to_center_bottom", `false`, nil},
		"export_fbx_preset":          {"fbx_preset", `"blender"`, []string{"FBX"}},
	}
	defaults := map[string]string{
		"export_texture_format": `"JPEG"`, "export_texture_size": `"4k"`, "export_pivot_bottom_center": `false`, "export_fbx_preset": `"blender"`,
	}
	for _, tc := range exportOfferings {
		offering, _ := c.Get(schema.OfferingID(tc.id))
		var options struct {
			NativeConversion struct {
				Parameters map[string]any `json:"parameters"`
				Bindings   []struct {
					Parameter       string          `json:"parameter"`
					Wire            string          `json:"wire"`
					Formats         []string        `json:"formats"`
					UpstreamDefault json.RawMessage `json:"upstream_default"`
				} `json:"bindings"`
			} `json:"native_conversion"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		if len(options.NativeConversion.Parameters) != 0 || len(options.NativeConversion.Bindings) != len(want) {
			t.Errorf("%s: declares constants %v and %d bindings, want no constants and %d bindings", tc.id,
				options.NativeConversion.Parameters, len(options.NativeConversion.Bindings), len(want))
		}
		for _, bound := range options.NativeConversion.Bindings {
			expected, known := want[bound.Parameter]
			if !known || bound.Wire != expected.wire || string(bound.UpstreamDefault) != expected.upstreamDefault || !slices.Equal(bound.Formats, expected.formats) {
				t.Errorf("%s: binding %+v, want %+v", tc.id, bound, expected)
			}
			parameter, found := parameterOf(offering, bound.Parameter)
			if !found || !offering.AgentParameterAllowed(parameter) || parameter.Presentation == nil ||
				parameter.Presentation.Surface != schema.SurfaceReadOnly || parameter.AdjustmentPolicy != "advised" {
				t.Errorf("%s: bound parameter %s is not a public advised parameter", tc.id, bound.Parameter)
			}
			// The Offering default is the upstream default (mapped to the wire value
			// when the parameter has wire_values), so a default request is the basic one.
			if string(parameter.Default) != defaults[bound.Parameter] {
				t.Errorf("%s: %s defaults to %s, want the upstream default %s", tc.id, bound.Parameter, parameter.Default, defaults[bound.Parameter])
			}
		}
		size, _ := parameterOf(offering, "export_texture_size")
		// The size has no texture-size semantic on purpose: the purpose budget would
		// otherwise move every purpose with a smaller budget (web/AR) to 2K, and with
		// it to the advanced price, so a purpose that wants a size names it itself.
		if size.Semantic != "" {
			t.Errorf("%s: export_texture_size carries the %q semantic", tc.id, size.Semantic)
		}
		mapped := map[string]string{}
		for option, wire := range size.WireValues {
			mapped[option] = string(wire)
		}
		if mapped["2k"] != "2048" || mapped["4k"] != "4096" || mapped["8k"] != "8192" {
			t.Errorf("%s: texture size wire values = %v", tc.id, mapped)
		}
		// The price upgrade reads exactly the three advanced fields, not the preset.
		selection := offering.NativePricing.Selection
		if selection == nil || selection.Amount != "5" || selection.Upgrade == nil || selection.Upgrade.Amount != "10" {
			t.Fatalf("%s: selection = %+v, want 5 upgraded to 10", tc.id, selection)
		}
		var advanced []string
		for _, condition := range selection.Upgrade.WhenAny {
			advanced = append(advanced, condition.Parameter+" "+condition.Operator+" "+string(condition.Value))
		}
		slices.Sort(advanced)
		if wantAdvanced := []string{`export_pivot_bottom_center truthy `, `export_texture_format not-equals "JPEG"`, `export_texture_size not-equals "4k"`}; !slices.Equal(advanced, wantAdvanced) {
			t.Errorf("%s: upgrade conditions = %q, want %q", tc.id, advanced, wantAdvanced)
		}
	}
}

func parameterOf(offering schema.Offering, name string) (schema.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return schema.Parameter{}, false
}

// The load-time checks of the Schema features the format sets and their price
// rest on.
func TestFormatSetDeclarationsAreValidatedAtLoad(t *testing.T) {
	const id = "tripo/tripo-v3.1-full/text-to-3d"
	load := func(mutate func(offering map[string]any)) error {
		var document map[string]any
		if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &document); err != nil {
			t.Fatal(err)
		}
		for _, raw := range document["offerings"].([]any) {
			if offering := raw.(map[string]any); offering["id"] == id {
				mutate(offering)
			}
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		_, err = catalog.Load(bytes.NewReader(encoded))
		return err
	}
	parameter := func(offering map[string]any, name string) map[string]any {
		for _, raw := range offering["parameters"].([]any) {
			if candidate := raw.(map[string]any); candidate["name"] == name {
				return candidate
			}
		}
		t.Fatalf("no parameter %s", name)
		return nil
	}
	output := func(offering map[string]any, variant string) map[string]any {
		for _, raw := range offering["outputs"].([]any) {
			if candidate := raw.(map[string]any); candidate["variant"] == variant {
				return candidate
			}
		}
		t.Fatalf("no output %s", variant)
		return nil
	}
	upgrade := func(offering map[string]any) map[string]any {
		return offering["native_pricing"].(map[string]any)["selection"].(map[string]any)["upgrade"].(map[string]any)
	}
	if err := load(func(map[string]any) {}); err != nil {
		t.Fatalf("the shipped declarations were refused: %v", err)
	}
	for name, test := range map[string]struct {
		mutate func(offering map[string]any)
		want   string
	}{
		"a required item that is not an option": {func(o map[string]any) {
			parameter(o, "output_formats")["required_items"] = []any{"BLEND"}
		}, "required_items"},
		"a repeated required item": {func(o map[string]any) {
			parameter(o, "output_formats")["required_items"] = []any{"GLTF", "GLTF"}
		}, "required_items"},
		"a default that lacks a required item": {func(o map[string]any) {
			p := parameter(o, "output_formats")
			p["required_items"] = []any{"GLTF", "FBX"}
			p["default"] = []any{"GLTF", "STL"}
		}, "required_items"},
		"required items on a text parameter": {func(o map[string]any) {
			parameter(o, "prompt")["required_items"] = []any{"x"}
		}, "required_items"},
		"contains on an undeclared format": {func(o map[string]any) {
			output(o, "converted.fbx")["required_when"] = map[string]any{"parameter": "output_formats", "operator": "contains", "value": "BLEND"}
		}, "required_when"},
		"contains on a parameter that is not a multi-select": {func(o map[string]any) {
			output(o, "converted.fbx")["required_when"] = map[string]any{"parameter": "texture_quality", "operator": "contains", "value": "standard"}
		}, "required_when"},
		"equals on a multi-select": {func(o map[string]any) {
			output(o, "converted.fbx")["required_when"] = map[string]any{"parameter": "output_formats", "operator": "equals", "value": "FBX"}
		}, "required_when"},
		"an upgrade that is not dearer": {func(o map[string]any) {
			upgrade(o)["amount"] = "5"
		}, "upgrade"},
		"an upgrade without conditions": {func(o map[string]any) {
			upgrade(o)["when_any"] = []any{}
		}, "upgrade"},
		"an upgrade with too many conditions": {func(o map[string]any) {
			condition := map[string]any{"parameter": "export_pivot_bottom_center", "operator": "truthy"}
			upgrade(o)["when_any"] = []any{condition, condition, condition, condition, condition}
		}, "upgrade"},
		"an upgrade condition with an unknown operator": {func(o map[string]any) {
			upgrade(o)["when_any"] = []any{map[string]any{"parameter": "export_pivot_bottom_center", "operator": "maybe"}}
		}, "upgrade"},
		"an upgrade condition on an unknown parameter": {func(o map[string]any) {
			upgrade(o)["when_any"] = []any{map[string]any{"parameter": "no_such_parameter", "operator": "truthy"}}
		}, "no_such_parameter"},
		"an upgrade condition on a value the parameter cannot take": {func(o map[string]any) {
			upgrade(o)["when_any"] = []any{map[string]any{"parameter": "export_texture_format", "operator": "not-equals", "value": "TIFF"}}
		}, "TIFF"},
	} {
		t.Run(name, func(t *testing.T) {
			err := load(test.mutate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want one naming %q", err, test.want)
			}
		})
	}
}
