package catalog_test

import (
	"encoding/json"
	"os"
	"testing"
)

// The consent flag of an extra-credit cost is not a setting the Agent or the
// session settings expose: in chat the approval card is the consent. Every
// Offering of the default Schema (disabled ones too, they can be switched on)
// that declares one must keep it off the generic parameter surfaces.
func TestExtraCreditConsentParameterIsHiddenOnEveryOffering(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Offerings []struct {
			ID   string `json:"id"`
			Cost *struct {
				ConsentParameter string `json:"consent_parameter"`
			} `json:"cost"`
			NativePricing *struct {
				ConsentParameter string `json:"consent_parameter"`
			} `json:"native_pricing"`
			Parameters []struct {
				Name         string `json:"name"`
				Presentation *struct {
					Surface string `json:"surface"`
				} `json:"presentation"`
			} `json:"parameters"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	declared := 0
	for _, offering := range document.Offerings {
		consent := ""
		if offering.Cost != nil {
			consent = offering.Cost.ConsentParameter
		}
		if offering.NativePricing != nil {
			consent = offering.NativePricing.ConsentParameter
		}
		if consent == "" {
			continue
		}
		declared++
		found := false
		for _, parameter := range offering.Parameters {
			if parameter.Name != consent {
				continue
			}
			found = true
			if parameter.Presentation == nil || parameter.Presentation.Surface != "hidden" {
				t.Errorf("%s: consent parameter %q must be presentation.surface=hidden, got %+v",
					offering.ID, parameter.Name, parameter.Presentation)
			}
		}
		if !found {
			t.Errorf("%s: consent parameter %q is not declared", offering.ID, consent)
		}
	}
	// #185 2026-10-21: both Smart Segmentation candidates now freeze their
	// five private exports behind the same hidden consent parameter.
	if declared != 19 {
		t.Fatalf("%d Offerings declare a consent parameter, nineteen priced offerings are expected", declared)
	}
}
