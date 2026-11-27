package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #676: /api/version carries the linked release next to the unchanged commit
// field, so a reported problem names both the release and the exact build.
func TestLegacyVersionReportsReleaseNextToCommit(t *testing.T) {
	t.Parallel()
	const commit = "9517d748e7d0736e3d7cc23c79c37873861d9ce6"
	for _, release := range []string{"v1.9.5", ""} {
		server := &Server{
			runtimeVersion: func() string { return commit },
			runtimeRelease: func() string { return release },
		}
		response := httptest.NewRecorder()
		server.legacyVersion(response, httptest.NewRequest(http.MethodGet, "/api/version", nil))
		var body map[string]string
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["version"] != commit {
			t.Fatalf("release %q: version = %q, want the commit", release, body["version"])
		}
		got, present := body["release"]
		if !present || got != release {
			t.Fatalf("release %q: body = %v", release, body)
		}
	}
}
