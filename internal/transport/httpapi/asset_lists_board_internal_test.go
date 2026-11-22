package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
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

// #425: a project that reached its asset bound while a batch was being decided
// answers 409 asset_list_asset_limit, naming the item and the bound in the
// request's language (the bound moved between the count and the write
// transaction, so nothing was written).
func TestAssetListAssetLimitAnswersAConflictWithTheSchemaCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{application: application, texts: texts}
	for _, test := range []struct {
		locale string
		want   string
	}{
		{"", "项目资产已达上限 200 个"},
		{"en", "The project already has its maximum of 200 assets"},
	} {
		t.Run(test.locale, func(t *testing.T) {
			trace := newRequestTrace()
			trace.locale = test.locale
			trace.texts = texts
			writer := &statusRecorder{ResponseWriter: httptest.NewRecorder(), trace: trace}
			server.writeAssetListError(writer,
				&assetlist.ReviewAssetLimitError{Name: "Hero", Maximum: 200}, "")
			response := writer.ResponseWriter.(*httptest.ResponseRecorder)
			if response.Code != http.StatusConflict {
				t.Fatalf("status = %d %s", response.Code, response.Body.String())
			}
			var body struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Code != "asset_list_asset_limit" {
				t.Fatalf("code = %q; want asset_list_asset_limit", body.Code)
			}
			if !strings.Contains(body.Detail, test.want) ||
				!strings.Contains(body.Detail, "Hero") {
				t.Fatalf("detail = %q; want %q naming the item", body.Detail, test.want)
			}
		})
	}
}
