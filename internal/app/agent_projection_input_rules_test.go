package app

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// list_offerings shows an Offering's input groups before a call: Meshy
// retexture takes a prompt or a style picture, at most one of them, so the
// Agent no longer learns it from a refusal. The rule's parameters are the
// names its inputs use, and the ids-only view stays without them.
func TestAgentOfferingProjectionCarriesTheInputRules(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	type rule struct {
		Parameters    []string `json:"parameters"`
		Minimum       int      `json:"minimum"`
		Maximum       int      `json:"maximum"`
		MaxTotalItems int      `json:"max_total_items"`
		Message       string   `json:"message"`
	}
	type projection struct {
		Capabilities []struct {
			Offerings []struct {
				OfferingID string `json:"offering_id"`
				Inputs     []struct {
					Name string `json:"name"`
				} `json:"inputs"`
				InputRules []rule `json:"input_rules"`
			} `json:"offerings"`
		} `json:"capabilities"`
	}
	var detail projection
	if err := json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil, "retexture", "", true), &detail); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, capability := range detail.Capabilities {
		for _, offering := range capability.Offerings {
			if offering.OfferingID != "meshy/processing/retexture" {
				continue
			}
			found = true
			var styleRule *rule
			for index := range offering.InputRules {
				if slices.Contains(offering.InputRules[index].Parameters, "style_asset") {
					styleRule = &offering.InputRules[index]
				}
			}
			if styleRule == nil || !slices.Equal(styleRule.Parameters, []string{"prompt", "style_asset"}) ||
				styleRule.Minimum != 1 || styleRule.Maximum != 1 || styleRule.Message == "" {
				t.Fatalf("style input rule = %+v", offering.InputRules)
			}
			for _, name := range styleRule.Parameters {
				if !slices.ContainsFunc(offering.Inputs, func(input struct {
					Name string `json:"name"`
				}) bool {
					return input.Name == name
				}) {
					t.Fatalf("rule parameter %s is not one of the projected inputs %+v", name, offering.Inputs)
				}
			}
		}
	}
	if !found {
		t.Fatal("Meshy retexture missing from list_offerings")
	}
	var ids projection
	if err := json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil, "retexture", "", false), &ids); err != nil {
		t.Fatal(err)
	}
	for _, capability := range ids.Capabilities {
		for _, offering := range capability.Offerings {
			if len(offering.InputRules) > 0 {
				t.Fatalf("ids-only view carries input rules: %+v", offering)
			}
		}
	}
}
