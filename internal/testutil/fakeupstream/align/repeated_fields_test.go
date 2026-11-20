package align

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// sentPart is one multipart part as the upstream saw it. A file part has no
// value here: the bytes are not compared.
type sentPart struct {
	Name, File, Value string
}

// labeledStep is a multi-view request: three pictures and one image_label per
// picture, each sent as its own part of the same name, in picture order.
func labeledStep() Step {
	picture := func(name string, size int) File {
		return File{Field: "images", Filename: name, MediaType: "image/png", Data: ReferencePNG(size)}
	}
	return Step{
		Name: "labeled", Method: "POST", Path: "/rodin", Paid: true,
		Form: []Field{
			{Name: "tier", Value: "Gen-2.5-Extreme-Low"},
			{Name: "image_label", Value: "F"}, {Name: "image_label", Value: "L"}, {Name: "image_label", Value: "B"},
		},
		Files: []File{picture("front.png", 32), picture("left.png", 40), picture("back.png", 48)},
	}
}

// labeledParts are the parts labeledStep puts on the wire: the text fields in
// script order, then the files.
var labeledParts = []sentPart{
	{Name: "tier", Value: "Gen-2.5-Extreme-Low"},
	{Name: "image_label", Value: "F"}, {Name: "image_label", Value: "L"}, {Name: "image_label", Value: "B"},
	{Name: "images", File: "front.png"}, {Name: "images", File: "left.png"}, {Name: "images", File: "back.png"},
}

// readParts lists the parts of a multipart request in the order they arrived.
func readParts(t *testing.T, request *http.Request) []sentPart {
	t.Helper()
	reader, err := request.MultipartReader()
	if err != nil {
		t.Errorf("multipart request: %v", err)
		return nil
	}
	var parts []sentPart
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Errorf("next part: %v", err)
			return parts
		}
		data, _ := io.ReadAll(part)
		sent := sentPart{Name: part.FormName(), File: part.FileName()}
		if sent.File == "" {
			sent.Value = string(data)
		}
		parts = append(parts, sent)
	}
}

// A form field sent more than once reaches the upstream as one part per value,
// in the order of the script, ahead of the files; the bundle's record of the
// request keeps every value of the repeated name in that order. A name sent
// once keeps its plain string, so the record of every single-valued step is
// the one it always was.
func TestSendRecordsEveryValueOfARepeatedFormField(t *testing.T) {
	seen := make(chan []sentPart, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- readParts(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	run := func(step Step) *Bundle {
		t.Helper()
		bundle, err := Run(context.Background(), "p", "v1", Target{Name: "t", BaseURL: server.URL, Key: "k"}, []Step{step}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}

	labeled := run(labeledStep())
	if labeled.PaidRequests != 1 {
		t.Fatalf("paid = %d, want the one labeled submission", labeled.PaidRequests)
	}
	got := <-seen
	if len(got) != len(labeledParts) {
		t.Fatalf("upstream saw %+v, want %+v", got, labeledParts)
	}
	for i, want := range labeledParts {
		if got[i] != want {
			t.Errorf("part %d = %+v, want %+v", i, got[i], want)
		}
	}
	var record map[string]any
	if err := json.Unmarshal(labeled.Obs[0].Request, &record); err != nil {
		t.Fatalf("request record %s: %v", labeled.Obs[0].Request, err)
	}
	if record["tier"] != "Gen-2.5-Extreme-Low" {
		t.Errorf("a field sent once is a plain string, got %#v", record["tier"])
	}
	for name, want := range map[string][]any{"image_label": {"F", "L", "B"}, "images": {"", "", ""}} {
		got, _ := record[name].([]any)
		if len(got) != len(want) {
			t.Fatalf("%s = %#v, want %v", name, record[name], want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %#v, want %#v", name, i, got[i], want[i])
			}
		}
	}

	single := run(Step{Name: "single", Method: "POST", Path: "/rodin", Form: []Field{{Name: "tier", Value: "t"}, {Name: "prompt", Value: "p"}},
		Files: []File{{Field: "model", Filename: "model.glb", MediaType: "model/gltf-binary", Data: []byte("glTF")}}})
	<-seen
	if got := string(single.Obs[0].Request); got != `{"model":"","prompt":"p","tier":"t"}` {
		t.Fatalf("the record of a step without repeated names changed: %s", got)
	}
}

// The export registers every value of a repeated field. Before, only the last
// value of a name reached the policy, so a request with image_label F, L and B
// failed with "string not approved" after the paid run had been spent. The
// sample keeps the parts, their order and their duplicate names.
func TestExportApprovesEveryValueOfARepeatedFormField(t *testing.T) {
	const base = "https://api.hyper3d.com/api/v2"
	upstream := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_ = readParts(t, request)
		body := `{"status":"queued"}`
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
	})
	steps := []Step{labeledStep()}
	tee := NewTee(upstream)
	if _, err := Run(context.Background(), "p", "v1", Target{Name: "official", BaseURL: base, Key: "k",
		Client: &http.Client{Transport: tee}}, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "raw.json")
	if err := tee.Save(saved); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(saved)
	if err != nil {
		t.Fatal(err)
	}

	// The export replays the script offline against the saved exchanges.
	worktree, out := t.TempDir(), t.TempDir()
	evidence := filepath.Join(t.TempDir(), "evidence")
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	capture, err := NewCapture(CaptureConfig{
		Connection: "hyper3d", Scenario: "repeated-form-fields", APIVersion: "v2", Environment: "isolated-wire-capture",
		AppRevision: strings.Repeat("a", 40), CatalogRevision: strings.Repeat("b", 64),
		PublicHosts: []string{"api.hyper3d.com"},
	}, worktree, evidence)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	again, err := Run(context.Background(), "p", "v1", Target{Name: "official-replay", BaseURL: base, Key: "replay",
		Client: &http.Client{Transport: capture.Transport(replay)}, SkipFetch: true, NoPollDelay: true}, steps, nil, nil)
	if err != nil {
		t.Fatalf("offline replay: %v", err)
	}
	candidate, err := capture.Export(worktree, out, "repeated", "", again)
	if err != nil {
		t.Fatalf("export of a request with repeated image_label parts: %v", err)
	}
	raw, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil || len(exchanges) != 1 {
		t.Fatalf("candidate: %d exchanges, %v", len(exchanges), err)
	}
	body := exchanges[0].Request.Body.Multipart
	if body == nil || len(body.Parts) != len(labeledParts) {
		t.Fatalf("exported request body: %+v", exchanges[0].Request.Body)
	}
	for i, want := range labeledParts {
		got := body.Parts[i]
		if got.Name != want.Name || got.Value != want.Value || (got.Media != nil) != (want.File != "") {
			t.Errorf("exported part %d = %+v, want %+v", i, got, want)
		}
	}
}
