package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// Every registered version builds together, has an alignment profile, and the
// registered providers are Tripo, the OpenAI-compatible gateway, Meshy (one
// version per route version), Hyper3D, the three image providers and the
// matting service.
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
		"openai/images-v1": "/openai/images/v1", "gemini/images-v1": "/gemini/images/v1",
		"jimeng/api-v3": "/jimeng/api/v3", "matting/api-v1": "/matting/api/v1",
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
		"/openai/images/v1/images/generations":                              http.StatusMethodNotAllowed, // routed: POST only
		"/gemini/images/v1/chat/completions":                                http.StatusMethodNotAllowed,
		"/jimeng/api/v3/images/generations":                                 http.StatusMethodNotAllowed,
		"/matting/api/v1/batches/batch_x":                                   http.StatusUnauthorized,
		"/openai/images/v2/images/generations":                              http.StatusNotFound, // an image API version that is not registered
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

// #540: the acceptance round of the three image providers adds steps to their
// alignment profiles (new sizes, 4K, forbidden parameters, several reference
// pictures). Each is a real, billable request on the official side, so each
// must be marked paid (the official run then refuses without -confirm-paid and
// counts them), and each must be sendable to the in-process fake, so the
// offline `fake` run of the profile stays whole.
func TestImageAcceptanceRoundStepsArePaidAndRunOnTheFake(t *testing.T) {
	added := map[string][]string{
		"openai/images-v1": {"generations-n2-1536x1152", "generations-quality-max", "generations-invalid-size", "edits-two-images-mask"},
		"gemini/images-v1": {"generate-16x9-4k", "edit-3x4-two-references", "generate-invalid-ratio"},
		"jimeng/api-v3":    {"generations-4096", "edits-two-references-4096", "generations-sequential-max2", "generations-invalid-size"},
	}
	for version, names := range added {
		profile, ok := Profiles()[version]
		if !ok {
			t.Fatalf("%s has no profile", version)
		}
		byName := map[string]align.Step{}
		for _, step := range profile.Steps {
			byName[step.Name] = step
		}
		for _, name := range names {
			step, ok := byName[name]
			if !ok {
				t.Errorf("%s lacks the acceptance step %q", version, name)
			} else if !step.Paid {
				t.Errorf("%s step %q must be marked paid: the official side bills it when it accepts it", version, name)
			}
		}
		handler, prefix, err := Handler(version)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		bundle, runErr := align.Run(context.Background(), profile.Provider, profile.API, align.Target{
			Name: "fake", BaseURL: server.URL + prefix, Key: "fake-test-key", NoPollDelay: true,
		}, profile.Steps, nil, nil)
		server.Close()
		if runErr != nil {
			t.Fatalf("%s: the profile does not run on the fake: %v", version, runErr)
		}
		answered := map[string]int{}
		for _, obs := range bundle.Obs {
			if obs.Error != "" {
				t.Errorf("%s step %q: %s", version, obs.Step, obs.Error)
			}
			answered[obs.Step] = obs.Status
		}
		for _, name := range names {
			if answered[name] == 0 {
				t.Errorf("%s step %q got no answer from the fake", version, name)
			}
		}
	}
}

// The two-picture edit of the OpenAI profile sends Plinth's real part names:
// image[] once per reference picture and mask for the edit mask.
func TestOpenAIEditStepSendsTwoImagePartsAndAMask(t *testing.T) {
	profile := Profiles()["openai/images-v1"]
	for _, step := range profile.Steps {
		if step.Name != "edits-two-images-mask" {
			continue
		}
		images, masks := 0, 0
		for _, file := range step.Files {
			switch file.Field {
			case "image[]":
				images++
			case "mask":
				masks++
			}
		}
		if images != 2 || masks != 1 {
			t.Fatalf("edit parts: %d image[], %d mask, want 2 and 1", images, masks)
		}
		return
	}
	t.Fatal("no edits-two-images-mask step")
}
