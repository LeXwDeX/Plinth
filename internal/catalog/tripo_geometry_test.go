package catalog

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #185 enables the native geometry Offerings that passed their isolated public
// qualification (Decimate triangle, Segment, Complete, and Quick Cap after its
// 429 export was fixed). Decimate quad stays disabled: upstream returns FBX,
// not the declared GLB, and the official page does not state the quad output
// format. Every Offering stays non-default and still requires its exact
// native source and full consent.
func TestTripoGeometryEnabledSourcesAndNormalization(t *testing.T) {
	qualified := map[string]bool{"decimate-triangle": true, "segment": true, "complete": true, "quick-cap": true}
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for kind, total := range map[string]int{"decimate-triangle": 85, "decimate-quad": 90, "segment": 90, "complete": 100, "quick-cap": 80} {
		t.Run(kind, func(t *testing.T) {
			id := generation.OfferingID("tripo/geometry/" + kind)
			o, ok := c.Get(id)
			// 185.5 declares the native OBJ export as the ZIP it is; 185.6
			// re-enables Quick Cap.
			revision := "2026-10-22.185.5"
			if kind == "quick-cap" {
				revision = "2026-10-22.185.6"
			}
			if !ok || o.Enabled != qualified[kind] || o.Default || o.Revision != revision {
				t.Fatalf("geometry Offering enablement drifted from the #185 qualification: %+v", o)
			}
			input := json.RawMessage(`{"source_task":"source","authorize_extra_credits":true}`)
			if _, _, err := c.Normalize(o.Capability, id, input); (err == nil) != qualified[kind] {
				t.Fatalf("public execution of %s: enabled=%v err=%v", kind, qualified[kind], err)
			}
			if _, _, err := c.Normalize(o.Capability, id, json.RawMessage(`{"source_task":"source"}`)); err == nil {
				t.Fatal("Offering accepted an input without full export consent")
			}
			listed := false
			for _, visible := range c.List(o.Capability) {
				listed = listed || visible.ID == id
			}
			if listed != qualified[kind] {
				t.Fatalf("live catalog listing of %s = %v", kind, listed)
			}
			normalized, err := normalizeInput(o, input)
			if err != nil {
				t.Fatal(err)
			}
			if o.EstimateTotalCredits(normalized) != total {
				t.Fatal("six-format cost not frozen")
			}
			var values map[string]json.RawMessage
			json.Unmarshal(normalized, &values)
			if len(values["output_formats"]) == 0 || len(values["part_names"]) > 0 || len(values["face_limit"]) > 0 {
				t.Fatal("invalid defaults")
			}
			var source schema.Parameter
			for _, p := range o.Parameters {
				if p.Semantic == schema.SemanticSourceTask {
					source = p
				}
			}
			if !source.SameProvider || source.TaskSourceKind != schema.TaskSourceUpstreamJob || source.WireName != "input" {
				t.Fatal("unbound native source")
			}
			if kind == "complete" || kind == "quick-cap" {
				if len(source.TaskOfferings) != 1 || source.TaskOfferings[0] != "tripo/geometry/segment" || len(source.TaskCapabilities) != 1 || source.TaskCapabilities[0] != "mesh-segmentation" {
					t.Fatal("Complete did not require exact Segment")
				}
			} else if len(source.TaskOfferings) != 2 || source.TaskOfferings[0] != "tripo/tripo-v3.1-full/text-to-3d" || source.TaskOfferings[1] != "tripo/tripo-v3.1-full/image-to-3d" {
				t.Fatal("source allowlist expanded")
			}
			for _, extra := range []string{`"part_names":[]`, `"ref_image":"anything"`, `"unknown":true`} {
				bad := json.RawMessage(`{"source_task":"source","authorize_extra_credits":true,` + extra + `}`)
				if _, err := normalizeInput(o, bad); err == nil {
					t.Fatal("unsupported parameter silently dropped")
				}
			}
			for _, bad := range []string{`{"source_task":"source"}`, `{"source_task":"source","authorize_extra_credits":false}`} {
				if _, err := normalizeInput(o, json.RawMessage(bad)); err == nil {
					t.Fatal("missing full cost consent accepted")
				}
			}
			segment, _ := c.Get("tripo/geometry/segment")
			full, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
			if kind == "complete" || kind == "quick-cap" {
				if _, err := generation.BindProjectedModelSource(o, segment); err != nil {
					t.Fatal(err)
				}
				if _, err := generation.BindProjectedModelSource(o, full); err == nil {
					t.Fatal("generation mistaken for Segment")
				}
			} else {
				if _, err := generation.BindProjectedModelSource(o, full); err != nil {
					t.Fatal(err)
				}
				if _, err := generation.BindProjectedModelSource(o, segment); err == nil {
					t.Fatal("undeclared processing source accepted")
				}
			}
			full.ProviderID = "other"
			if _, err := generation.BindProjectedModelSource(o, full); err == nil {
				t.Fatal("cross-provider accepted")
			}
		})
	}
}

func TestTripoGeometryFaceLimitsAreSchemaOwned(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for kind, max := range map[string]int{"decimate-triangle": 20000, "decimate-quad": 10000} {
		id := generation.OfferingID("tripo/geometry/" + kind)
		o, _ := c.Get(id)
		for _, v := range []int{500, max} {
			raw, _ := json.Marshal(map[string]any{"source_task": "source", "authorize_extra_credits": true, "face_limit": v})
			if _, err := normalizeInput(o, raw); err != nil {
				t.Fatal(err)
			}
		}
		for _, v := range []any{499, max + 1, 500.5, "500", nil} {
			raw, _ := json.Marshal(map[string]any{"source_task": "source", "authorize_extra_credits": true, "face_limit": v})
			if _, err := normalizeInput(o, raw); err == nil {
				t.Fatalf("accepted %s face %v", kind, v)
			}
		}
	}
}
