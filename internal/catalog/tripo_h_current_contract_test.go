package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Independent contract fixture, retrieved 2026-10-17:
// https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard
// https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard
// https://developers.tripo3d.ai/en/pricing (H Series: standard texture 20/30;
// HD texture +10, 8K texture +20, HD geometry +20, basic conversion +5).
func TestTripoHCurrentGenerationDefaultsAndQuotes(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id          string
		base, total string
		full        bool
	}{
		{"tripo/tripo-v3.1-full/text-to-3d", "10", "90", true},
		{"tripo/tripo-v3.1/text-to-3d", "10", "90", false},
		{"tripo/tripo-v3.1-full/image-to-3d", "20", "100", true},
		{"tripo/tripo-v3.1/image-to-3d", "20", "100", false},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			o, ok := c.Get(schema.OfferingID(tc.id))
			if !ok {
				t.Fatal("missing Offering")
			}
			if o.NativePricing == nil || o.NativePricing.Unit != "tripo.credit" || o.Cost != nil || o.EstimatedCredits != 0 {
				t.Fatalf("expected one native Tripo quote source: native=%v legacy_cost=%v estimated=%d", o.NativePricing != nil, o.Cost != nil, o.EstimatedCredits)
			}
			defaults := map[string]any{}
			for _, p := range o.Parameters {
				if len(p.Default) != 0 {
					var value any
					if err := json.Unmarshal(p.Default, &value); err != nil {
						t.Fatal(err)
					}
					defaults[p.Name] = value
				}
				if p.Name == "geometry_quality" {
					if len(p.Options) != 2 || p.Options[0].Value != "standard" || p.Options[1].Value != "detailed" {
						t.Fatalf("unsupported geometry enum: %+v", p.Options)
					}
				}
			}
			if defaults["geometry_quality"] != "detailed" || defaults["texture_quality"] != "extreme" || defaults["texture_version"] != "v3.5-20260815" {
				t.Fatalf("highest current defaults absent: %+v", defaults)
			}
			quote, err := o.DefaultNativeQuote(nil)
			if err != nil || quote == nil || quote.BaseAmount != tc.base || quote.TotalAmount != tc.total {
				t.Fatalf("default quote=%+v err=%v, expected base %s total %s", quote, err, tc.base, tc.total)
			}
			if tc.full && (!o.Enabled || len(o.Outputs) < 7) {
				t.Fatalf("full Offering lost enabled six-format delivery: %+v", o)
			}
		})
	}
}

func TestTripoHCurrentPriceBranchesAndConsent(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
	base := map[string]any{"prompt": "a compass", "authorize_extra_credits": true}
	cases := []struct {
		name                 string
		changes              map[string]any
		total                string
		wantTexture, wantPBR bool
	}{
		{"default 8K detailed geometry six conversions", nil, "90", true, true},
		{"standard texture and geometry", map[string]any{"texture_quality": "standard", "geometry_quality": "standard"}, "50", true, true},
		{"HD texture and standard geometry", map[string]any{"texture_quality": "detailed", "geometry_quality": "standard"}, "60", true, true},
		{"bare detailed geometry", map[string]any{"texture": false}, "60", false, false},
		{"segmented bare model", map[string]any{"generate_parts": true}, "80", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{}
			for key, value := range base {
				input[key] = value
			}
			for key, value := range tc.changes {
				input[key] = value
			}
			encoded, _ := json.Marshal(input)
			o, frozen, err := c.Normalize(schema.TextTo3D, id, encoded)
			if err != nil {
				t.Fatal(err)
			}
			quote, err := o.EvaluateNativeQuote(frozen)
			if err != nil || quote == nil || quote.TotalAmount != tc.total {
				t.Fatalf("quote=%+v err=%v; want %s", quote, err, tc.total)
			}
			var values map[string]any
			if err := json.Unmarshal(frozen, &values); err != nil {
				t.Fatal(err)
			}
			if values["texture"] != tc.wantTexture || values["pbr"] != tc.wantPBR {
				t.Fatalf("forced texture/PBR mismatch: %+v", values)
			}
		})
	}
	for _, input := range []string{
		`{"prompt":"compass"}`,
		`{"prompt":"compass","authorize_extra_credits":false}`,
		`{"prompt":"compass","authorize_extra_credits":true,"geometry_quality":"extreme"}`,
		`{"prompt":"compass","authorize_extra_credits":true,"output_formats":["GLTF"]}`,
		`{"prompt":"compass","authorize_extra_credits":true,"output_formats":["GLTF","GLTF","USDZ","FBX","OBJ","STL"]}`,
		`{"prompt":"compass","authorize_extra_credits":true,"output_formats":["GLTF","USDZ","FBX","OBJ","STL","XYZ"]}`,
	} {
		if _, _, err := c.Normalize(schema.TextTo3D, id, json.RawMessage(input)); err == nil {
			t.Fatalf("accepted unsafe H input: %s", input)
		}
	}
	o, _ := c.Get(id)
	if _, err := o.EvaluateNativeQuote(json.RawMessage(`{"output_formats":[]}`)); err == nil {
		t.Fatal("direct quote accepted an empty required format selection")
	}
	if o.NativePricing == nil || o.NativePricing.MaxAuthorizedExtraAmount != "125" {
		t.Fatalf("conservative frozen extra bound missing: %+v", o.NativePricing)
	}
	if q, err := o.UpperNativeQuote(); err != nil || q == nil || q.TotalAmount != "135" {
		t.Fatalf("upper quote=%+v err=%v", q, err)
	}
}
