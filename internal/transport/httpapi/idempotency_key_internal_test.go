package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #302: requireIdempotencyKey is the one place the browser routes that start a
// paid step read their Idempotency-Key. Each route gives the length range its
// service accepts; a key outside it is refused here, before the service would
// turn it into a generic invalid-input answer.
func TestRequireIdempotencyKeyAppliesTheRouteRule(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		rule   idempotencyKeyRule
		header []string
		want   string
		ok     bool
	}{
		"task: no header":        {taskIdempotencyKeyRule, nil, "", false},
		"task: empty header":     {taskIdempotencyKeyRule, []string{""}, "", false},
		"task: blank header":     {taskIdempotencyKeyRule, []string{" \t "}, "", false},
		"task: one character":    {taskIdempotencyKeyRule, []string{"k"}, "k", true},
		"task: trimmed":          {taskIdempotencyKeyRule, []string{"  grant-key "}, "grant-key", true},
		"task: longest":          {taskIdempotencyKeyRule, []string{strings.Repeat("k", 200)}, strings.Repeat("k", 200), true},
		"task: one over":         {taskIdempotencyKeyRule, []string{strings.Repeat("k", 201)}, "", false},
		"task: padding not part": {taskIdempotencyKeyRule, []string{strings.Repeat(" ", 5) + strings.Repeat("k", 200)}, strings.Repeat("k", 200), true},
		"plan: one short":        {planIdempotencyKeyRule, []string{strings.Repeat("k", 15)}, "", false},
		"plan: shortest":         {planIdempotencyKeyRule, []string{strings.Repeat("k", 16)}, strings.Repeat("k", 16), true},
		"plan: longest":          {planIdempotencyKeyRule, []string{strings.Repeat("k", 200)}, strings.Repeat("k", 200), true},
		"plan: one over":         {planIdempotencyKeyRule, []string{strings.Repeat("k", 201)}, "", false},
		"run: no header":         {runIdempotencyKeyRule, nil, "", false},
		"run: one character":     {runIdempotencyKeyRule, []string{"k"}, "k", true},
		"run: no upper bound":    {runIdempotencyKeyRule, []string{strings.Repeat("k", 500)}, strings.Repeat("k", 500), true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil)
			request.Header[http.CanonicalHeaderKey("Idempotency-Key")] = test.header
			response := httptest.NewRecorder()
			key, ok := requireIdempotencyKey(response, request, test.rule, "Schema copy")
			if ok != test.ok || key != test.want {
				t.Fatalf("key=%q ok=%v, want key=%q ok=%v", key, ok, test.want, test.ok)
			}
			if test.ok {
				if response.Body.Len() != 0 {
					t.Fatalf("a usable key was answered: %d %s", response.Code, response.Body.String())
				}
				return
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || body["code"] != "idempotency_key_required" || body["detail"] != "Schema copy" {
				t.Fatalf("refusal = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

// The answer carries only the Schema copy. The access log keeps the internal
// reason for the request ID (#233): what was wrong with the header and its
// length, never the key itself.
func TestRequireIdempotencyKeyLogsTheInternalReasonNotTheKey(t *testing.T) {
	t.Parallel()
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	secretKey := "canary-" + strings.Repeat("k", 300)
	for name, test := range map[string]struct {
		header string
		reason string
	}{
		"blank":    {"   ", "missing or blank"},
		"too long": {secretKey, "at most 200"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			logger := logging.New(&logs, slog.LevelDebug)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/grants", nil)
			request.Header.Set("Idempotency-Key", test.header)
			response := httptest.NewRecorder()
			requestLogMiddleware(logger, []routing.One{route}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requireIdempotencyKey(writer, request, taskIdempotencyKeyRule, "Schema copy")
			})).ServeHTTP(response, request)
			if strings.Contains(response.Body.String(), "canary") || strings.Contains(logs.String(), "canary") {
				t.Fatalf("the key reached the answer or the log:\n%s\n%s", response.Body.String(), logs.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
				t.Fatalf("log %q: %v", logs.String(), err)
			}
			cause, _ := entry["cause"].(string)
			if entry["error_code"] != "idempotency_key_required" || entry["status"] != float64(http.StatusBadRequest) ||
				!strings.Contains(cause, "Idempotency-Key") || !strings.Contains(cause, test.reason) {
				t.Fatalf("access log lost the internal reason: %s", logs.String())
			}
		})
	}
}
