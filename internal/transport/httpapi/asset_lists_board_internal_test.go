package httpapi

import (
	"encoding/json"
	"testing"
)

// #331: the board's representative asset follows the completion snapshot's
// role priority: the 3D model first, then a rendered image, then the first
// asset. A missing, empty or malformed snapshot names none.
func TestAssetListRepresentativeAssetRolePriority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		result string
		want   string
	}{
		{"no result", ``, ""},
		{"empty object", `{}`, ""},
		{"empty assets", `{"assets":[]}`, ""},
		{"model wins over image and order", `{"assets":[` +
			`{"id":"asset_image","role":"image"},{"id":"asset_model","role":"model"},` +
			`{"id":"asset_concept","role":"concept"}]}`, "asset_model"},
		{"image wins over the first asset", `{"assets":[` +
			`{"id":"asset_concept","role":"concept"},{"id":"asset_image","role":"image"}]}`,
			"asset_image"},
		{"first asset is the fallback", `{"assets":[` +
			`{"id":"asset_concept","role":"concept"},{"id":"asset_other","role":"other"}]}`,
			"asset_concept"},
		{"an unnamed model is skipped", `{"assets":[` +
			`{"id":"","role":"model"},{"id":"asset_image","role":"image"}]}`, "asset_image"},
		{"malformed assets", `{"assets":"x"}`, ""},
		{"not an object", `[]`, ""},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := assetListRepresentativeAsset(json.RawMessage(test.result)); got != test.want {
				t.Fatalf("representative = %q, want %q", got, test.want)
			}
		})
	}
}
