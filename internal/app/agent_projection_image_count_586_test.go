package app

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #586: list_offerings shows the Agent each 2D count parameter as an advised
// input it may set, with the range the Offering declares, now that no panel
// count stands in for it.
func TestAgentOfferingProjectionListsTheImageCountForTheAgent(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		offering, parameter string
		maximum             float64
	}{
		{"openai/gpt-image-2/text-to-image", "n", 10},
		{"openai/gpt-image-2/image-to-image", "n", 10},
		{"jimeng/seedream-4-5/text-to-image", "max_images", 15},
		{"jimeng/seedream-4-5/image-to-image", "sequential_image_generation", 0},
	} {
		raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil, "", "", true, "")
		var projection struct {
			Capabilities []struct {
				Offerings []struct {
					OfferingID string `json:"offering_id"`
					Inputs     []struct {
						Name             string   `json:"name"`
						Minimum          *float64 `json:"minimum"`
						Maximum          *float64 `json:"maximum"`
						AdjustmentPolicy string   `json:"adjustment_policy"`
						AdjustmentReason string   `json:"adjustment_reason"`
					} `json:"inputs"`
				} `json:"offerings"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &projection); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, capability := range projection.Capabilities {
			for _, offering := range capability.Offerings {
				if offering.OfferingID != want.offering {
					continue
				}
				for _, input := range offering.Inputs {
					if input.Name != want.parameter {
						continue
					}
					found = true
					if input.AdjustmentPolicy != "advised" || input.AdjustmentReason == "" {
						t.Errorf("%s.%s policy = %q, reason = %q", want.offering, want.parameter, input.AdjustmentPolicy, input.AdjustmentReason)
					}
					if want.maximum > 0 && (input.Minimum == nil || *input.Minimum != 1 || input.Maximum == nil || *input.Maximum != want.maximum) {
						t.Errorf("%s.%s range = %v..%v, want 1..%v", want.offering, want.parameter, input.Minimum, input.Maximum, want.maximum)
					}
				}
			}
		}
		if !found {
			t.Errorf("list_offerings does not show %s to the Agent on %s", want.parameter, want.offering)
		}
	}
}
