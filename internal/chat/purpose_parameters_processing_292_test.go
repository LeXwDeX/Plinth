package chat_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #292: the files a Tripo processing step exports (the FBX of a rigging, the five
// formats of a decimation) are written at the texture size the session purpose
// allows. The size is a public parameter with the texture-size semantic, so the
// resolver the Agent's tools and the direct task API share (#295) gives it the
// purpose's budget (#129), the target platform's variant of it included, with
// the shipped Product Schema. The step needs no override of its own and no
// reason: it is not an advised parameter.

// tripoExportOfferings are the shipped Offerings whose exports carry the size.
var tripoExportOfferings = []generation.OfferingID{
	"tripo/processing/texture", "tripo/processing/rigging", "tripo/processing/rigging-v3",
	"tripo/geometry/decimate-triangle", "tripo/geometry/decimate-quad", "tripo/geometry/segment",
	"tripo/geometry/complete", "tripo/geometry/quick-cap",
	"tripo/geometry/smartsegment-image", "tripo/geometry/smartsegment-model",
}

func shippedPurposeResolver(t *testing.T) (chat.PurposeParameters, *catalog.Catalog) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	return chat.PurposeParameters{Model3D: chatSchema.Model3DUsage, Image2D: chatSchema.Image2DUsage}, product
}

func TestDirectProcessingExportsTakeThePurposeTextureBudget(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	for name, test := range map[string]struct {
		brief map[string]any
		want  string // the size the purpose gives; "" leaves the Offering's default of 8K
	}{
		"game character":                  {map[string]any{"purpose": "game_character"}, "2k"},
		"game character on a phone":       {map[string]any{"purpose": "game_character", "target_platform": "mobile"}, "2k"},
		"game character on the web":       {map[string]any{"purpose": "game_character", "target_platform": "web"}, "2k"},
		"game character on a PC":          {map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, "4k"},
		"game prop":                       {map[string]any{"purpose": "game_prop"}, "2k"},
		"game prop on a PC":               {map[string]any{"purpose": "game_prop", "target_platform": "pc_console"}, "4k"},
		"web and AR":                      {map[string]any{"purpose": "web_ar"}, "2k"},
		"print":                           {map[string]any{"purpose": "print"}, "4k"},
		"display bounds nothing below 8K": {map[string]any{"purpose": "render"}, ""},
		"a brief without a purpose":       {map[string]any{"style": "写实"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			brief := purposeParamsBrief(t, test.brief)
			for _, id := range tripoExportOfferings {
				offering, ok := product.Get(id)
				if !ok {
					t.Fatalf("missing %s", id)
				}
				request := `{"source_task":"source"}`
				applied, err := resolver.ApplyToDirectInput(brief, offering, json.RawMessage(request), nil)
				if err != nil {
					t.Fatal(err)
				}
				if test.want == "" {
					// Nothing to give: the request is exactly what the caller sent.
					if !bytes.Equal(applied.Input, []byte(request)) {
						t.Fatalf("%s: input = %s, want the request's own bytes", id, applied.Input)
					}
					continue
				}
				values := purposeParamsDecode(t, applied.Input)
				if values["export_texture_size"] != test.want {
					t.Fatalf("%s: export_texture_size = %v, want %s (input %s)", id, values["export_texture_size"], test.want, applied.Input)
				}
				// A step that cannot carry a reason needs none: the size is no advised change.
				if len(applied.Reasons) != 0 || len(applied.Sources) != 0 {
					t.Fatalf("%s: reasons = %v sources = %v, want none", id, applied.Reasons, applied.Sources)
				}
			}
		})
	}
}

// Without a session purpose nothing bounds the export: the default frozen with
// the step is the 8K every export has always had. A size the caller sets is the
// caller's even when it is over the purpose's budget.
func TestDirectProcessingExportKeepsTheCallersSize(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, ok := product.Get("tripo/processing/rigging-v3")
	if !ok {
		t.Fatal("missing the shipped rigging Offering")
	}
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character", "target_platform": "mobile"})
	request := `{"source_task":"source","export_texture_size":"8k"}`
	applied, err := resolver.ApplyToDirectInput(brief, offering, json.RawMessage(request), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(applied.Input, []byte(request)) {
		t.Fatalf("input = %s, want the caller's 8k kept", applied.Input)
	}
	var defaulted json.RawMessage
	for _, parameter := range offering.Parameters {
		if parameter.Name == "export_texture_size" {
			defaulted = parameter.Default
		}
	}
	if string(defaulted) != `"8k"` {
		t.Fatalf("the Offering's default size = %s, want 8k", defaulted)
	}
}
