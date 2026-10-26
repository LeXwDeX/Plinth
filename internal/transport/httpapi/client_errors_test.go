package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
)

// #233: browser errors reach the server log only from signed-in users, within
// size and rate limits, and redacted like every other log line.
func TestClientErrorReportsAreAuthenticatedBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	route := schema.Runtime.HTTP.Routes.ClientErrors
	limits := schema.Runtime.HTTP.ClientErrorReport
	if route == "" || limits.MaxBodyBytes <= 0 || limits.PerOwnerPerMinute <= 0 {
		t.Fatalf("client error contract = %q %+v", route, limits)
	}
	var logs bytes.Buffer
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		identity:    DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-a", Roles: []string{"user"}}, Policy: policy},
		application: schema, logger: logging.New(&logs, slog.LevelDebug), mux: http.NewServeMux(),
	}
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	server.clientErrors.now = func() time.Time { return now }
	server.mux.HandleFunc("POST "+route, server.reportClientError)
	send := func(body string) int {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, route, strings.NewReader(body)))
		return response.Code
	}
	report := map[string]string{"kind": "error-boundary", "message": "boom at https://cdn.test/x.png?sig=topsecret", "stack": "at render (app.js:1)", "page": "/workspace", "revision": "abc", "request_id": "req_1"}
	encoded, _ := json.Marshal(report)
	if code := send(string(encoded)); code != http.StatusNoContent {
		t.Fatalf("report = %d", code)
	}
	if !strings.Contains(logs.String(), `"kind":"error-boundary"`) || !strings.Contains(logs.String(), `"owner_id":"owner-a"`) || strings.Contains(logs.String(), "topsecret") {
		t.Fatalf("report log = %s", logs.String())
	}
	if code := send(`{"kind":"x","unexpected":true}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", code)
	}
	if code := send(`{"kind":"x","message":"` + strings.Repeat("a", int(limits.MaxBodyBytes)) + `"}`); code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d", code)
	}
	for range limits.PerOwnerPerMinute {
		send(string(encoded))
	}
	if code := send(string(encoded)); code != http.StatusTooManyRequests {
		t.Fatalf("over the per-minute limit = %d", code)
	}
	now = now.Add(time.Minute)
	if code := send(string(encoded)); code != http.StatusNoContent {
		t.Fatalf("next minute = %d", code)
	}
	anonymous, err := NewBearerIdentity(map[string]access.Claims{"configured-token-0123456789": {ID: "owner-z"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	server.identity = anonymous
	// A Schema without anonymous limits keeps the route signed-in only;
	// the default Schema's signed-out reporting is covered in #317's test.
	server.application.Runtime.HTTP.ClientErrorReport.AnonymousGlobalPerMinute = 0
	if code := send(string(encoded)); code != http.StatusUnauthorized {
		t.Fatalf("anonymous report = %d", code)
	}
	_ = io.Discard
}
