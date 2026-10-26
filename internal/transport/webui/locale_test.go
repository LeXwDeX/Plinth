package webui_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/transport/webui"
)

func TestShellFollowsTheRequestLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	handler, err := webui.New(webui.Config{
		Assets: fs.FS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(
			`<html lang="{{.Locale}}"><title>{{.Title}}</title><script>window.__ART_AI_PRODUCT_SCHEMA__={{.Schema}};</script></html>`,
		)}}),
		Application: application,
		API:         http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(configure func(*http.Request)) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/login", nil)
		configure(request)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("shell status = %d", response.Code)
		}
		return response
	}

	english := serve(func(request *http.Request) { request.Header.Set("Accept-Language", "en-GB,en;q=0.9") })
	body := english.Body.String()
	englishTitle := application.Localization.Bundles["en"].Fields["content.brand.metadata_title"]
	for _, expected := range []string{`<html lang="en">`, "<title>" + englishTitle + "</title>", `"active_locale":"en"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("en shell lacks %q: %s", expected, body[:min(len(body), 400)])
		}
	}
	if english.Header().Get("Content-Language") != "en" || len(english.Header().Values("Vary")) == 0 {
		t.Fatalf("en shell headers = %v", english.Header())
	}

	chosen := serve(func(request *http.Request) {
		request.Header.Set("Accept-Language", "en-GB")
		request.AddCookie(&http.Cookie{Name: application.Localization.Preference.Cookie, Value: "zh-CN"})
	})
	if body := chosen.Body.String(); !strings.Contains(body, `<html lang="zh-CN">`) ||
		!strings.Contains(body, "<title>"+application.Content.Brand.MetadataTitle+"</title>") {
		t.Fatalf("persisted zh-CN choice ignored: %s", body[:min(len(body), 400)])
	}
}
