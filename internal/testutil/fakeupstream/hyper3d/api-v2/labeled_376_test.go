package hyper3dapiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// #376: the labeled multi-view step of the alignment script. Rodin Gen-2.5 takes
// image_label as an array that matches images by position, and the docs do not
// say how a multipart request encodes it. The step sends one part per label, in
// picture order, as `images` is sent; the two approved real runs of 2026-11-19
// confirmed that (evidence_376_test.go). These tests pin what the step puts on
// the wire and what it can spend.

func profileSteps(t *testing.T, names ...string) []align.Step {
	t.Helper()
	byName := map[string]align.Step{}
	for _, step := range Profile().Steps {
		byName[step.Name] = step
	}
	var steps []align.Step
	for _, name := range names {
		step, ok := byName[name]
		if !ok {
			t.Fatalf("the profile has no step %q", name)
		}
		steps = append(steps, step)
	}
	return steps
}

// The profile's paid steps are listed here, so a new one is added knowingly: the
// labeled request is one of them, and only its submission is.
func TestPaidStepsOfTheProfile(t *testing.T) {
	var paid []string
	for _, step := range Profile().Steps {
		if step.Paid {
			paid = append(paid, step.Name)
		}
	}
	want := []string{"rodin-text", "rodin-image", "bang", "texture", "texture-failed", "rodin-labeled", "rodin-label-overflow"}
	if !reflect.DeepEqual(paid, want) {
		t.Fatalf("paid steps = %v, want %v", paid, want)
	}
}

func TestLabeledRodinStepIsTheCheapestThreeViewRequest(t *testing.T) {
	steps := profileSteps(t, "rodin-labeled", "rodin-labeled-status", "rodin-labeled-download")
	submit := steps[0]
	if submit.Method != "POST" || submit.Path != "/rodin" || !submit.Paid || submit.RequireVar != "" {
		t.Fatalf("submission = %+v", submit)
	}
	for _, follow := range steps[1:] {
		if follow.Paid {
			t.Errorf("%s must not be a paid step", follow.Name)
		}
	}
	if steps[1].Path != "/status" || steps[2].Path != "/download" ||
		steps[1].RequireVar != "rodin-labeled_key" || steps[2].RequireVar != "rodin-labeled_uuid" {
		t.Errorf("follow-up steps = %+v, %+v", steps[1], steps[2])
	}

	// The cheapest Gen-2.5 request (the Catalog's own tier is Extreme-High, a
	// credit more), then one image_label per picture in picture order.
	want := []align.Field{
		{Name: "tier", Value: "Gen-2.5-Extreme-Low"}, {Name: "geometry_file_format", Value: "glb"},
		{Name: "material", Value: "PBR"}, {Name: "mesh_mode", Value: "Raw"}, {Name: "quality", Value: "medium"},
		{Name: "texture_mode", Value: "extreme-low"},
		{Name: "image_label", Value: "F"}, {Name: "image_label", Value: "L"}, {Name: "image_label", Value: "B"},
	}
	if !reflect.DeepEqual(submit.Form, want) {
		t.Fatalf("form = %+v\nwant %+v", submit.Form, want)
	}

	if len(submit.Files) != 3 || submit.Multipart != nil {
		t.Fatalf("files = %d, want three pictures", len(submit.Files))
	}
	seen := map[string]bool{}
	for i, file := range submit.Files {
		if file.Field != "images" || file.MediaType != "image/png" || file.FromVar != "" {
			t.Errorf("picture %d = %s %s from %q", i, file.Field, file.MediaType, file.FromVar)
		}
		config, err := png.DecodeConfig(bytes.NewReader(file.Data))
		if err != nil || config.Width != 512 || config.Height != 512 {
			t.Errorf("picture %d (%s) is not a 512 x 512 PNG: %v", i, file.Filename, err)
		}
		if seen[string(file.Data)] {
			t.Errorf("picture %d (%s) repeats an earlier picture", i, file.Filename)
		}
		seen[string(file.Data)] = true
	}
	for i, name := range []string{"front.png", "left.png", "back.png"} {
		if submit.Files[i].Filename != name {
			t.Errorf("picture %d is %q, want %q", i, submit.Files[i].Filename, name)
		}
	}
}

// partsRecorder remembers the parts of every multipart request, then sends it on.
type partsRecorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	sent map[string][]wirePart
}

// wirePart is a multipart part as the upstream receives it; a file's bytes are
// not kept, only whether they are a PNG.
type wirePart struct {
	Name, File, MediaType, Value string
	PNG                          bool
}

