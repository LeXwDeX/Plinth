package httpapi

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestCreativeLabImageConstraintSurvivesExtendedDiscovery(t *testing.T) {
	t.Parallel()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("meshy/creative-lab/keycap/prototype")
	api := c.Application().ProviderDiscovery.API
	inputs := projectInputParameterSchemas(o.Parameters)
	if len(inputs) != 1 {
		t.Fatalf("inputs=%d", len(inputs))
	}
	projected := projectInputSchemaWire(inputs[0], api)
	raw, _ := json.Marshal(projected[api.InputFields.ImageInput])
	var got schema.ImageInputConstraint
	if err := json.Unmarshal(raw, &got); err != nil || got.MinWidth != 32 || got.MinHeight != 32 || got.MaxPixels != 178956970 {
		t.Fatalf("image input lost: %s", raw)
	}
	inputs[0].ImageInput.MinWidth = 1
	if o.Parameters[0].ImageInput.MinWidth != 32 {
		t.Fatal("discovery mutation leaked to offering")
	}
	params := projectParameterSchemas(o.Parameters, nil)
	var found bool
	for _, p := range params {
		if p.Name == "source_image" {
			found = true
			extended := projectParameterSchemaWire(p, api, true)
			encoded, _ := json.Marshal(extended[api.ParameterFields.Validation])
			var v schema.Parameter
			if json.Unmarshal(encoded, &v) != nil || v.ImageInput == nil || v.ImageInput.MinWidth != 32 {
				t.Fatal("extended validation lost image input")
			}
			legacy := projectParameterSchemaWire(p, api, false)
			if _, ok := legacy[api.ParameterFields.Validation]; ok {
				t.Fatal("legacy validation wire changed")
			}
		}
	}
	if !found {
		t.Fatal("source input omitted")
	}
}

func TestCreativeSourceConstraintsArePublicButInstanceIdentityIsPrivate(t *testing.T) {
	t.Parallel()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	build, _ := c.Get("meshy/creative-lab/keycap/build")
	api := c.Application().ProviderDiscovery.API
	inputs := projectInputParameterSchemas(build.Parameters)
	if len(inputs) != 1 {
		t.Fatal("source input absent")
	}
	fields := projectInputSchemaWire(inputs[0], api)
	if fields[api.InputFields.SameSession] != true || fields[api.InputFields.TaskSourceBinding] != "provider" {
		t.Fatal("source constraints lost in discovery")
	}
	source, _ := c.Get("meshy/creative-lab/keycap/prototype")
	source.ProviderOptions = json.RawMessage(`{"candidate":"private-candidate","remote":"private-remote","url":"https://signed.invalid/private"}`)
	task := generation.Task{ID: "public", State: generation.TaskSucceeded, Offering: source, Input: json.RawMessage(`{"prompt":"public"}`), ProviderData: json.RawMessage(`{"candidate":"private-candidate"}`), Assets: []generation.Asset{{ID: "preview", Role: "image", Variant: "1", MediaType: "image/png", SHA256: strings.Repeat("a", 64), Size: 100}}}
	s := &Server{generation: &generation.Module{}, application: c.Application()}
	native := s.newTaskResponse("", task)
	stream, err := s.projectTaskStream("", task, catalog.CapabilityPresentation{LegacyAPI: &catalog.LegacyTaskAPISchema{}}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{native, stream} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-candidate", "private-remote", "signed.invalid", "provider_binding", "metadata_json", "stored_asset_json"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("private identity leaked via public projection: %s", secret)
			}
		}
	}
}
