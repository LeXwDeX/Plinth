package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMultiviewPublicOfferingPolicyIsExplicit(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	read := func(h http.Handler) []map[string]any {
		t.Helper()
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/offerings?capability=image-to-3d", nil))
		if out.Code != 200 {
			t.Fatal(out.Code)
		}
		var body struct {
			Offerings []map[string]any `json:"offerings"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Offerings
	}
	id := "meshy/meshy-7.1-multiview/image-to-3d"
	found := false
	for _, o := range read(handler) {
		if o["id"] == "meshy/meshy-7.1/image-to-3d" {
			if _, exists := o["reference_selection_policy"]; exists {
				t.Fatal("legacy empty policy changed public shape")
			}
		}
		if o["id"] != id {
			continue
		}
		found = true
		if o["price_mode"] != "native" {
			t.Fatalf("native price missing: %v", o)
		}
		q, ok := o["default_native_quote"].(map[string]any)
		if !ok || q["unit"] != "meshy.credit" || q["total_amount"] != "40" {
			t.Fatalf("default quote=%v", q)
		}
		if _, ok := o["estimated_credits"]; ok {
			t.Fatal("native price advertised as legacy integer")
		}

		if o["reference_selection_policy"] != "preserve" {
			t.Fatalf("missing policy: %v", o)
		}
		if _, leak := o["provider_options"]; leak {
			t.Fatal("private routing leaked")
		}
		var images map[string]any
		for _, p := range o["parameters"].([]any) {
			p := p.(map[string]any)
			if p["name"] == "reference_assets" {
				images = p
			}
		}
		if images == nil || images["max_items"] != float64(4) || images["min_items"] != float64(1) || images["label"] != "参考图（1–4 张，首张正面）" {
			t.Fatal("public source bounds missing")
		}
	}
	if !found {
		t.Fatal("default catalog missing independent Offering")
	}
	for _, path := range []string{"/api/providers", "/api/providers?projection=extended", "/api/providers?capability=image-to-3d&projection=extended"} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
		if out.Code != http.StatusOK || bytes.Contains(out.Body.Bytes(), []byte("meshy-7.1-multiview")) {
			t.Fatalf("native model leaked into integer compatibility discovery: %s %s", path, out.Body.String())
		}
	}

}
