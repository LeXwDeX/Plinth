package align

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// artifactServer serves a finished task whose output names four artifacts: a GLB,
// a 3MF (a ZIP package, behind a signed-looking URL), a picture the CDN refuses
// (403) and a body that is cut short.
type artifactServer struct {
	*httptest.Server
	model, pack []byte
}

func newArtifactServer(t *testing.T) *artifactServer {
	t.Helper()
	s := &artifactServer{
		model: append([]byte("glTF\x02\x00\x00\x00"), bytes.Repeat([]byte{7}, 4096)...),
		pack:  append([]byte("PK\x03\x04"), bytes.Repeat([]byte{9}, 2048)...),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /task", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":{"status":"done","out":{"model":%q,"pack":%q,"gone":%q,"cut":%q}}}`,
			s.URL+"/a/model.glb", s.URL+"/a/pack.3mf?Policy=signed-secret", s.URL+"/a/gone.png", s.URL+"/a/cut.glb")
	})
	mux.HandleFunc("GET /a/model.glb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "binary/octet-stream")
		_, _ = w.Write(s.model)
	})
	mux.HandleFunc("GET /a/pack.3mf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "binary/octet-stream")
		_, _ = w.Write(s.pack)
	})
	mux.HandleFunc("GET /a/gone.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	})
	mux.HandleFunc("GET /a/cut.glb", func(w http.ResponseWriter, r *http.Request) {
		// Fewer bytes than declared: the server drops the connection, the client
		// reads an unexpected EOF.
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("glTF\x02\x00\x00\x00"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func fetchStep() Step {
	return Step{Name: "poll", Method: "GET", Path: "/task", Fetch: []string{"data.out.*"},
		Poll: &Poll{StatusPath: "data.status", Terminal: []string{"done"}, Max: 2}}
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func artifactsByField(obs Obs) map[string]ArtifactObs {
	byField := map[string]ArtifactObs{}
	for _, artifact := range obs.Artifacts {
		byField[artifact.Field] = artifact
	}
	return byField
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// An ArtifactDir keeps the bytes of every artifact it can fetch, as new 0600 files
// named by step and position (fields in name order), with the digest in the
// observation. What it cannot keep is recorded, leaves no partial file, and does
// not stop the run: a 403 or a body cut short is an observation of the upstream.
func TestRunKeepsTheBytesOfFetchedArtifacts(t *testing.T) {
	server := newArtifactServer(t)
	dir := privateDir(t)
	bundle, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", NoPollDelay: true, ArtifactDir: dir},
		[]Step{fetchStep()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := artifactsByField(bundle.Obs[0])
	// cut, gone, model, pack in field order: positions 1 to 4.
	model, pack := artifacts["data.out.model"], artifacts["data.out.pack"]
	if model.File != "poll-3.glb" || pack.File != "poll-4.3mf" {
		t.Fatalf("files: model %q, pack %q (%+v)", model.File, pack.File, artifacts)
	}
	for _, tc := range []struct {
		artifact ArtifactObs
		want     []byte
		format   string
	}{{model, server.model, "glb"}, {pack, server.pack, "zip"}} {
		path := filepath.Join(dir, tc.artifact.File)
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("%s: bytes differ or unreadable: %v", tc.artifact.File, readErr)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, want 0600", tc.artifact.File, info.Mode().Perm())
		}
		sum := sha256.Sum256(tc.want)
		if tc.artifact.SHA256 != hex.EncodeToString(sum[:]) || tc.artifact.Size != int64(len(tc.want)) ||
			tc.artifact.Format != tc.format || tc.artifact.Status != http.StatusOK || tc.artifact.Error != "" {
			t.Errorf("%s: observation %+v", tc.artifact.File, tc.artifact)
		}
	}
	gone := artifacts["data.out.gone"]
	if gone.File != "" || gone.SHA256 != "" || gone.Status != http.StatusForbidden || gone.Error != "status 403: not saved" {
		t.Errorf("the refused picture: %+v", gone)
	}
	cut := artifacts["data.out.cut"]
	if cut.File != "" || !strings.HasPrefix(cut.Error, "read body: ") {
		t.Errorf("the body cut short: %+v", cut)
	}
	// Only the two kept files are there: no partial file is left behind.
	if got := dirNames(t, dir); strings.Join(got, ",") != "poll-3.glb,poll-4.3mf" {
		t.Errorf("directory holds %v", got)
	}
}

// What a failed fetch records does not carry the artifact URL, which is a signed
// link: neither the observation nor the console may show it.
func TestFetchErrorsDoNotCarryTheSignedURL(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	signed := closed.URL + "/a/model.glb?Policy=signed-secret"
	closed.Close()
	artifact, err := fetch(context.Background(), &http.Client{}, "data.out.model", signed, &artifactSave{Dir: privateDir(t), Step: "poll", N: 1})
	if err != nil || artifact.File != "" || !strings.HasPrefix(artifact.Error, "fetch: ") || strings.Contains(artifact.Error, "signed-secret") {
		t.Fatalf("observation %+v, err %v", artifact, err)
	}
	wrapped := &url.Error{Op: "Get", URL: signed, Err: errors.New("connection refused")}
	if got := withoutURL(fmt.Errorf("outer: %w", wrapped)); got != "connection refused" {
		t.Errorf("withoutURL = %q", got)
	}
	if got := withoutURL(errors.New("plain")); got != "plain" {
		t.Errorf("withoutURL = %q", got)
	}
}

// Without an ArtifactDir an artifact is observed and nothing else: no file, no
// digest, no error text, and the same size and format as before.
func TestRunWithoutAnArtifactDirOnlyObserves(t *testing.T) {
	server := newArtifactServer(t)
	bundle, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", NoPollDelay: true},
		[]Step{fetchStep()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := artifactsByField(bundle.Obs[0])
	model := artifacts["data.out.model"]
	if model.Format != "glb" || model.Size != int64(len(server.model)) || model.Status != http.StatusOK {
		t.Errorf("model observation: %+v", model)
	}
	if artifacts["data.out.pack"].Format != "zip" || artifacts["data.out.gone"].Status != http.StatusForbidden {
		t.Errorf("observations: %+v", artifacts)
	}
	for field, artifact := range artifacts {
		if artifact.File != "" || artifact.SHA256 != "" || artifact.Error != "" {
			t.Errorf("%s: kept or explained something without an ArtifactDir: %+v", field, artifact)
		}
	}
}

// An earlier file of the same name (a second run into one directory) is never
// replaced: the new bytes get the next free name.
func TestRunNeverReplacesAnEarlierArtifactFile(t *testing.T) {
	server := newArtifactServer(t)
	dir := privateDir(t)
	earlier := filepath.Join(dir, "poll-3.glb")
	if err := os.WriteFile(earlier, []byte("earlier bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", NoPollDelay: true, ArtifactDir: dir},
		[]Step{fetchStep()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(earlier); string(got) != "earlier bytes" {
		t.Fatalf("the earlier file was replaced: %q", got)
	}
	if file := artifactsByField(bundle.Obs[0])["data.out.model"].File; file != "poll-3-2.glb" {
		t.Fatalf("the new bytes went to %q", file)
	}
}

// A file that cannot be written locally ends the step, and with it the run before
// any later (paid) step, after every artifact of the step was tried and recorded.
func TestRunStopsWhenAnArtifactCannotBeWrittenLocally(t *testing.T) {
	server := newArtifactServer(t)
	missing := filepath.Join(t.TempDir(), "missing")
	bundle, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", NoPollDelay: true, ArtifactDir: missing},
		[]Step{fetchStep(), {Name: "after", Method: "GET", Path: "/task", Paid: true}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "keep artifact") {
		t.Fatalf("err = %v", err)
	}
	if len(bundle.Obs) != 1 || bundle.PaidRequests != 0 {
		t.Fatalf("the paid step after it ran: %d obs, %d paid", len(bundle.Obs), bundle.PaidRequests)
	}
	artifacts := artifactsByField(bundle.Obs[0])
	for _, field := range []string{"data.out.model", "data.out.pack"} {
		if artifacts[field].File != "" || !strings.HasPrefix(artifacts[field].Error, "create file: ") {
			t.Errorf("%s: %+v", field, artifacts[field])
		}
	}
	if len(artifacts) != 4 {
		t.Errorf("every artifact of the step must be recorded: %+v", artifacts)
	}
}

func TestArtifactExtensionFollowsTheBytesAndKnowsContainers(t *testing.T) {
	for name, tc := range map[string]struct{ format, url, want string }{
		"glb wins over the URL":      {"glb", "https://cdn.test/a/model.3mf", "glb"},
		"jpeg":                       {"jpeg", "https://cdn.test/a/view.jpg?x=1", "jpg"},
		"fbx":                        {"fbx-binary", "https://cdn.test/a/model.fbx", "fbx"},
		"binary stl":                 {"stl-binary", "https://cdn.test/a/model", "stl"},
		"a ZIP named 3mf":            {"zip", "https://cdn.test/a/model.3MF?Policy=x", "3mf"},
		"a ZIP named usdz":           {"zip", "https://cdn.test/a/model.usdz", "usdz"},
		"a ZIP with no known name":   {"zip", "https://cdn.test/a/model", "zip"},
		"a ZIP with a hostile name":  {"zip", "https://cdn.test/a/model.../../x", "zip"},
		"text named obj":             {"text", "https://cdn.test/a/model.obj", "obj"},
		"text with an odd extension": {"text", "https://cdn.test/a/model.sh", "txt"},
		"binary named splat":         {"binary", "https://cdn.test/a/model.splat", "splat"},
		"binary with no name":        {"binary", "https://cdn.test/a/model", "bin"},
		"an unparsable URL":          {"binary", "://bad url", "bin"},
	} {
		if got := artifactExtension(tc.format, tc.url); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := fileSafe("a/b c..d-e_f"); got != "a_b_c..d-e_f" {
		t.Errorf("fileSafe = %q", got)
	}
}

// A file is named from its first bytes and its length, so a download is never held
// in memory: the binary STL check needs the length, which the head alone lacks.
func TestDetectFormatNeedsOnlyTheHeadAndTheLength(t *testing.T) {
	stl := append(make([]byte, 80), 20, 0, 0, 0)
	stl = append(stl, make([]byte, 20*50)...)
	text := bytes.Repeat([]byte("v 0 0 0\n"), 200)
	for name, data := range map[string][]byte{
		"a binary STL over the head": stl, "a long text": text, "a long GLB": append([]byte("glTF"), make([]byte, 5000)...),
		"a long binary": append([]byte{1, 0, 2}, make([]byte, 5000)...),
	} {
		whole := DetectFormat(data)
		writer := &headWriter{}
		for rest := data; len(rest) > 0; { // in odd pieces, as a network delivers it
			n := min(len(rest), 333)
			_, _ = writer.Write(rest[:n])
			rest = rest[n:]
		}
		if got := detectFormat(writer.head, writer.total); got != whole || len(writer.head) > formatHead {
			t.Errorf("%s: streamed %q (head %d bytes), whole %q", name, got, len(writer.head), whole)
		}
	}
	if got := DetectFormat(stl); got != "stl-binary" {
		t.Errorf("a binary STL over the head is %q", got)
	}
	if got := DetectFormat(nil); got != "binary" {
		t.Errorf("no bytes are %q", got)
	}
}
