package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #317 (LOG-04): pages shown before sign-in (login, an expired session) can
// report browser errors too, under tighter Schema limits: a smaller body,
// shorter text, a per-client and a global per-minute cap, the same field
// whitelist, the page path without query or fragment, and the request ID in
// the log line. Cross-site reports are still refused.
func TestAnonymousClientErrorReportsAreBoundedAndLogged(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	route := schema.Runtime.HTTP.Routes.ClientErrors
	limits := schema.Runtime.HTTP.ClientErrorReport
	if limits.AnonymousMaxBodyBytes <= 0 || limits.AnonymousMaxBodyBytes > limits.MaxBodyBytes ||
		limits.AnonymousPerClientPerMinute <= 0 || limits.AnonymousPerClientPerMinute > limits.PerOwnerPerMinute ||
		limits.AnonymousGlobalPerMinute < limits.AnonymousPerClientPerMinute ||
		limits.AnonymousMaxTextCharacters <= 0 || limits.AnonymousMaxTextCharacters > limits.MaxTextCharacters {
		t.Fatalf("anonymous limits = %+v", limits)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	signedOut, err := NewBearerIdentity(map[string]access.Claims{"configured-token-0123456789": {ID: "owner-z"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	providerAsset, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := &Server{
		identity: signedOut, application: schema, logger: logging.New(&logs, slog.LevelDebug),
		mux: http.NewServeMux(), providerAssetRoute: providerAsset,
	}
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	server.clientErrors.now = func() time.Time { return now }
	server.mux.HandleFunc("POST "+route, server.reportClientError)
	chain := server.Handler()
	send := func(client, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
		request.RemoteAddr = net.JoinHostPort(client, "40000")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.com")
		request.Host = "example.com"
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		chain.ServeHTTP(response, request)
		return response
	}
	long := strings.Repeat("x", limits.AnonymousMaxTextCharacters+50)
	report := fmt.Sprintf(`{"kind":"window-error","message":%q,"stack":"at login (app.js:1)","page":"/login?next=/s/abc&token=topsecret#frag","revision":"r1","request_id":"req_prev"}`, long)
	response := send("198.51.100.7", report, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("anonymous report = %d %s", response.Code, response.Body.String())
	}
	var line map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var candidate map[string]any
		if json.Unmarshal([]byte(raw), &candidate) == nil && candidate["msg"] == "前端错误" {
			line = candidate
		}
	}
	if line == nil {
		t.Fatalf("no client error line: %s", logs.String())
	}
	requestID := response.Header().Get("X-Request-ID")
	if line["level"] != "ERROR" || line["anonymous"] != true || line["owner_id"] != nil || line["request_id"] != requestID ||
		line["page"] != "/login" || line["related_request_id"] != "req_prev" || line["kind"] != "window-error" {
		t.Fatalf("anonymous log = %v (request %q)", line, requestID)
	}
	if message, _ := line["message"].(string); len([]rune(message)) > limits.AnonymousMaxTextCharacters+1 {
		t.Fatalf("anonymous message not truncated: %d", len([]rune(message)))
	}
	if strings.Contains(logs.String(), "topsecret") {
		t.Fatalf("query string reached the log: %s", logs.String())
	}
	// Field whitelist and the smaller body limit.
	if code := send("198.51.100.8", `{"kind":"x","cookie":"sid=1"}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", code)
	}
	if code := send("198.51.100.8", `{"kind":"x","message":"`+strings.Repeat("a", int(limits.AnonymousMaxBodyBytes))+`"}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("oversized anonymous body = %d", code)
	}
	// Cross-site reports stay refused (SEC-03).
	if code := send("198.51.100.9", `{"kind":"x"}`, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}).Code; code != http.StatusForbidden {
		t.Fatalf("cross-site anonymous report = %d", code)
	}
	// Per client: one address (an IPv6 /64) gets AnonymousPerClientPerMinute.
	small := `{"kind":"x","message":"m"}`
	for index := range limits.AnonymousPerClientPerMinute {
		if code := send(fmt.Sprintf("2001:db8:1:2::%x", index+1), small, nil).Code; code != http.StatusNoContent {
			t.Fatalf("report %d from the /64 = %d", index, code)
		}
	}
	if code := send("2001:db8:1:2::ffff", small, nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("over the per-client limit = %d", code)
	}
	// Global: rotating addresses cannot exceed AnonymousGlobalPerMinute.
	accepted := 0
	for index := range limits.AnonymousGlobalPerMinute + 5 {
		if send(fmt.Sprintf("203.0.113.%d", index%250+1), small, nil).Code == http.StatusNoContent {
			accepted++
		}
	}
	if accepted > limits.AnonymousGlobalPerMinute {
		t.Fatalf("anonymous reports accepted in one minute = %d, global cap %d", accepted, limits.AnonymousGlobalPerMinute)
	}
	if code := send("192.0.2.200", small, nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("over the global anonymous limit = %d", code)
	}
	now = now.Add(time.Minute)
	if code := send("192.0.2.200", small, nil).Code; code != http.StatusNoContent {
		t.Fatalf("next minute = %d", code)
	}
}
