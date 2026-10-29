package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// Every registered version builds together, has an alignment profile, and the
// registered providers are Tripo, the OpenAI-compatible gateway, Meshy (one
// version per route version) and Hyper3D.
func TestVersionsAreRegisteredExplicitlyWithProfiles(t *testing.T) {
	server, err := New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	profiles := Profiles()
	got := map[string]string{}
	for _, version := range server.Versions() {
		name := version.Provider + "/" + version.API
		got[name] = version.Prefix
		profile, ok := profiles[name]
		if !ok || len(profile.Steps) == 0 || profile.Official == "" {
			t.Errorf("%s has no usable alignment profile", name)
		}
		seen := map[string]bool{}
		for _, step := range profile.Steps {
			if seen[step.Name] {
				t.Errorf("%s: duplicate step name %q", name, step.Name)
			}
			seen[step.Name] = true
		}
		if len(version.Endpoints) == 0 {
			t.Errorf("%s declares no endpoints", name)
		}
	}
	want := map[string]string{
		"tripo/v3": "/tripo/v3", "openai/v1": "/openai/v1", "meshy/openapi-v1": "/meshy/openapi/v1",
		"meshy/openapi-v2": "/meshy/openapi/v2", "hyper3d/api-v2": "/hyper3d/api/v2",
	}
	if len(got) != len(want) || len(profiles) != len(got) {
		t.Fatalf("versions %v, profiles %d", got, len(profiles))
	}
	for name, prefix := range want {
		if got[name] != prefix {
			t.Errorf("%s mounted at %q, want %q", name, got[name], prefix)
		}
	}
}

func TestEveryVersionIsServedUnderItsOwnPrefixOnly(t *testing.T) {
	server, err := New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(server)
	defer front.Close()
	for path, want := range map[string]int{
		"/tripo/v3/account/balance": http.StatusUnauthorized, // routed: it asks for a key
		"/openai/v1/models":         http.StatusUnauthorized,
		"/meshy/openapi/v1/balance": http.StatusUnauthorized,
		"/meshy/openapi/v2/text-to-3d/01a18514-059e-73f7-a0ce-0f88a12c9300": http.StatusUnauthorized,
		"/hyper3d/api/v2/check_balance":                                     http.StatusUnauthorized,
		"/meshy/openapi/v3/balance":                                         http.StatusNotFound, // a Meshy route version that is not registered
		"/meshy/balance":                                                    http.StatusNotFound, // the shared Meshy base alone is not a version
		"/v3/account/balance":                                               http.StatusNotFound, // the official prefix alone is not routed
		"/tripo/v4/account/balance":                                         http.StatusNotFound, // a version that is not registered
	} {
		response, err := http.Get(front.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, response.StatusCode, want)
		}
	}
	if _, _, err := Handler("tripo/v9"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("an unknown version must be refused: %v", err)
	}
}
