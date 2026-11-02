package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Tripo multiview Offering is an independent, non-default model whose
// public projection carries the ordered-reference policy, the view parameter
// and no private routing.
func TestTripoMultiviewPublicOfferingProjection(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/offerings?capability=image-to-3d", nil))
	if out.Code != http.StatusOK {
		t.Fatal(out.Code)
	}
	var body struct {
		Offerings []map[string]any `json:"offerings"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range body.Offerings {
		if o["id"] != "tripo/tripo-v3.1-multiview/image-to-3d" {
			continue
		}
		found = true
		if o["reference_selection_policy"] != "preserve" {
			t.Fatalf("missing policy: %v", o)
		}
		if _, leak := o["provider_options"]; leak {
			t.Fatal("private routing leaked")
		}
		if o["default"] == true || o["recommended"] == true {
			t.Fatalf("multiview must not be default or recommended: %v", o)
		}
		names := map[string]map[string]any{}
		for _, p := range o["parameters"].([]any) {
			p := p.(map[string]any)
			names[p["name"].(string)] = p
		}
		if _, exists := names["enable_image_autofix"]; exists {
			t.Fatal("enable_image_autofix must not be offered")
		}
		images, views := names["reference_assets"], names["reference_views"]
		if images == nil || images["min_items"] != float64(2) || images["max_items"] != float64(4) {
			t.Fatalf("reference bounds: %v", images)
		}
		if views == nil || views["type"] != "string-array" || len(views["options"].([]any)) != 4 {
			t.Fatalf("reference_views: %v", views)
		}
	}
	if !found {
		t.Fatal("default catalog missing the Tripo multiview Offering")
	}
}
