package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

func worktreeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The official run's upstream recorder (#220) is off without switches and
// refuses half-set ones before any request is sent.
func TestOfficialRecorderIsOffByDefaultAndRefusesHalfSetSwitches(t *testing.T) {
	profile := registry.Profiles()["tripo/v3"]
	t.Setenv(wirefixture.EnvUpstreamRecordDir, "")
	t.Setenv(wirefixture.EnvUpstreamRecordAck, "")
	recorder, _, err := officialRecorder(profile, worktreeRoot(t), profile.Official)
	if err != nil || recorder != nil {
		t.Fatalf("recorder must be off by default: %v", err)
	}
	t.Setenv(wirefixture.EnvUpstreamRecordAck, "1")
	if _, _, err := officialRecorder(profile, worktreeRoot(t), profile.Official); !errors.Is(err, wirefixture.ErrUpstreamRecordRefused) {
		t.Fatalf("ACK without a directory must be refused, got %v", err)
	}
}

// The labeled Rodin request (#376) is one paid submission with a status and a
// download step behind it. Without -confirm-paid the official run refuses before
// it reads a key or sends anything, and says how many paid requests it would
// send: the one submission.
func TestOfficialRefusesTheLabeledRodinRequestWithoutConfirmation(t *testing.T) {
	private := t.TempDir()
	if err := os.Chmod(private, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPER3D_API_KEY", "dummy-not-a-key")
	err := run([]string{"official", "-version", "hyper3d/api-v2", "-key-env", "HYPER3D_API_KEY", "-private", private,
		"-steps", "rodin-labeled,rodin-labeled-status,rodin-labeled-download"})
	if err == nil || !strings.Contains(err.Error(), "sends 1 paid request(s)") || !strings.Contains(err.Error(), "-confirm-paid") {
		t.Fatalf("an unconfirmed official run must refuse and count its paid request, got %v", err)
	}
	if entries, readErr := os.ReadDir(private); readErr != nil || len(entries) != 0 {
		t.Fatalf("a refused run leaves nothing behind: %v %v", entries, readErr)
	}
}

// The steps of the 1.9.0 real round (#539) are paid submissions with a free status
// and download behind them. Without -confirm-paid each refuses before it reads a key
// or sends anything, counting the one paid request, and leaves nothing behind.
func TestOfficialRefusesTheRealRoundRodinStepsWithoutConfirmation(t *testing.T) {
	for _, name := range []string{"reject-seed-probe", "rodin-text-quadpose", "rodin-image-label-short", "bang-upload", "texture-images"} {
		t.Run(name, func(t *testing.T) {
			private := t.TempDir()
			if err := os.Chmod(private, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HYPER3D_API_KEY", "dummy-not-a-key")
			err := run([]string{"official", "-version", "hyper3d/api-v2", "-key-env", "HYPER3D_API_KEY", "-private", private,
				"-steps", name + "," + name + "-status," + name + "-download"})
			if err == nil || !strings.Contains(err.Error(), "sends 1 paid request(s)") || !strings.Contains(err.Error(), "-confirm-paid") {
				t.Fatalf("an unconfirmed official run must refuse and count its paid request, got %v", err)
			}
			if entries, readErr := os.ReadDir(private); readErr != nil || len(entries) != 0 {
				t.Fatalf("a refused run leaves nothing behind: %v %v", entries, readErr)
			}
		})
	}
}

// #540: the acceptance-round steps the image profiles gained are costlier
// than the routine refresh, so an official run skips them unless -steps names
// them. The default paid list (what -confirm-paid has to acknowledge) leaves
// them out, naming one brings it in, and the fake and compare commands keep
// the whole script.
func TestOfficialRunSkipsOptInStepsUnlessNamed(t *testing.T) {
	optIn := map[string][]string{
		"openai/images-v1": {"generations-n2-1536x1152", "generations-quality-max", "generations-invalid-size", "edits-two-images-mask"},
		"gemini/images-v1": {"generate-16x9-4k", "edit-3x4-two-references", "generate-invalid-ratio"},
		"jimeng/api-v3":    {"generations-4096", "edits-two-references-4096", "generations-sequential-max2", "generations-invalid-size"},
	}
	for version, names := range optIn {
		profile := registry.Profiles()[version]
		for _, name := range names {
			found := false
			for _, step := range profile.Steps {
				if step.Name == name {
					found = step.OptIn
				}
			}
			if !found {
				t.Errorf("%s step %q must be marked OptIn", version, name)
			}
		}
		private := t.TempDir()
		if err := os.Chmod(private, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ALIGN_DUMMY_KEY", "dummy-not-a-key")
		base := []string{"official", "-version", version, "-key-env", "ALIGN_DUMMY_KEY", "-private", private}
		err := run(base)
		if err == nil || !strings.Contains(err.Error(), "-confirm-paid") {
			t.Fatalf("%s: an unconfirmed default run must refuse, got %v", version, err)
		}
		for _, name := range names {
			if strings.Contains(err.Error(), name) {
				t.Errorf("%s: the default paid list must not contain the opt-in step %q: %v", version, name, err)
			}
		}
		err = run(append(base, "-steps", strings.Join(names, ",")))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("sends %d paid request(s)", len(names))) {
			t.Fatalf("%s: naming the opt-in steps must select exactly them, got %v", version, err)
		}
		for _, name := range names {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s: -steps naming %q must bring it in: %v", version, name, err)
			}
		}
		if all := selectSteps("fake", profile.Steps, ""); len(all) != len(profile.Steps) {
			t.Errorf("%s: the fake command must keep every step, got %d of %d", version, len(all), len(profile.Steps))
		}
		if all := selectSteps("compare", profile.Steps, ""); len(all) != len(profile.Steps) {
			t.Errorf("%s: the compare command must keep every step", version)
		}
	}
}

func TestDraftCommandRequiresPrivateDirectories(t *testing.T) {
	if err := run([]string{"draft"}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("draft without flags must fail, got %v", err)
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"draft", "-records", open, "-out", open, "-name", "x", "-worktree", worktreeRoot(t)})
	if err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("a non-0700 records directory must be refused, got %v", err)
	}
	empty := t.TempDir()
	if err := os.Chmod(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"draft", "-records", empty, "-out", empty, "-name", "x", "-worktree", worktreeRoot(t)}); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("no records must not produce a draft, got %v", err)
	}
}

