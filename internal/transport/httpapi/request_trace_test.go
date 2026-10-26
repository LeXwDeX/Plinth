package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #233: every 4xx/5xx is logged with a request ID, the route template, the
// public error code and the internal cause; the response carries only the
// request ID and the public detail.
func TestErrorResponsesAreLoggedWithRequestIDAndRedactedCause(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	logger := logging.New(&buffer, slog.LevelDebug)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/things/{sessionID}", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("fail") == "server" {
			writeProblem(writer, http.StatusInternalServerError, "storage_failed", "public text",
				errors.New("open https://bucket.test/a.png?X-Amz-Signature=secretsig: token=abc123secret"))
			return
		}
		writeProblem(writer, http.StatusNotFound, "thing_not_found", "not found")
	})
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Go through the production chain: a middleware that copies the request
	// (WithContext) must not hide the matched route from the access log.
	server := &Server{application: product.Application(), logger: logger, mux: mux, providerAssetRoute: route}
	chain := server.Handler()
	for _, testCase := range []struct {
		query, level, code string
		status             int
	}{
		{"fail=server", "ERROR", "storage_failed", 500},
		{"", "WARN", "thing_not_found", 404},
	} {
		buffer.Reset()
		response := httptest.NewRecorder()
		chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/things/session_one?"+testCase.query, nil))
		requestID := response.Header().Get("X-Request-ID")
		if response.Code != testCase.status || !strings.HasPrefix(requestID, "req_") {
			t.Fatalf("status %d request id %q", response.Code, requestID)
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["request_id"] != requestID || body["code"] != testCase.code {
			t.Fatalf("problem body = %s", response.Body.String())
		}
		if strings.Contains(response.Body.String(), "bucket.test") || strings.Contains(response.Body.String(), "abc123secret") {
			t.Fatalf("internal cause leaked to the client: %s", response.Body.String())
		}
		var line map[string]any
		for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(raw), &entry) == nil && entry["msg"] == "HTTP request" {
				line = entry
			}
		}
		if line == nil {
			t.Fatalf("no request log: %s", buffer.String())
		}
		if line["level"] != testCase.level || line["request_id"] != requestID || line["route"] != "GET /api/v1/things/{sessionID}" ||
			line["session_id"] != "session_one" || line["error_code"] != testCase.code {
			t.Fatalf("request log = %v", line)
		}
		if testCase.status == 500 {
			cause, _ := line["cause"].(string)
			if !strings.Contains(cause, "https://bucket.test/a.png") || strings.Contains(cause, "secretsig") || strings.Contains(cause, "abc123secret") {
				t.Fatalf("cause not logged or not redacted: %q", cause)
			}
		}
	}
}
