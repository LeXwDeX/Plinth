package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #316: compatibility endpoints answer errors as {"error": "..."} through
// their own writers; every JSON error body carries the request ID the access
// log records, the same value as the X-Request-ID header.
func TestCompatibilityErrorBodiesCarryTheRequestID(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := &Server{application: product.Application(), logger: logging.New(&bytes.Buffer{}, slog.LevelDebug), mux: mux, providerAssetRoute: route}
	mux.HandleFunc("POST /api/auth/login", func(writer http.ResponseWriter, _ *http.Request) {
		server.writeAuthenticationError(writer, http.StatusUnauthorized, server.application.Authentication.Messages.InvalidCredentials)
	})
	mux.HandleFunc("POST /api/admin/users", func(writer http.ResponseWriter, _ *http.Request) {
		server.writeAdministrationUserError(writer, http.StatusBadRequest, "invalid username")
	})
	mux.HandleFunc("GET /api/legacy", func(writer http.ResponseWriter, _ *http.Request) {
		server.writeLegacyError(writer, http.StatusNotFound, "error.not_found")
	})
	mux.HandleFunc("GET /api/own-id", func(writer http.ResponseWriter, _ *http.Request) {
		// A body that already names a request ID of its own keeps it.
		writeJSON(writer, http.StatusConflict, map[string]any{"error": "busy", "request_id": "consent_1"})
	})
	mux.HandleFunc("GET /api/ok", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"success": true})
	})
	mux.HandleFunc("GET /api/list", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusBadRequest, []string{"not", "an", "object"})
	})
	chain := server.Handler()
	call := func(method, path string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		response := httptest.NewRecorder()
		chain.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader("{}")))
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response, body
	}
	for _, path := range []string{"POST /api/auth/login", "POST /api/admin/users", "GET /api/legacy"} {
		method, target, _ := strings.Cut(path, " ")
		response, body := call(method, target)
		requestID := response.Header().Get("X-Request-ID")
		if response.Code < 400 || !strings.HasPrefix(requestID, "req_") || body["request_id"] != requestID {
			t.Fatalf("%s: status %d header %q body %s", path, response.Code, requestID, response.Body.String())
		}
		if body["error"] == nil || body["error"] == "" {
			t.Fatalf("%s: error text lost: %s", path, response.Body.String())
		}
	}
	if _, body := call(http.MethodGet, "/api/own-id"); body["request_id"] != "consent_1" {
		t.Fatalf("existing request_id replaced: %v", body)
	}
	if _, body := call(http.MethodGet, "/api/ok"); body["request_id"] != nil {
		t.Fatalf("success body changed: %v", body)
	}
	if response, _ := call(http.MethodGet, "/api/list"); strings.TrimSpace(response.Body.String()) != `["not","an","object"]` {
		t.Fatalf("non-object error body changed: %s", response.Body.String())
	}
}
