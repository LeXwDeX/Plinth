package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAgentOfferingsProjectionCarriesOfferingKnowledge(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil)
	var projection struct {
		Capabilities []struct {
			Offerings []struct {
				OfferingID string                      `json:"offering_id"`
				Knowledge  *agentOfferingKnowledgeView `json:"knowledge"`
			} `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, capability := range projection.Capabilities {
		for _, offering := range capability.Offerings {
			if offering.OfferingID != "meshy/nano-banana-pro/text-to-image" {
				continue
			}
			found = true
			declared, ok := product.Get(generation.OfferingID("meshy/nano-banana-pro/text-to-image"))
			if !ok || declared.Knowledge == nil {
				t.Fatal("catalog knowledge missing")
			}
			if offering.Knowledge == nil {
				t.Fatal("projection omitted knowledge")
			}
			if !slices.Equal(offering.Knowledge.Strengths, declared.Knowledge.Strengths) ||
				!slices.Equal(offering.Knowledge.Limitations, declared.Knowledge.Limitations) {
				t.Fatalf("projection knowledge differs from the Catalog: %#v", offering.Knowledge)
			}
			if offering.Knowledge.Prompt == nil || offering.Knowledge.Prompt.ReadsText == nil ||
				!*offering.Knowledge.Prompt.ReadsText {
				t.Fatalf("prompt guidance = %#v", offering.Knowledge.Prompt)
			}
			if !slices.Equal(offering.Knowledge.ReferenceUse, []string{"none"}) {
				t.Fatalf("a text-to-image offering declared reference use: %v", offering.Knowledge.ReferenceUse)
			}
		}
	}
	if !found {
		t.Fatal("offering missing from the projection")
	}

	// An Offering without knowledge omits the field rather than sending an
	// empty object.
	var generic struct {
		Capabilities []struct {
			Offerings []map[string]any `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, capability := range generic.Capabilities {
		for _, offering := range capability.Offerings {
			if offering["offering_id"] == "meshy/processing/remesh" {
				if _, present := offering["knowledge"]; present {
					t.Fatalf("undeclared knowledge = %#v", offering["knowledge"])
				}
			}
			// Sources stay maintainer-facing and never reach the Agent
			// projection. (Processing offerings have their own top-level
			// "sources" input list, so inspect only the knowledge object.)
			if knowledge, ok := offering["knowledge"].(map[string]any); ok {
				for _, forbidden := range []string{"sources", "sources_note"} {
					if _, present := knowledge[forbidden]; present {
						t.Fatalf("Offering knowledge %q leaked into list_offerings", forbidden)
					}
				}
			}
		}
	}
}

func TestListOfferingsDescriptionNamesKnowledgeAndPromptGuidance(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range product.Application().Chat.AgentTools {
		if tool.Name != "list_offerings" {
			continue
		}
		for _, needle := range []string{"knowledge", "prompt guidance", "reference_use"} {
			if !strings.Contains(tool.Description, needle) {
				t.Fatalf("list_offerings description omits %q: %s", needle, tool.Description)
			}
		}
		return
	}
	t.Fatal("list_offerings is not declared")
}
