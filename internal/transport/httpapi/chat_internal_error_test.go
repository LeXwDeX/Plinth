package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #233: a chat 500 keeps its public text but the access log names the cause;
// writeChatInternalError used to be called without the error in hand.
func TestChatInternalErrorLogsItsCause(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := logging.New(&logs, slog.LevelDebug)
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-a", Roles: []string{"user"}}, Policy: policy}
	server := &Server{application: product.Application(), logger: logger, mux: http.NewServeMux(), identity: identity}
	server.mux.HandleFunc("PATCH /chat/{sessionID}", func(writer http.ResponseWriter, request *http.Request) {
		principal, err := identity.Identify(request)
		if err != nil {
			t.Fatal(err)
		}
		server.legacyRenameChatSession(writer, request, principal)
	})
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	requestLogMiddleware(logger, route, nil, server.mux).ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/chat/session_one", strings.NewReader(`{"title":`)))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	var line map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(raw), &entry) == nil && entry["msg"] == "HTTP request" {
			line = entry
		}
	}
	if cause, _ := line["cause"].(string); !strings.Contains(cause, "EOF") {
		t.Fatalf("access log has no cause: %v", line)
	}
}
