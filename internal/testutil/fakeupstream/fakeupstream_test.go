package fakeupstream_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

func request(t *testing.T, header http.Header) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	for name, values := range header {
		r.Header[name] = values
	}
	return r
}

func TestScenarioSelectionByHeaderAndKeyPrefix(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   fakeupstream.Scenario
	}{
		{"none", http.Header{"Authorization": {"Bearer plain-key"}}, fakeupstream.ScenarioNone},
		{"header", http.Header{"X-Fake-Scenario": {"rate-limit"}}, fakeupstream.ScenarioRateLimit},
		{"key exact", http.Header{"Authorization": {"Bearer fake-timeout"}}, fakeupstream.ScenarioTimeout},
		{"key with suffix", http.Header{"Authorization": {"Bearer fake-insufficient-balance-key1"}}, fakeupstream.ScenarioInsufficientBalance},
		{"key quota", http.Header{"Authorization": {"Bearer fake-key-quota_2"}}, fakeupstream.ScenarioKeyQuota},
		{"artifact 404", http.Header{"Authorization": {"Bearer fake-artifact-404-a"}}, fakeupstream.ScenarioArtifactNotFound},
		{"header wins over key", http.Header{"X-Fake-Scenario": {"server-error"}, "Authorization": {"Bearer fake-timeout"}}, fakeupstream.ScenarioServerError},
		{"unknown header value", http.Header{"X-Fake-Scenario": {"nonsense"}}, fakeupstream.ScenarioNone},
		{"prefix only", http.Header{"Authorization": {"Bearer fake-"}}, fakeupstream.ScenarioNone},
		{"not a whole word", http.Header{"Authorization": {"Bearer fake-timeouts"}}, fakeupstream.ScenarioNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fakeupstream.ScenarioOf(request(t, tc.header)); got != tc.want {
				t.Fatalf("scenario = %q, want %q", got, tc.want)
			}
		})
	}
	for _, scenario := range fakeupstream.Scenarios {
		if got := fakeupstream.ScenarioFromKey("fake-" + string(scenario)); got != scenario {
			t.Errorf("key for %q selected %q", scenario, got)
		}
	}
}

func TestInterceptHonorsPhaseAndFailTimes(t *testing.T) {
	env := fakeupstream.NewEnv(fakeupstream.Config{TimeoutHold: 10 * time.Millisecond})
	render := func(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(scenario))
	}
	run := func(header http.Header, phase fakeupstream.Phase) (handled bool, code int) {
		rec := httptest.NewRecorder()
		handled = env.Intercept(rec, request(t, header), phase, render)
		return handled, rec.Code
	}
	if handled, _ := run(http.Header{"X-Fake-Scenario": {"insufficient-balance"}}, fakeupstream.PhaseRead); handled {
		t.Fatal("a balance scenario must only fail task creation")
	}
	if handled, code := run(http.Header{"X-Fake-Scenario": {"insufficient-balance"}}, fakeupstream.PhaseCreate); !handled || code != http.StatusTeapot {
		t.Fatalf("create: handled=%t code=%d", handled, code)
	}
	if handled, _ := run(http.Header{"X-Fake-Scenario": {"task-failed"}}, fakeupstream.PhaseCreate); handled {
		t.Fatal("a task-level scenario is not a request fault")
	}
	// FailTimes: the first matching request fails, the second passes.
	limited := http.Header{"X-Fake-Scenario": {"rate-limit"}, "X-Fake-Fail-Times": {"1"}, "Authorization": {"Bearer k"}}
	if handled, _ := run(limited, fakeupstream.PhaseRead); !handled {
		t.Fatal("first request must fail")
	}
	if handled, _ := run(limited, fakeupstream.PhaseRead); handled {
		t.Fatal("second request must pass after FailTimes=1")
	}
}