func (r *partsRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return r.base.RoundTrip(request)
	}
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return nil, err
	}
	var parts []wirePart
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		wire := wirePart{Name: part.FormName(), File: part.FileName(), MediaType: part.Header.Get("Content-Type")}
		if wire.File == "" {
			wire.Value = string(data)
		} else {
			_, decodeErr := png.DecodeConfig(bytes.NewReader(data))
			wire.PNG = decodeErr == nil
		}
		parts = append(parts, wire)
	}
	r.mu.Lock()
	r.sent[request.URL.Path] = parts
	r.mu.Unlock()
	forwarded := request.Clone(request.Context())
	forwarded.Body = io.NopCloser(bytes.NewReader(raw))
	return r.base.RoundTrip(forwarded)
}

// The three steps run against the fake: the submission is accepted (three labels
// for three pictures is not IMAGE_LABEL_LENGTH_TOO_LONG), the task finishes and
// the files download. The upstream receives the text parts in script order, the
// label parts F, L, B among them, then the three PNG pictures.
func TestLabeledRodinStepsRunAgainstTheFake(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	recorder := &partsRecorder{base: http.DefaultTransport, sent: map[string][]wirePart{}}
	bundle, err := align.Run(context.Background(), "hyper3d", "api-v2", align.Target{
		Name: "fake", BaseURL: f.Server.URL + Prefix, Key: key, NoPollDelay: true,
		Client: &http.Client{Transport: recorder},
	}, profileSteps(t, "rodin-labeled", "rodin-labeled-status", "rodin-labeled-download"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.PaidRequests != 1 {
		t.Fatalf("paid requests = %d, want the submission alone", bundle.PaidRequests)
	}
	submit, status, download := bundle.Obs[0], bundle.Obs[1], bundle.Obs[2]
	var accepted map[string]any
	if err := json.Unmarshal(submit.Body, &accepted); err != nil || submit.Status != http.StatusCreated ||
		accepted["error"] != nil || accepted["uuid"] == nil {
		t.Fatalf("submission = %d %s (%v)", submit.Status, submit.Body, err)
	}
	if status.Skipped != "" || len(status.Polls) == 0 || status.Polls[len(status.Polls)-1].Status != "Done" {
		t.Fatalf("status = %+v", status)
	}
	if download.Skipped != "" || len(download.Artifacts) == 0 {
		t.Fatalf("download = %+v", download)
	}

	want := []wirePart{
		{Name: "tier", Value: "Gen-2.5-Extreme-Low"}, {Name: "geometry_file_format", Value: "glb"},
		{Name: "material", Value: "PBR"}, {Name: "mesh_mode", Value: "Raw"}, {Name: "quality", Value: "medium"},
		{Name: "texture_mode", Value: "extreme-low"},
		{Name: "image_label", Value: "F"}, {Name: "image_label", Value: "L"}, {Name: "image_label", Value: "B"},
		{Name: "images", File: "front.png", MediaType: "image/png", PNG: true},
		{Name: "images", File: "left.png", MediaType: "image/png", PNG: true},
		{Name: "images", File: "back.png", MediaType: "image/png", PNG: true},
	}
	got := recorder.sent[Prefix+"/rodin"]
	if len(got) != len(want) {
		t.Fatalf("the upstream received %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The overflow probe sends two pictures with the three labels: the fake, like
// Rodin, refuses it as IMAGE_LABEL_LENGTH_TOO_LONG, so the follow-up steps are
// skipped and nothing is generated.
func TestLabelOverflowProbeIsRefusedByTheFake(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	bundle, err := align.Run(context.Background(), "hyper3d", "api-v2", align.Target{
		Name: "fake", BaseURL: f.Server.URL + Prefix, Key: key, NoPollDelay: true,
	}, profileSteps(t, "rodin-label-overflow", "rodin-label-overflow-status", "rodin-label-overflow-download"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	submit := bundle.Obs[0]
	if !bytes.Contains(submit.Body, []byte("IMAGE_LABEL_LENGTH_TOO_LONG")) {
		t.Fatalf("submission = %d %s, want IMAGE_LABEL_LENGTH_TOO_LONG", submit.Status, submit.Body)
	}
	for _, follow := range bundle.Obs[1:] {
		if follow.Skipped == "" {
			t.Errorf("%s ran after a refused submission: %+v", follow.Step, follow)
		}
	}
}