type captureUpstream struct{}

func (captureUpstream) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
	}
	body := `{"code":0,"data":{"task_id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"queued"}}`
	if request.Method == http.MethodGet {
		body = `{"code":0,"data":{"task_id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"success","progress":100}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
}

// #233: the application's own recorder (UPSTREAM_CAPTURE_DIR) writes the
// records `draft` reads. A directory it produced converts, unchanged, into the
// candidate/policy/meta files of a committed sample, and the draft passes the
// same loader, Validate and privacy scan the committed corpus does.
func TestDraftCommandReadsTheApplicationsRecords(t *testing.T) {
	records := filepath.Join(t.TempDir(), "capture")
	recorder, err := wirecapture.NewRuntimeRecorder(wirecapture.RuntimeConfig{
		Dir: records, MaxRecords: 100, MaxBytes: 8 << 20,
		Defaults: wirecapture.Metadata{Environment: "production", AppRevision: "0123456789abcdef0123456789abcdef01234567", CatalogRevision: "2026-11-13.299.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: recorder.Wrap("tripo", captureUpstream{})}
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/v2/openapi/task", `{"type":"text_to_model","prompt":"a secret castle for alice@example.org","model_version":"v3.1"}`},
		{http.MethodGet, "/v2/openapi/task/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", ""},
	} {
		request, _ := http.NewRequest(call.method, "http://tripo.internal.example:8080"+call.path, strings.NewReader(call.body))
		request.Header.Set("Authorization", "Bearer sk-live-0123456789abcdefABCDEF")
		if call.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	if recorder.Written() != 2 {
		t.Fatalf("recorded %d exchanges, want 2 (%v)", recorder.Written(), recorder.WriteError())
	}
	out := t.TempDir()
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"draft", "-records", records, "-out", out, "-name", "captured-tripo", "-connection", "tripo",
		"-offering", "tripo-text-to-3d", "-worktree", worktreeRoot(t)}); err != nil {
		t.Fatalf("draft over the application's records: %v", err)
	}
	var candidate []wirecapture.Exchange
	var policy wirecapture.Policy
	for name, target := range map[string]any{"captured-tripo.candidate.json": &candidate, "captured-tripo.policy.json": &policy} {
		raw, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(filepath.Join(out, name)); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s must be 0600", name)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			t.Fatalf("%s is not in the committed sample format: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "captured-tripo.meta.json")); err != nil {
		t.Fatal(err)
	}
	for i := range candidate {
		for _, body := range []*json.RawMessage{&candidate[i].Request.Body.JSON, &candidate[i].Response.Body.JSON} {
			var compact bytes.Buffer
			if len(*body) > 0 && json.Compact(&compact, *body) == nil {
				*body = compact.Bytes()
			}
		}
	}
	if err := wirecapture.Validate(candidate, policy); err != nil || len(candidate) != 2 {
		t.Fatalf("the draft is not a valid sample: %d exchanges, %v", len(candidate), err)
	}
	if findings := privacyscan.ScanExchanges("captured-tripo.candidate.json", candidate, policy); len(findings) != 0 {
		t.Fatalf("privacy scan of the draft: %v", findings)
	}
	// The draft replays: the first recorded request is the first replayed one.
	replay, err := wirefixture.NewReplay(candidate, policy)
	if err != nil {
		t.Fatalf("the draft must load as a replay: %v", err)
	}
	first := candidate[0]
	request, _ := http.NewRequest(first.Request.Method, "https://"+first.Request.Host+first.Request.Path, bytes.NewReader(first.Request.Body.JSON))
	request.Header.Set("Content-Type", first.Request.ContentType)
	for name, value := range first.Request.Headers {
		request.Header.Set(name, value)
	}
	if _, err := replay.RoundTrip(request); err != nil {
		t.Fatalf("replay of the first draft exchange: %v", err)
	}
	for _, name := range []string{"captured-tripo.candidate.json", "captured-tripo.policy.json", "captured-tripo.meta.json"} {
		raw, _ := os.ReadFile(filepath.Join(out, name))
		for _, secret := range []string{"alice", "example.org", "sk-live", "tripo.internal", "castle", "Bearer", "8080"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s holds %q", name, secret)
			}
		}
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An "official" upstream that counts its requests, so a refused flag can be shown
// never to have reached it.
func countingUpstream(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// -artifacts holds real models the paid API delivered: it must be an absolute path
// to an existing 0700 directory, not a link, outside the worktree (#386).
func TestArtifactsDirMustBePrivateAndOutsideTheWorktree(t *testing.T) {
	worktree := privateTempDir(t)
	good := privateTempDir(t)
	inside := filepath.Join(worktree, "arts")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	// A linked ancestor hides a directory that is inside the worktree.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(worktree, alias); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ dir, want string }{
		"relative":                   {"arts", "absolute"},
		"missing":                    {filepath.Join(t.TempDir(), "nope"), "existing directory"},
		"a file":                     {file, "existing directory"},
		"a symbolic link":            {link, "symbolic link"},
		"a directory open to others": {open, "0700"},
		"inside the worktree":        {inside, "outside the worktree"},
		"the worktree itself":        {worktree, "outside the worktree"},
		"inside it through a link":   {filepath.Join(alias, "arts"), "outside the worktree"},
	} {
		if _, err := artifactsDir(tc.dir, worktree); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	resolved, err := artifactsDir(good, worktree)
	if want, _ := filepath.EvalSymlinks(good); err != nil || resolved != want {
		t.Fatalf("a good directory: %q, %v", resolved, err)
	}
}

// The flag is checked before a single request is sent, and only the official
// command takes it.
func TestArtifactsFlagIsCheckedBeforeAnyRequest(t *testing.T) {
	t.Setenv("FAKE_ALIGN_KEY", "k")
	upstream, hits := countingUpstream(t)
	private, worktree := privateTempDir(t), privateTempDir(t)
	good := privateTempDir(t)
	inside := filepath.Join(worktree, "arts")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	args := func(command, dir string, more ...string) []string {
		return append([]string{command, "-version", "tripo/v3", "-key-env", "FAKE_ALIGN_KEY", "-base-url", upstream.URL,
			"-private", private, "-worktree", worktree, "-artifacts", dir, "-steps", "multiview-to-model,convert-3mf"}, more...)
	}
	// Confirmed and refused: the directory is checked first.
	if err := run(args("official", inside, "-confirm-paid")); err == nil || !strings.Contains(err.Error(), "outside the worktree") {
		t.Errorf("a directory inside the worktree: err = %v", err)
	}
	if err := run(args("fake", good, "-confirm-paid")); err == nil || !strings.Contains(err.Error(), "official command only") {
		t.Errorf("another command: err = %v", err)
	}
	// A good directory is accepted: the run then stops at the paid-request check.
	if err := run(args("official", good)); err == nil || !strings.Contains(err.Error(), "2 paid request(s) (multiview-to-model, convert-3mf)") {
		t.Errorf("a good directory: err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d request(s) were sent", hits.Load())
	}
}

// An official run with -artifacts leaves the bytes of what it fetched in the
// directory and their digests in the private bundle; what it fetched but could not
// keep ends the run with an error after the bundle is saved.
func TestOfficialRunKeepsArtifactsAndRefusesToCallItDoneWithoutThem(t *testing.T) {
	t.Setenv("FAKE_ALIGN_KEY", "k")
	t.Setenv(wirefixture.EnvUpstreamRecordDir, "")
	t.Setenv(wirefixture.EnvUpstreamRecordAck, "")
	model := append([]byte("glTF\x02\x00\x00\x00"), bytes.Repeat([]byte{5}, 1500)...)
	refused := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/task":
			if refused {
				_, _ = fmt.Fprintf(w, `{"data":{"model":%q,"gone":%q}}`, server.URL+"/m.glb", server.URL+"/gone.png")
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":{"model":%q}}`, server.URL+"/m.glb")
		case "/m.glb":
			_, _ = w.Write(model)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	profile := align.Profile{Provider: "t", API: "v1", Official: "https://official.example",
		Steps: []align.Step{{Name: "task", Method: "GET", Path: "/task", Fetch: []string{"data.*"}}}}
	private, arts := privateTempDir(t), privateTempDir(t)

	if err := runOfficial(profile, private, "kept", "FAKE_ALIGN_KEY", server.URL, worktreeRoot(t), true, nil, arts); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(arts, "task-1.glb"))
	if err != nil || !bytes.Equal(got, model) {
		t.Fatalf("the model was not kept: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(arts, "task-1.glb")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	bundle, err := align.LoadBundle(filepath.Join(private, "kept.official.bundle.json"))
	if err != nil || len(bundle.Obs[0].Artifacts) != 1 || bundle.Obs[0].Artifacts[0].File != "task-1.glb" || len(bundle.Obs[0].Artifacts[0].SHA256) != 64 {
		t.Fatalf("bundle: %+v, %v", bundle, err)
	}

	refused = true
	err = runOfficial(profile, private, "unsaved", "FAKE_ALIGN_KEY", server.URL, worktreeRoot(t), true, nil, arts)
	if err == nil || !strings.Contains(err.Error(), "1 fetched artifact(s) were not saved") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(private, "unsaved.official.bundle.json")); statErr != nil {
		t.Errorf("the bundle must be saved before the run reports the unsaved artifact: %v", statErr)
	}

	// Without -artifacts the same run keeps nothing and does not complain about it.
	if err := runOfficial(profile, private, "observed", "FAKE_ALIGN_KEY", server.URL, worktreeRoot(t), true, nil, ""); err != nil {
		t.Fatal(err)
	}
	if names, _ := os.ReadDir(arts); len(names) != 2 { // the first run's model, and the second run's after its refused picture
		t.Errorf("directory holds %d files, want 2", len(names))
	}
}

func TestReportArtifactsListsWhatWasKeptAndCountsTheRest(t *testing.T) {
	bundle := &align.Bundle{Obs: []align.Obs{
		{Step: "poll", Artifacts: []align.ArtifactObs{
			{Field: "data.model", Format: "glb", Size: 12, File: "poll-1.glb", SHA256: "ab"},
			{Field: "data.mask", Error: "status 403: not saved"},
		}},
		{Step: "none"},
	}}
	var out, errOut bytes.Buffer
	if unsaved := reportArtifacts(&out, &errOut, bundle); unsaved != 1 {
		t.Fatalf("unsaved = %d", unsaved)
	}
	if !strings.Contains(out.String(), "artifact poll-1.glb  glb  12 bytes  sha256:ab  (poll data.model)") ||
		!strings.Contains(errOut.String(), "artifact not saved: poll data.mask: status 403: not saved") {
		t.Fatalf("out %q, err %q", out.String(), errOut.String())
	}
}
