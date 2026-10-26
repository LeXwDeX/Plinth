package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type localizedSchemaResponse struct {
	Schema struct {
		Content struct {
			Messages map[string]string `json:"messages"`
		} `json:"content"`
		Localization struct {
			ActiveLocale string `json:"active_locale"`
		} `json:"localization"`
	} `json:"schema"`
}

func requestSchema(t *testing.T, handler http.Handler, configure func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil)
	if configure != nil {
		configure(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestSchemaEndpointProjectsTheRequestLocale(t *testing.T) {
	_, handler, _ := newHTTPHarnessWithAuthentication(t, []string{"admin"}, nil, nil)

	chinese := requestSchema(t, handler, nil)
	english := requestSchema(t, handler, func(request *http.Request) {
		request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	})
	chosen := requestSchema(t, handler, func(request *http.Request) {
		request.Header.Set("Accept-Language", "en-US")
		request.AddCookie(&http.Cookie{Name: "plinth_locale", Value: "zh-CN"})
	})
	decode := func(response *httptest.ResponseRecorder) localizedSchemaResponse {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("schema status = %d", response.Code)
		}
		var decoded localizedSchemaResponse
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	zh, en, cookie := decode(chinese), decode(english), decode(chosen)
	if zh.Schema.Localization.ActiveLocale != "zh-CN" || en.Schema.Localization.ActiveLocale != "en" || cookie.Schema.Localization.ActiveLocale != "zh-CN" {
		t.Fatalf("active locales = %q %q %q", zh.Schema.Localization.ActiveLocale, en.Schema.Localization.ActiveLocale, cookie.Schema.Localization.ActiveLocale)
	}
	if zh.Schema.Content.Messages["ui.header.logout"] == en.Schema.Content.Messages["ui.header.logout"] {
		t.Fatalf("en logout copy equals zh-CN: %q", en.Schema.Content.Messages["ui.header.logout"])
	}
	if chinese.Header().Get("ETag") == english.Header().Get("ETag") {
		t.Fatal("zh-CN and en share an ETag; a revalidation could swap languages")
	}
	if english.Header().Get("Content-Language") != "en" || len(english.Header().Values("Vary")) == 0 {
		t.Fatalf("locale headers = %v", english.Header())
	}

	// A cached English copy must not be confirmed for a Chinese request.
	stale := requestSchema(t, handler, func(request *http.Request) {
		request.Header.Set("If-None-Match", english.Header().Get("ETag"))
	})
	if stale.Code != http.StatusOK {
		t.Fatalf("zh-CN request with the en ETag = %d, want 200", stale.Code)
	}
	fresh := requestSchema(t, handler, func(request *http.Request) {
		request.Header.Set("Accept-Language", "en")
		request.Header.Set("If-None-Match", english.Header().Get("ETag"))
	})
	if fresh.Code != http.StatusNotModified {
		t.Fatalf("en request with the en ETag = %d, want 304", fresh.Code)
	}
}
