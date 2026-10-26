package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// withoutRequestID returns a JSON problem body without its per-request ID, so
// tests can compare two error responses for indistinguishability.
func withoutRequestID(t *testing.T, body []byte) string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return string(body)
	}
	delete(fields, "request_id")
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// isErrorBody reports whether a compatibility error response is exactly
// {"error": message} plus the request ID named in X-Request-ID (#316).
func isErrorBody(response *httptest.ResponseRecorder, message string) bool {
	var body map[string]any
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body) != 2 {
		return false
	}
	requestID := response.Header().Get("X-Request-ID")
	return body["error"] == message && requestID != "" && body["request_id"] == requestID
}
