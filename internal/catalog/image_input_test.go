package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestImageInputPublicContractAndLookupCopies(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("meshy/creative-lab/keycap/prototype")
	p := specialParameter(t, o, "source_image")
	want := schema.ImageInputConstraint{MinWidth: 32, MinHeight: 32, MaxPixels: 178956970}
	if p.ImageInput == nil || *p.ImageInput != want {
		t.Fatalf("missing input dimension constraint: %+v", p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	var got schema.ImageInputConstraint
	if err := json.Unmarshal(public["image_input"], &got); err != nil || got != want {
		t.Fatal("constraint lost in public DTO")
	}
	p.ImageInput.MinWidth = 1
	o, _ = c.Get(o.ID)
	if *specialParameter(t, o, "source_image").ImageInput != want {
		t.Fatal("caller mutated stored image constraint")
	}
	old, _ := c.Get("meshy/nano-banana-pro/image-to-image")
	for _, p := range old.Parameters {
		if p.ImageInput != nil {
			t.Fatal("historical image contract was rewritten")
		}
	}
}

func TestImageInputRejectsInvalidAndUnpublishedConstraints(t *testing.T) {
	for index, mutate := range []func(map[string]any, map[string]any){
		func(p, app map[string]any) { p["type"] = "text" },
		func(p, app map[string]any) { p["max_bytes"] = 0 },
		func(p, app map[string]any) { p["media_types"] = []string{"application/octet-stream"} },
		func(p, app map[string]any) { p["image_input"].(map[string]any)["min_width"] = 0 },
		func(p, app map[string]any) { p["image_input"].(map[string]any)["max_pixels"] = 1023 },
		func(p, app map[string]any) {
			delete(app["provider_discovery"].(map[string]any)["api"].(map[string]any)["input_fields"].(map[string]any), "image_input")
		},
		func(p, app map[string]any) {
			fields := app["provider_discovery"].(map[string]any)["api"].(map[string]any)["input_fields"].(map[string]any)
			fields["image_input"] = fields["max_bytes"]
		},
	} {
		var doc map[string]any
		if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
			t.Fatal(err)
		}
		for _, raw := range doc["offerings"].([]any) {
			o := raw.(map[string]any)
			if o["id"] == "meshy/creative-lab/keycap/prototype" {
				mutate(o["parameters"].([]any)[0].(map[string]any), doc["application"].(map[string]any))
			}
		}
		raw, _ := json.Marshal(doc)
		if _, err := Load(bytes.NewReader(raw)); err == nil {
			t.Fatalf("invalid image contract accepted: mutation %d", index)
		}
	}
}
