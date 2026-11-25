package app

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #620: list_offerings marks the Offerings that can apply a style Skill (they
// declare the Schema's Skill feature), in the ids-only view the Agent reads
// first and in the detail view, so it does not learn it by trying models.
func TestAgentOfferingsProjectionMarksStyleSkillOfferings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	feature := product.Application().Chat.Skills.Feature
	for _, detail := range []bool{false, true} {
		raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil, "text-to-image", "", detail, feature)
		var projection struct {
			Capabilities []struct {
				Offerings []struct {
					OfferingID string `json:"offering_id"`
					StyleSkill bool   `json:"style_skill"`
				} `json:"offerings"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &projection); err != nil {
			t.Fatal(err)
		}
		var marked, unmarked []string
		for _, capability := range projection.Capabilities {
			for _, offering := range capability.Offerings {
				if offering.StyleSkill {
					marked = append(marked, offering.OfferingID)
				} else {
					unmarked = append(unmarked, offering.OfferingID)
				}
			}
		}
		if !slices.Contains(marked, "openai/gpt-image-2/text-to-image") || !slices.Contains(unmarked, "meshy/nano-banana-pro/text-to-image") {
			t.Fatalf("detail=%v marked=%v unmarked=%v", detail, marked, unmarked)
		}
	}
}
