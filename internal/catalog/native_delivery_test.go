package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeDeliveryFreezesAllFormatsAndCostsBeforeConsent(t *testing.T) {
	c := enabledNativeTripoCatalog(t)
	for _, tc := range []struct {
		name         string
		formats      []string
		extra, total int
	}{
		{"rigging", []string{"FBX"}, 10, 35},
		{"texture", []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}, 50, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := schema.OfferingID("tripo/processing/" + tc.name)
			o, _ := c.Get(id)
			input := map[string]any{"source_task": "source", "prompt": "silver"}
			if tc.name == "rigging" {
				delete(input, "prompt")
			}
			for _, consent := range []any{nil, false} {
				if consent == nil {
					delete(input, "authorize_extra_credits")
				} else {
					input["authorize_extra_credits"] = consent
				}
				encoded, _ := json.Marshal(input)
				if _, _, err := c.Normalize(o.Capability, id, encoded); err == nil {
					t.Fatal("accepted complete paid delivery without consent")
				}
			}
			input["authorize_extra_credits"] = true
			for _, formats := range [][]string{nil, {}, {"FBX"}, {"FBX", "FBX"}} {
				input["output_formats"] = formats
				encoded, _ := json.Marshal(input)
				frozen, normalized, err := c.Normalize(o.Capability, id, encoded)
				if err != nil {
					t.Fatal(err)
				}
				var values struct {
					Formats []string `json:"output_formats"`
				}
				if err := json.Unmarshal(normalized, &values); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(values.Formats, tc.formats) {
					t.Fatalf("formats=%v", values.Formats)
				}
				extra, err := EvaluateExtraCredits(*frozen.Cost, normalized)
				if err != nil || extra != tc.extra || frozen.EstimateTotalCredits(normalized) != tc.total || frozen.MaximumEstimatedCredits() != tc.total {
					t.Fatalf("extra=%d total=%d err=%v", extra, frozen.EstimateTotalCredits(normalized), err)
				}
				if _, err := AuthorizeExtraCredits(frozen, normalized); err != nil {
					t.Fatal(err)
				}
				policy := approvalpolicy.New("manual", []string{"manual", "auto"}, nil, nil)
				if !approvalpolicy.RequiresHumanForExtraCredits(policy, "manual", extra) || approvalpolicy.RequiresHumanForExtraCredits(policy, "auto", extra) {
					t.Fatal("extra-cost approval boundary changed")
				}
			}
			var wire struct {
				Native struct {
					Source     string         `json:"source_alias"`
					Max        int64          `json:"max_source_bytes"`
					Parameters map[string]any `json:"parameters"`
					Formats    []struct {
						Format, Role, Variant string
						Media                 string `json:"media_type"`
					} `json:"formats"`
				} `json:"native_conversion"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &wire); err != nil {
				t.Fatal(err)
			}
			native := wire.Native
			if native.Source != "model" || native.Max != 150000000 || len(native.Formats) != len(tc.formats) || !reflect.DeepEqual(native.Parameters, map[string]any{"texture_size": float64(8192), "texture_format": "PNG", "with_animation": tc.name == "rigging", "bake": true, "fbx_preset": "blender"}) {
				t.Fatalf("conversion contract=%+v", native)
			}
			for i, format := range native.Formats {
				output := o.Outputs[i+1]
				if format.Format != tc.formats[i] || format.Role != output.Role || format.Variant != output.Variant || len(output.MediaTypes) != 1 || format.Media != output.MediaTypes[0] || output.Minimum == nil || *output.Minimum != 1 || output.Delivery != schema.AssetDeliveryRequired || output.MaxBytes != 1<<30 {
					t.Fatalf("format/output mismatch: %+v %+v", format, output)
				}
				if format.Format == "FBX" && (output.DeliveryProfile != "binary-fbx-v1" || format.Media != "model/fbx" || output.Variant != "fbx") {
					t.Fatal("FBX contract widened")
				}
				if tc.name == "rigging" && output.Fidelity != "skeleton" {
					t.Fatal("FBX must prove skeleton")
				}
			}
		})
	}
}

func TestLegacyOfferingScopesAndAliasesValidateAndClone(t *testing.T) {
	c := enabledNativeTripoCatalog(t)
	for _, tc := range []struct {
		name   string
		mutate func(*ApplicationSchema)
	}{
		{"unknown scope", func(a *ApplicationSchema) {
			for i := range a.Capabilities {
				if a.Capabilities[i].ID == schema.Rigging {
					a.Capabilities[i].LegacyAPI.Fields[0].OfferingIDs = []schema.OfferingID{"unknown"}
				}
			}
		}},
		{"duplicate scope", func(a *ApplicationSchema) {
			for i := range a.Capabilities {
				if a.Capabilities[i].ID == schema.Rigging {
					a.Capabilities[i].LegacyAPI.Fields[0].OfferingIDs = []schema.OfferingID{"meshy/processing/rigging", "meshy/processing/rigging"}
				}
			}
		}},
		{"wrong capability", func(a *ApplicationSchema) {
			for i := range a.Capabilities {
				if a.Capabilities[i].ID == schema.Rigging {
					a.Capabilities[i].LegacyAPI.Fields[0].OfferingIDs = []schema.OfferingID{"tripo/processing/texture"}
				}
			}
		}},
		{"wrong variant", func(a *ApplicationSchema) {
			for i := range a.Capabilities {
				if a.Capabilities[i].ID == schema.Rigging {
					a.Capabilities[i].LegacyAPI.ResultAssets[0].OfferingVariants["tripo/processing/rigging"] = "not-glb"
				}
			}
		}},
		{"alias outside scope", func(a *ApplicationSchema) {
			for i := range a.Capabilities {
				if a.Capabilities[i].ID == schema.Rigging {
					a.Capabilities[i].LegacyAPI.ResultAssets[0].OfferingIDs = []schema.OfferingID{"meshy/processing/rigging"}
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := c.Application()
			tc.mutate(&a)
			if err := validateLegacyOfferingScopes(a, c.byID); err == nil {
				t.Fatal("accepted invalid scope")
			}
		})
	}
	a := c.Application()
	for i := range a.Capabilities {
		if a.Capabilities[i].ID == schema.Rigging {
			legacy := a.Capabilities[i].LegacyAPI
			legacy.Fields[0].OfferingIDs[0] = "unknown"
			legacy.ResultAssets[0].OfferingVariants["tripo/processing/rigging"] = "unknown"
		}
	}
	if err := validateLegacyOfferingScopes(c.Application(), c.byID); err != nil {
		t.Fatalf("scope clone leaked mutation: %v", err)
	}
}

func TestNativeSourceBindingUsesExactProviderOfferingAndCapability(t *testing.T) {
	c := enabledNativeTripoCatalog(t)
	for _, candidate := range nativeTripoCandidates {
		o, _ := c.Get(schema.OfferingID("tripo/processing/" + candidate.name))
		sourceID := schema.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
		capability := schema.TextTo3D
		if candidate.name == "animation" {
			sourceID = "tripo/processing/rigging-v3"
			capability = schema.Rigging
		}
		valid := generation.Task{ID: "source", State: generation.TaskSucceeded, Capability: capability, Offering: generation.Offering{ID: sourceID, ProviderID: o.ProviderID}, Assets: []generation.Asset{{ID: "model", Role: "model", Variant: "glb", StorageKey: "saved"}}}
		binding, err := generation.BindModelSource(o, valid, "", nil)
		if err != nil || binding.Parameter != "source_task" || binding.Value != "source" {
			t.Fatalf("%s source binding: %+v %v", candidate.name, binding, err)
		}
		for _, change := range []string{"provider", "offering", "capability", "state"} {
			source := valid
			switch change {
			case "provider":
				source.Offering.ProviderID = "meshy"
			case "offering":
				source.Offering.ID = "tripo/unknown"
			case "capability":
				source.Capability = schema.FormatConversion
			case "state":
				source.State = generation.TaskFailed
			}
			if _, err := generation.BindModelSource(o, source, "", nil); !generation.IsCode(err, generation.ErrInvalidInput) {
				t.Fatalf("%s %s accepted incompatible source: %v", candidate.name, change, err)
			}
		}
	}
}
