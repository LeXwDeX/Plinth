package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedSSORoutesReturnNotFound(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/sso/redirect?redirect=%2F"},
		{http.MethodGet, "/api/auth/sso/callback"},
		{http.MethodPost, "/api/auth/sso/callback"},
		{http.MethodPost, "/api/auth/sso/logout"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://example.com")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound || response.Header().Get("Location") != "" || len(response.Result().Cookies()) != 0 {
				t.Fatalf("retired route = %d; headers %v", response.Code, response.Header())
			}
		})
	}
}
