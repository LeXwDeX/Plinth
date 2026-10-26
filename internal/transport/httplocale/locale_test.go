package httplocale_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/transport/httplocale"
)

func TestFromRequestPrefersCookieThenAcceptLanguage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	cookie := application.Localization.Preference.Cookie
	for _, test := range []struct {
		name, cookie, accept, want string
	}{
		{"nothing", "", "", "zh-CN"},
		{"browser english", "", "en-US,en;q=0.9", "en"},
		{"cookie overrides browser", "zh-CN", "en-US", "zh-CN"},
		{"cookie english", "en", "zh-CN", "en"},
		{"unknown cookie ignored", "de", "en-US", "en"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.accept != "" {
				request.Header.Set("Accept-Language", test.accept)
			}
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: cookie, Value: test.cookie})
			}
			if got := httplocale.FromRequest(application, request); got != test.want {
				t.Fatalf("FromRequest = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMarkVariesByLocaleInputs(t *testing.T) {
	response := httptest.NewRecorder()
	httplocale.MarkResponse(response.Header(), "en")
	if got := response.Header().Values("Vary"); len(got) != 2 || got[0] != "Accept-Language" || got[1] != "Cookie" {
		t.Fatalf("Vary = %v", got)
	}
	if got := response.Header().Get("Content-Language"); got != "en" {
		t.Fatalf("Content-Language = %q", got)
	}
}
