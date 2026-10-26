package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestPersonalSkillCreationContractIsPublicAndValidated(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.Chat.Skills.Creation.Route == "" || application.Chat.Skills.Creation.MaxNameCharacters <= 0 {
		t.Fatalf("missing creation contract: %#v", application.Chat.Skills.Creation)
	}
	public := application.Public()
	if public.Chat.Skills.Creation != application.Chat.Skills.Creation {
		t.Fatalf("public creation contract = %#v", public.Chat.Skills.Creation)
	}
	if len(public.Chat.Skills.Categories) != len(application.Chat.Skills.Categories) ||
		len(public.Chat.Skills.Categories) == 0 ||
		public.Chat.Skills.Categories[0].Label != application.Chat.Skills.Categories[0].Label {
		t.Fatalf("public Skill categories = %#v", public.Chat.Skills.Categories)
	}
	encoded, err := json.Marshal(public.Chat.Skills)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, leaked := fields["execution"]; leaked {
		t.Fatal("public Skill schema contains private execution declaration")
	}
	application.Chat.Skills.Categories[0].Label = "modified"
	if fresh := product.Application().Chat.Skills.Categories[0].Label; fresh == "modified" {
		t.Fatal("Skill categories were not defensively cloned")
	}
	invalid := application
	invalid.Chat.Skills.Creation.Route = "https://external.test/personal"
	if err := catalog.ValidateApplicationSchema(invalid); err == nil {
		t.Fatal("external personal Skill route accepted")
	}
	invalid = application
	invalid.Chat.Skills.Creation.MaxNameCharacters = 0
	if err := catalog.ValidateApplicationSchema(invalid); err == nil {
		t.Fatal("zero maximum name length accepted")
	}
	if application.Chat.Skills.Creation.ItemRoute != application.Chat.Skills.Creation.Route+"/{skillID}" {
		t.Fatalf("personal Skill item route = %q", application.Chat.Skills.Creation.ItemRoute)
	}
	for _, route := range []string{"/api/v1/other/{skillID}", application.Chat.Skills.Creation.Route + "/{id}", application.Chat.Skills.Creation.Route + "/{skillID}/x"} {
		invalid = application
		invalid.Chat.Skills.Creation.ItemRoute = route
		if err := catalog.ValidateApplicationSchema(invalid); err == nil {
			t.Fatalf("invalid personal Skill item route accepted: %q", route)
		}
	}
	invalid = application
	invalid.Chat.Skills.Creation.RecommendedSize = ""
	if err := catalog.ValidateApplicationSchema(invalid); err == nil {
		t.Fatal("empty creation default size accepted")
	}
}

func TestPersonalSkillCreationDefaultMustMatchSkillOfferingOptions(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	application := doc["application"].(map[string]any)
	chat := application["chat"].(map[string]any)
	skills := chat["skills"].(map[string]any)
	creation := skills["creation"].(map[string]any)
	creation["recommended_size"] = "not-an-offering-size"
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(encoded)); err == nil {
		t.Fatal("creation default outside Skill Offering output-size accepted")
	}
}