func TestNewRejectsBrokenVersions(t *testing.T) {
	ok := func(*fakeupstream.Env) (http.Handler, error) { return http.NotFoundHandler(), nil }
	base := fakeupstream.Version{Provider: "p", API: "v1", Prefix: "/p/v1", Handler: ok}
	cases := map[string][]fakeupstream.Version{
		"no handler":     {{Provider: "p", API: "v1", Prefix: "/p/v1"}},
		"bad prefix":     {{Provider: "p", API: "v1", Prefix: "p/v1", Handler: ok}},
		"trailing slash": {{Provider: "p", API: "v1", Prefix: "/p/v1/", Handler: ok}},
		"same prefix":    {base, {Provider: "q", API: "v1", Prefix: "/p/v1", Handler: ok}},
		"nested prefix":  {base, {Provider: "q", API: "v1", Prefix: "/p/v1/sub", Handler: ok}},
		"constructed without documentation": {{Provider: "p", API: "v1", Prefix: "/p/v1", Handler: ok,
			Endpoints: []fakeupstream.Endpoint{{Method: "GET", Path: "/a", Evidence: fakeupstream.EvidenceConstructed}}}},
		"real without sample": {{Provider: "p", API: "v1", Prefix: "/p/v1", Handler: ok,
			Endpoints: []fakeupstream.Endpoint{{Method: "GET", Path: "/a", Evidence: fakeupstream.EvidenceReal}}}},
	}
	for name, versions := range cases {
		if _, err := fakeupstream.New(fakeupstream.Config{}, versions...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := fakeupstream.New(fakeupstream.Config{}, base, fakeupstream.Version{Provider: "p", API: "v2", Prefix: "/p/v2", Handler: ok}); err != nil {
		t.Fatalf("a second API version under its own prefix must register: %v", err)
	}
}

func TestShapeDiffReportsFieldsAndTypes(t *testing.T) {
	want, err := fakeupstream.ShapeOf([]byte(`{"a":1,"b":{"c":"x","d":[{"e":null}]},"f":true}`))
	if err != nil {
		t.Fatal(err)
	}
	same, _ := fakeupstream.ShapeOf([]byte(`{"f":false,"b":{"d":[{"e":null},{"e":null}],"c":"yy"},"a":9}`))
	if diff := fakeupstream.DiffShape(want, same); !diff.Empty() {
		t.Fatalf("values and order must not matter: %s", diff)
	}
	other, _ := fakeupstream.ShapeOf([]byte(`{"a":"1","b":{"c":"x","d":[{"e":null}],"g":1}}`))
	diff := fakeupstream.DiffShape(want, other)
	if !slices.Equal(diff.Missing, []string{"$.f"}) || !slices.Equal(diff.Extra, []string{"$.b.g"}) || len(diff.TypeMismatch) != 1 {
		t.Fatalf("diff = %s", diff)
	}
}

func TestCorruptBytesLoseTheirMagic(t *testing.T) {
	glb, err := fakeupstream.ModelFile("glb")
	if err != nil {
		t.Fatal(err)
	}
	broken := fakeupstream.Corrupt(glb.Data)
	if len(broken) >= len(glb.Data) || string(broken[:4]) == "glTF" {
		t.Fatalf("corrupt bytes kept the format (len %d of %d)", len(broken), len(glb.Data))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	return root
}

const (
	testutilPrefix = "github.com/LeXwDeX/Plinth/internal/testutil/"
	modulePath     = "github.com/LeXwDeX/Plinth"
)

// isTestTooling names the commands that exist for tests: they may link internal/testutil.
func isTestTooling(command string) bool { return strings.HasPrefix(command, "fake-upstream") }

// Everything under internal/testutil (the fake upstream, wirefixture and any package added later) is test
// tooling: no binary that ships may link any of it, directly or through another package.
func TestProductionBinariesDoNotLinkAnyTestutilPackage(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	checked, tooling := 0, 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		command := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/"+entry.Name())
		command.Dir = root
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", entry.Name(), err, out)
		}
		var linked []string
		for _, path := range strings.Fields(string(out)) {
			if strings.HasPrefix(path, testutilPrefix) {
				linked = append(linked, strings.TrimPrefix(path, modulePath+"/"))
			}
		}
		if isTestTooling(entry.Name()) {
			tooling++
			if len(linked) == 0 {
				t.Errorf("cmd/%s no longer links internal/testutil: the check below would pass vacuously", entry.Name())
			}
			continue
		}
		if len(linked) > 0 {
			t.Errorf("cmd/%s links test tooling: %s", entry.Name(), strings.Join(linked, ", "))
		}
		checked++
	}
	if checked < 3 || tooling < 1 {
		t.Fatalf("checked %d shipping commands and %d tooling commands", checked, tooling)
	}
}

// A production package never imports test tooling: only internal/testutil itself and the fake-upstream
// commands do (test files are not part of go list's Imports).
func TestOnlyTestToolingImportsTestutil(t *testing.T) {
	root := moduleRoot(t)
	command := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
	command.Dir = root
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, out)
	}
	scanned := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		owner := fields[0]
		scanned++
		if strings.HasPrefix(owner, testutilPrefix) || strings.HasPrefix(strings.TrimPrefix(owner, modulePath+"/"), "cmd/fake-upstream") {
			continue
		}
		for _, imported := range fields[1:] {
			if strings.HasPrefix(imported, testutilPrefix) {
				t.Errorf("%s imports %s", owner, imported)
			}
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d packages", scanned)
	}
}

// The runtime image builds an explicit list of commands, never ./... or the
// fake upstream.
func TestRuntimeImageBuildsOnlyListedCommands(t *testing.T) {
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "Dockerfile.go-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "fake-upstream") || strings.Contains(text, "fakeupstream") {
		t.Fatal("Dockerfile.go-runtime mentions the fake upstream")
	}
	if strings.Contains(text, "go build ./...") || strings.Contains(text, "./...") {
		t.Fatal("Dockerfile.go-runtime must build explicit commands, not ./...")
	}
	if !strings.Contains(text, "./cmd/art-ai") {
		t.Fatal("Dockerfile.go-runtime no longer builds cmd/art-ai")
	}
}
