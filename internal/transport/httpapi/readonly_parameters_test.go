package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPublicOfferingDistinguishesReadOnlyFromHiddenParameters(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(handler, http.MethodGet, "/api/v1/offerings", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var body struct {
		Offerings []struct {
			ID         string `json:"id"`
			Parameters []struct {
				Name             string `json:"name"`
				Fixed            bool   `json:"fixed"`
				AdjustmentPolicy string `json:"adjustment_policy"`
				AdjustmentReason string `json:"adjustment_reason"`
				Default          any    `json:"default"`
				Presentation     struct {
					Surface string `json:"surface"`
				} `json:"presentation"`
			} `json:"parameters"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, offering := range body.Offerings {
		for _, parameter := range offering.Parameters {
			key := offering.ID + ":" + parameter.Name
			switch key {
			case "openai/gpt-image-2/text-to-image:quality":
				if parameter.Fixed || parameter.AdjustmentPolicy != "advised" || parameter.AdjustmentReason == "" || parameter.Default != "max" || parameter.Presentation.Surface != "readonly" {
					t.Fatalf("quality projection = %+v", parameter)
				}
				seen[key] = true
			case "meshy/meshy-7.1/text-to-3d:geometry_resolution":
				if parameter.Fixed || parameter.AdjustmentPolicy != "advised" || parameter.AdjustmentReason == "" || parameter.Default != "4k" || parameter.Presentation.Surface != "readonly" {
					t.Fatalf("geometry projection = %+v", parameter)
				}
				seen[key] = true
			case "meshy/meshy-7.1/text-to-3d:mode":
				if parameter.Presentation.Surface != "hidden" {
					t.Fatalf("private protocol mode exposed: %+v", parameter)
				}
				seen[key] = true
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing projection fixtures: %v", seen)
	}
}
