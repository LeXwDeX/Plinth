package mattingapiv1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// m8: FastAPI (0.122.0 onwards, documented in its "Use Old 403 Authentication
// Error Status Codes" page) answers a missing bearer credential with 401 and a
// WWW-Authenticate header. The committed sample keeps the body only, so the
// header is constructed, like the answer to a key the service does not know.
func TestUnauthorizedAnswersCarryWWWAuthenticate(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, created := submit(f, t)
	task := testkit.Field(t, created, "tasks", 0, "task_id").(string)
	routes := []struct {
		name, method, path string
		body               testkit.Body
	}{
		{"create", "POST", Prefix + "/jobs", jobForm(t, true)},
		{"batch", "GET", Prefix + "/batches/" + created["batch_id"].(string), testkit.Body{}},
		{"download", "GET", Prefix + "/tasks/" + task + "/download", testkit.Body{}},
	}
	for _, route := range routes {
		var payload any
		if route.body.ContentType != "" {
			payload = route.body
		}
		missing := rawWith(f, route.method, route.path, "", payload)
		if missing.status != http.StatusUnauthorized || missing.header.Get("WWW-Authenticate") != "Bearer" ||
			!strings.Contains(missing.body, "Not authenticated") {
			t.Errorf("%s without a key: %d %v %s", route.name, missing.status, missing.header, missing.body)
		}
		for _, wrong := range []struct{ key, scenario string }{{key, "invalid-key"}, {"fake-invalid-key-1", ""}} {
			var extra []string
			if wrong.scenario != "" {
				extra = []string{"X-Fake-Scenario", wrong.scenario}
			}
			answer := rawWith(f, route.method, route.path, wrong.key, payload, extra...)
			if answer.status != http.StatusUnauthorized || answer.header.Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s with a wrong key %q: %d %v %s", route.name, wrong.key, answer.status, answer.header, answer.body)
			}
			if strings.Contains(answer.body, "Not authenticated") || !strings.Contains(answer.body, `"detail"`) {
				t.Errorf("%s: a wrong key is not 'Not authenticated': %s", route.name, answer.body)
			}
		}
	}
	// An ordinary non-empty key is still accepted: only the scenario selects the refusal.
	if status, _ := submit(f, t); status != http.StatusOK {
		t.Fatalf("a non-empty key was refused: %d", status)
	}
}

type rawAnswer struct {
	status int
	header http.Header
	body   string
}

func rawWith(f *testkit.Fixture, method, path, apiKey string, body any, headers ...string) rawAnswer {
	status, header, data := f.Raw(method, path, apiKey, body, headers...)
	return rawAnswer{status, header, string(data)}
}

// m8: an unhandled exception in the service (Starlette's default error
// handler) answers text/plain "Internal Server Error"; the other request-phase
// faults keep their JSON {detail}. Constructed: no 5xx was ever observed.
func TestServerErrorIsPlainText(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, created := submit(f, t)
	paths := []string{
		Prefix + "/batches/" + created["batch_id"].(string),
		Prefix + "/tasks/" + testkit.Field(t, created, "tasks", 0, "task_id").(string) + "/download",
	}
	for _, path := range paths {
		answer := rawWith(f, "GET", path, key, nil, "X-Fake-Scenario", "server-error")
		if answer.status != http.StatusInternalServerError || !strings.HasPrefix(answer.header.Get("Content-Type"), "text/plain") ||
			answer.body != "Internal Server Error" {
			t.Errorf("%s: %d %v %q", path, answer.status, answer.header, answer.body)
		}
	}
	create := jobForm(t, true)
	answer := rawWith(f, "POST", Prefix+"/jobs", key, create, "X-Fake-Scenario", "server-error")
	if answer.status != http.StatusInternalServerError || answer.body != "Internal Server Error" {
		t.Errorf("create: %d %q", answer.status, answer.body)
	}
	limited := rawWith(f, "GET", paths[0], key, nil, "X-Fake-Scenario", "rate-limit")
	if limited.status != http.StatusTooManyRequests || !strings.HasPrefix(limited.header.Get("Content-Type"), "application/json") ||
		limited.header.Get("Retry-After") == "" {
		t.Errorf("rate limit: %d %v %q", limited.status, limited.header, limited.body)
	}
}
