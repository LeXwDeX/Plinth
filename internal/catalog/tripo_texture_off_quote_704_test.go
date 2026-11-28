package catalog_test

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// The native quotes of the real run of 2026-11-28 (#704,
// internal/testutil/fakeupstream/tripo/v3/testdata/observation-704-texture-off.json):
// with texture off the H full text task froze texture=false and pbr=false and
// quoted no texture component, 10 + 50 = 60 Tripo credits; at the defaults it
// quoted both texture components, 10 + 80 = 90.
func TestTripoHTextQuoteWithTextureOffHasNoTextureComponent(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
	for name, tc := range map[string]struct {
		changes      map[string]any
		texture      bool
		extra, total string
		components   map[string]string
	}{
		"texture off": {map[string]any{"texture": false}, false, "50", "60",
			map[string]string{"detailed_geometry": "20", "format_conversions": "30"}},
		"defaults": {nil, true, "80", "90",
			map[string]string{"detailed_geometry": "20", "extreme_texture": "20", "format_conversions": "30", "standard_texture": "10"}},
	} {
		t.Run(name, func(t *testing.T) {
			input := map[string]any{"prompt": "a compass", "authorize_extra_credits": true}
			maps.Copy(input, tc.changes)
			encoded, _ := json.Marshal(input)
			o, frozen, err := c.Normalize(schema.TextTo3D, id, encoded)
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := json.Unmarshal(frozen, &values); err != nil {
				t.Fatal(err)
			}
			if values["texture"] != tc.texture || values["pbr"] != tc.texture || values["texture_quality"] != "extreme" {
				t.Fatalf("frozen texture=%v pbr=%v texture_quality=%v, want %v %v extreme",
					values["texture"], values["pbr"], values["texture_quality"], tc.texture, tc.texture)
			}
			quote, err := o.EvaluateNativeQuote(frozen)
			if err != nil || quote == nil {
				t.Fatalf("quote=%+v err=%v", quote, err)
			}
			components := map[string]string{}
			for _, component := range quote.Components {
				components[component.ID] = component.Amount
			}
			if quote.Unit != "tripo.credit" || quote.BaseAmount != "10" || quote.ExtraAmount != tc.extra ||
				quote.TotalAmount != tc.total || !maps.Equal(components, tc.components) {
				t.Fatalf("quote = %+v, want 10 + %s = %s with %v", quote, tc.extra, tc.total, tc.components)
			}
		})
	}
}
