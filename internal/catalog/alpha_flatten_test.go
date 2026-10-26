package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #271: the board uploads PNG with alpha. Offerings whose upstream is not shown to take transparent
// pixels declare alpha_flatten; the others receive the stored bytes, alpha included. GPT Image draws
// transparent input as black unless background=transparent is requested, so it flattens except then.
func TestAlphaFlattenDeclaredOnlyWhereTransparencyIsUnverified(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	white := schema.AlphaFlatten{BackgroundColor: "#ffffff", MaxPixels: 16777216}
	keepTransparent := white
	keepTransparent.KeepWhen = []schema.ParameterCondition{{Parameter: "background", Operator: "equals", Value: json.RawMessage(`"transparent"`)}}
	flattened := map[string]schema.AlphaFlatten{
		"meshy/nano-banana-pro/image-to-image":     white,
		"meshy/nano-banana-2/image-to-image":       white,
		"gemini/gemini-3-pro-image/image-to-image": white,
		"jimeng/seedream-4-5/image-to-image":       white,
		"openai/gpt-image-2/image-to-image":        keepTransparent,
	}
	for _, offering := range append(c.List(schema.ImageToImage), c.List(schema.ImageTo3D)...) {
		for _, p := range offering.Parameters {
			if p.AlphaFlatten == nil {
				continue
			}
			want, declared := flattened[string(offering.ID)]
			if !declared || p.Name != "reference_assets" || !sameAlphaFlatten(*p.AlphaFlatten, want) {
				t.Fatalf("unexpected alpha_flatten on %s.%s: %+v", offering.ID, p.Name, *p.AlphaFlatten)
			}
			delete(flattened, string(offering.ID))
		}
	}
	if len(flattened) != 0 {
		t.Fatalf("alpha_flatten missing on %v", flattened)
	}
	o, _ := c.Get("openai/gpt-image-2/image-to-image")
	policy := specialParameter(t, o, "reference_assets").AlphaFlatten
	policy.BackgroundColor = "#000000"
	policy.KeepWhen[0].Operator = "not-equals"
	o, _ = c.Get(o.ID)
	if !sameAlphaFlatten(*specialParameter(t, o, "reference_assets").AlphaFlatten, keepTransparent) {
		t.Fatal("caller mutated stored alpha_flatten")
	}
	if specialParameter(t, o, "mask").AlphaFlatten != nil {
		t.Fatal("the inpaint mask keeps its alpha")
	}
}

func sameAlphaFlatten(got, want schema.AlphaFlatten) bool {
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	return bytes.Equal(a, b)
}

func TestAlphaFlattenRejectsInvalidDeclarations(t *testing.T) {
	declaration := func() map[string]any {
		return map[string]any{"background_color": "#ffffff", "max_pixels": 16777216}
	}
	for index, mutation := range []struct {
		offering  string
		parameter string
		mutate    func(map[string]any)
	}{
		{"gemini/gemini-3-pro-image/image-to-image", "reference_assets", func(p map[string]any) { p["alpha_flatten"].(map[string]any)["background_color"] = "white" }},
		{"gemini/gemini-3-pro-image/image-to-image", "reference_assets", func(p map[string]any) { p["alpha_flatten"].(map[string]any)["max_pixels"] = 0 }},
		{"gemini/gemini-3-pro-image/image-to-image", "reference_assets", func(p map[string]any) { p["media_types"] = []string{"application/octet-stream"} }},
		// Image-to-3D sources are not projected through imagewire, so a declaration there would be ignored.
		{"tripo/tripo-v3.1/image-to-3d", "reference_assets", func(p map[string]any) { p["alpha_flatten"] = declaration() }},
		// A mask's transparent area is the edit area: flattening would erase it.
		{"openai/gpt-image-2/image-to-image", "mask", func(p map[string]any) { p["alpha_flatten"] = declaration() }},
		{"openai/gpt-image-2/image-to-image", "reference_assets", func(p map[string]any) {
			p["alpha_flatten"].(map[string]any)["keep_when"] = []any{map[string]any{"parameter": "no_such_parameter", "operator": "equals", "value": "transparent"}}
		}},
		{"openai/gpt-image-2/image-to-image", "reference_assets", func(p map[string]any) {
			p["alpha_flatten"].(map[string]any)["keep_when"] = []any{map[string]any{"parameter": "background", "operator": "equals", "value": "see-through"}}
		}},
	} {
		var doc map[string]any
		if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
			t.Fatal(err)
		}
		for _, raw := range doc["offerings"].([]any) {
			o := raw.(map[string]any)
			if o["id"] != mutation.offering {
				continue
			}
			for _, rawParameter := range o["parameters"].([]any) {
				if p := rawParameter.(map[string]any); p["name"] == mutation.parameter {
					mutation.mutate(p)
				}
			}
		}
		raw, _ := json.Marshal(doc)
		if _, err := Load(bytes.NewReader(raw)); err == nil {
			t.Fatalf("invalid alpha_flatten accepted: mutation %d", index)
		}
	}
}

func TestCanvasArtboardBackgroundIsTransparentAndValidated(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	export := taskCatalog.Application().Canvas.Frontend.Export
	if export.ArtboardBackgroundColor != export.TransparentBackgroundValue {
		t.Fatalf("artboard background = %q, want transparent (#271)", export.ArtboardBackgroundColor)
	}
	for _, mutate := range []func(*CanvasExportSchema){
		func(e *CanvasExportSchema) { e.ArtboardBackgroundColor = "" },
		func(e *CanvasExportSchema) { e.ArtboardBackgroundColor = "white" },
		func(e *CanvasExportSchema) { e.OutputMediaType = "image/jpeg" },
	} {
		application := taskCatalog.Application()
		mutate(&application.Canvas.Frontend.Export)
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("accepted invalid artboard background: %+v", application.Canvas.Frontend.Export)
		}
	}
	application := taskCatalog.Application()
	application.Canvas.Frontend.Export.ArtboardBackgroundColor = "#ffffff"
	application.Canvas.Frontend.Export.OutputMediaType = "image/jpeg"
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("an opaque artboard background may use any media type: %v", err)
	}
}
