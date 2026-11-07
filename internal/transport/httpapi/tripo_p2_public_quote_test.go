package httpapi_test

import (
	"encoding/json"
	"testing"
)

func TestTripoP2NativePriceIsPublicOnEveryOffering(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	var body struct {
		Offerings []map[string]any `json:"offerings"`
	}
	if err := json.Unmarshal(fetchSurface(t, handler, "/api/v1/offerings"), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"tripo/tripo-p2/image-to-3d":           false,
		"tripo/tripo-p2-multiview/image-to-3d": false,
		"tripo/tripo-p2/text-to-3d":            false,
	}
	for _, offering := range body.Offerings {
		id, _ := offering["id"].(string)
		if _, tracked := want[id]; !tracked {
			continue
		}
		want[id] = true
		quote, ok := offering["default_native_quote"].(map[string]any)
		if !ok || offering["price_mode"] != "native" || quote["unit"] != "tripo.credit" ||
			quote["total_amount"] != "110" || quote["base_amount"] != "100" || quote["extra_amount"] != "10" {
			t.Fatalf("%s public native quote = %+v", id, offering)
		}
		if _, legacy := offering["estimated_credits"]; legacy {
			t.Fatalf("%s leaked legacy estimated credits: %+v", id, offering)
		}
		if _, private := offering["native_pricing"]; private {
			t.Fatalf("%s leaked the private price declaration: %+v", id, offering)
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("%s missing from /api/v1/offerings", id)
		}
	}
}
