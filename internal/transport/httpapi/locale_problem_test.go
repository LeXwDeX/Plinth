package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func englishBundle(t *testing.T) (catalog.ApplicationSchema, catalog.LocaleBundle) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	return application, application.Localization.Bundles["en"]
}

func serveWithLocale(handler http.Handler, request *http.Request, acceptLanguage string) *httptest.ResponseRecorder {
	if acceptLanguage != "" {
		request.Header.Set("Accept-Language", acceptLanguage)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestProblemDetailFollowsTheRequestLocale(t *testing.T) {
	t.Parallel()
	application, english := englishBundle(t)
	_, handler, _ := newHTTPHarness(t)
	forbidden := func(locale string) map[string]any {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`))
		request.AddCookie(&http.Cookie{Name: "meshyai_session", Value: "test-session"})
		request.Header.Set("X-User-ID", "owner-a")
		response := serveWithLocale(handler, request, locale)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d", response.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return body
	}
	if got := forbidden("en-US")["detail"]; got != english.Messages["error.forbidden"] {
		t.Fatalf("en detail = %v", got)
	}
	if got := forbidden("")["detail"]; got != application.Content.Messages["error.forbidden"] {
		t.Fatalf("zh-CN detail = %v", got)
	}
	if got := forbidden("en-US")["code"]; got != "forbidden" {
		t.Fatalf("code changed with the locale: %v", got)
	}
}

func TestLoginErrorFollowsTheRequestLocale(t *testing.T) {
	t.Parallel()
	application, english := englishBundle(t)
	_, handler, _ := newHTTPHarness(t)
	login := func(locale string) string {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"nouser","password":"wrong"}`))
		request.Header.Set("Content-Type", "application/json")
		response := serveWithLocale(handler, request, locale)
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		text, _ := body[application.Authentication.API.ErrorField].(string)
		return text
	}
	if got := login("en"); got != english.Fields["authentication.messages.invalid_credentials"] {
		t.Fatalf("en login error = %q", got)
	}
	if got := login(""); got != application.Authentication.Messages.InvalidCredentials {
		t.Fatalf("zh-CN login error = %q", got)
	}
}

func TestPendingSessionTitleFollowsTheRequestLocale(t *testing.T) {
	t.Parallel()
	application, english := englishBundle(t)
	_, handler, _ := newHTTPHarness(t)
	session := func(method, target, locale string) map[string]any {
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{}`)
		} else {
			body = strings.NewReader("")
		}
		request := httptest.NewRequest(method, target, body)
		request.Header.Set("X-User-ID", "local-user")
		if method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		response := serveWithLocale(handler, request, locale)
		var decoded any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		if list, ok := decoded.([]any); ok {
			return list[0].(map[string]any)
		}
		return decoded.(map[string]any)
	}
	created := session(http.MethodPost, "/api/chat/sessions", "en")
	if created["title"] != english.Fields["chat.defaults.title"] {
		t.Fatalf("en new session title = %v", created["title"])
	}
	// The stored title stays the Schema default; each reader sees its own locale.
	if listed := session(http.MethodGet, "/api/chat/sessions", ""); listed["title"] != application.Chat.Defaults.Title {
		t.Fatalf("zh-CN listed title = %v", listed["title"])
	}
	if listed := session(http.MethodGet, "/api/chat/sessions", "en"); listed["title"] != english.Fields["chat.defaults.title"] {
		t.Fatalf("en listed title = %v", listed["title"])
	}
}
