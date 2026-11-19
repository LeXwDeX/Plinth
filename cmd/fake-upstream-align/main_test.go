package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
