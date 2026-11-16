package diagbundle

import (
	"strings"
	"testing"
)

// A session ID is a credential-class value: its key is redacted, and the same
// ID must not survive in free text either, such as the request path of an
// access log line (#322). The real form is session_ plus a random run.
func TestSessionIDsDoNotSurviveInFreeText(t *testing.T) {
	const keyed = "session_IaR1PmP3VzKNQFSE1YaxQEMT"
	const pathOnly = "session_7JW_W7RkXcZAYZdPMJSVZvc0"
	scrubber := NewScrubber()
	scrubber.Collect("", map[string]any{"session_id": keyed, "owner_id": "owner-secret-id-1"})

	if got := scrubber.Leaf("session_id", keyed); got != redacted {
		t.Fatalf("keyed session_id = %q, want %q", got, redacted)
	}
	for _, text := range []string{
		"/api/v1/chat/sessions/" + keyed + "/brief",
		"/api/v1/chat/sessions/" + pathOnly + "/brief",
		"GET /api/chat/sessions/" + pathOnly,
	} {
		got := scrubber.Text(text)
		if strings.Contains(got, keyed) || strings.Contains(got, pathOnly) || strings.Contains(got, "session_7JW") {
			t.Errorf("Text(%q) = %q, a session ID survived", text, got)
		}
		if !strings.Contains(got, "/api/") {
			t.Errorf("Text(%q) = %q, the rest of the path was lost", text, got)
		}
	}
	// Lower-case log key names and other identifiers are left alone.
	for _, text := range []string{
		"session_title_refresh_attempts", "session_id", "session_preferences",
		"task_AbCdEfGhIjKlMnOpQrStUv", "req_ab12cd34ef567890",
	} {
		if got := scrubber.Text(text); got != text {
			t.Errorf("Text(%q) = %q, want it unchanged", text, got)
		}
	}
	if leak := scrubber.Leaks([]byte(`{"path":"/x/` + keyed + `"}`)); leak == "" {
		t.Error("Leaks did not report a registered session ID")
	}
	if leak := scrubber.Leaks([]byte(`{"path":"/x/` + pathOnly + `"}`)); leak == "" {
		t.Error("Leaks did not report a session ID in its real form")
	}
}
