package httpapi_test

import (
	"encoding/json"
	"testing"
)

// #324: the five export formats of Tripo segmentation are fixed, so the price the client is
// given is the one the step always costs (base + every fixed export), not the base alone.
// The low and the high end are equal, so no client can draw a "40–90" range from it.
func TestOfferingProjectionQuotesTheFixedExportsOfTripoSegmentation(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"admin"}, nil, nil, nil, nil, nil)
	var listed struct {
		Offerings []struct {
			ID        string `json:"id"`
			Estimated int    `json:"estimated_credits"`
			Maximum   int    `json:"maximum_estimated_credits"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(fetchSurface(t, handler, "/api/v1/offerings?capability=mesh-segmentation"), &listed); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"tripo/geometry/segment": 90, "tripo/geometry/smartsegment-model": 105}
	seen := 0
	for _, offering := range listed.Offerings {
		credits, ok := want[offering.ID]
		if !ok {
			continue
		}
		seen++
		if offering.Estimated != credits || offering.Maximum > credits {
			t.Errorf("%s estimated=%d maximum=%d; want a single %d", offering.ID, offering.Estimated, offering.Maximum, credits)
		}
	}
	if seen != len(want) {
		t.Fatalf("listed %d of %d Tripo segmentation offerings: %+v", seen, len(want), listed.Offerings)
	}
}
