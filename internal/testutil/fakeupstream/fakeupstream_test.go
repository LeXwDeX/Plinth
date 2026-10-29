package fakeupstream_test

import (
	"encoding/json"
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

// Only the first issue(s) of a task's artifact URLs expire, and only under the
// expired scenario; every other URL is left exactly as built. A renewal makes the
// URLs issued afterwards valid and leaves the earlier ones expired.
func TestArtifactURLsExpireOnTheFirstIssuesOnly(t *testing.T) {
	env := fakeupstream.NewEnv(fakeupstream.Config{})
	first, again := env.IssueArtifactURLs("task-a"), env.IssueArtifactURLs("task-a")
	if first != 1 || again != 2 || env.IssueArtifactURLs("task-b") != 1 {
		t.Fatalf("issues = %d, %d: the count is per task", first, again)
	}
	const base = "http://x/files/a.glb"
	if got := env.SignArtifactURL(base, fakeupstream.ScenarioNone, 1); got != base {
		t.Fatalf("a normal URL changed: %s", got)
	}
	expired := env.SignArtifactURL(base, fakeupstream.ScenarioArtifactExpired, first)
	second := env.SignArtifactURL(base+"?a=b", fakeupstream.ScenarioArtifactExpired, again)
	if !strings.HasSuffix(expired, "?"+fakeupstream.IssueParam+"=1&"+fakeupstream.EpochParam+"=0") ||
		!strings.HasSuffix(second, "&"+fakeupstream.IssueParam+"=2&"+fakeupstream.EpochParam+"=0") {
		t.Fatalf("signed URLs = %s, %s", expired, second)
	}
	asked := func(rawURL string) *http.Request { return httptest.NewRequest(http.MethodGet, rawURL, nil) }
	if !fakeupstream.ArtifactURLExpired(asked(expired), fakeupstream.ScenarioArtifactExpired, 1) ||
		fakeupstream.ArtifactURLExpired(asked(second), fakeupstream.ScenarioArtifactExpired, 1) ||
		fakeupstream.ArtifactURLExpired(asked(expired), fakeupstream.ScenarioNone, 1) {
		t.Fatal("only the first issue of an artifact-expired task may be expired")
	}
	if !fakeupstream.ArtifactURLExpired(asked(second), fakeupstream.ScenarioArtifactExpired, 2) {
		t.Fatal("two expired issues expire the second one too")
	}
	// A renewal: URLs issued before it stay expired, URLs issued after it are valid
	// however many issues the task expires.
	if env.RenewArtifactURLs() != 1 {
		t.Fatal("the first renewal is epoch 1")
	}
	renewed := env.SignArtifactURL(base, fakeupstream.ScenarioArtifactExpired, env.IssueArtifactURLs("task-a"))
	if fakeupstream.ArtifactURLExpired(asked(renewed), fakeupstream.ScenarioArtifactExpired, 1000) ||
		!fakeupstream.ArtifactURLExpired(asked(expired), fakeupstream.ScenarioArtifactExpired, 1000) {
		t.Fatalf("renewal: issued after is valid, issued before stays expired (%s)", renewed)
	}
	for _, tc := range []struct {
		header http.Header
		want   int
	}{
		{http.Header{}, 1},
		{http.Header{"Authorization": {"Bearer fake-artifact-expired"}}, 1},
		{http.Header{"Authorization": {"Bearer fake-artifact-expired-3"}}, 3},
		{http.Header{"Authorization": {"Bearer fake-artifact-expired-x"}}, 1},
		{http.Header{"X-Fake-Scenario": {"artifact-expired"}, "X-Fake-Fail-Times": {"2"}}, 2},
	} {
		if got := fakeupstream.ExpiredIssues(request(t, tc.header)); got != tc.want {
			t.Errorf("ExpiredIssues(%v) = %d, want %d", tc.header, got, tc.want)
		}
	}
	if fakeupstream.ScenarioFromKey("fake-artifact-expired-x") != fakeupstream.ScenarioArtifactExpired {
		t.Fatal("the expired scenario is selectable by key prefix")
	}
}

// The control route starts a new epoch over HTTP.
func TestRenewRouteStartsANewEpoch(t *testing.T) {
	server, err := fakeupstream.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(server)
	defer front.Close()
	for want := 1; want <= 2; want++ {
		response, err := http.Post(front.URL+fakeupstream.RenewPath, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Epoch int `json:"epoch"`
		}
		_ = json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || body.Epoch != want {
			t.Fatalf("renew %d = %d, epoch %d", want, response.StatusCode, body.Epoch)
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

// The fake upstream is test tooling: no binary that ships may link it.
func TestProductionBinariesDoNotLinkFakeUpstream(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "fake-upstream") {
			continue
		}
		command := exec.Command("go", "list", "-deps", "./cmd/"+entry.Name())
		command.Dir = root
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", entry.Name(), err, out)
		}
		if strings.Contains(string(out), "internal/testutil/fakeupstream") {
			t.Errorf("cmd/%s links the fake upstream", entry.Name())
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("checked only %d commands", checked)
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
