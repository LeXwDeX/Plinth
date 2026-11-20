package tripov3

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// The third partial run of the alignment script (#386): the multiview model with
// the production defaults and its 3MF export, run with -artifacts to keep the real
// 3MF. One approved real run is two paid requests, so the script must be exact and
// must never send a paid request whose inputs an earlier step did not produce.

var multiviewRun = []string{
	"mv-upload-front", "mv-upload-left", "mv-upload-back",
	"multiview-to-model", "multiview-to-model-poll", "convert-3mf", "convert-3mf-poll",
}

// profileSteps builds the script once: it draws its pictures, which is slow under
// the race detector.
var profileSteps = sync.OnceValue(func() []align.Step { return Profile().Steps })

// selectSteps is the CLI's -steps: the named steps of the profile, in profile order.
func selectSteps(t *testing.T, names ...string) []align.Step {
	t.Helper()
	keep := map[string]bool{}
	for _, name := range names {
		keep[name] = true
	}
	var steps []align.Step
	for _, step := range profileSteps() {
		if keep[step.Name] {
			steps = append(steps, step)
			delete(keep, step.Name)
		}
	}
	if len(keep) > 0 {
		t.Fatalf("the profile has no step %v", keep)
	}
	return steps
}

var placeholder = regexp.MustCompile(`\{([a-z0-9_]+)\}`)

func marshal(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMultiviewTo3MFScriptIsOneChainOfTwoPaidRequests(t *testing.T) {
	steps := selectSteps(t, multiviewRun...)
	for i, step := range steps {
		if step.Name != multiviewRun[i] {
			t.Fatalf("step %d is %s, want %s", i, step.Name, multiviewRun[i])
		}
	}
	// Two paid requests: the generation and the export. The uploads and polls are free.
	var paid []string
	for _, step := range steps {
		if step.Paid {
			paid = append(paid, step.Name)
		}
	}
	if strings.Join(paid, ",") != "multiview-to-model,convert-3mf" {
		t.Fatalf("paid steps = %v", paid)
	}
	// RequireVar chains every step to the one before it, so a step that is selected
	// without what it needs, or whose predecessor produced nothing, is skipped; and
	// each variable a step uses was captured by an earlier step of the run.
	wantRequire := map[string]string{
		"mv-upload-front": "", "mv-upload-left": "mv_front", "mv-upload-back": "mv_left",
		"multiview-to-model": "mv_back", "multiview-to-model-poll": "m2m",
		"convert-3mf": "m2m", "convert-3mf-poll": "m2m_convert",
	}
	captured := map[string]bool{}
	for _, step := range steps {
		if step.RequireVar != wantRequire[step.Name] {
			t.Errorf("%s requires %q, want %q", step.Name, step.RequireVar, wantRequire[step.Name])
		}
		if step.RequireVar != "" && !captured[step.RequireVar] {
			t.Errorf("%s requires %s, which no earlier step captures", step.Name, step.RequireVar)
		}
		for _, match := range placeholder.FindAllStringSubmatch(step.Path+marshal(t, step.Body), -1) {
			if !captured[match[1]] {
				t.Errorf("%s uses {%s}, which no earlier step captures", step.Name, match[1])
			}
		}
		for name := range step.Capture {
			captured[name] = true
		}
	}
}

// The generation request is what the adapter sends for the Offering
// tripo/tripo-v3.1-multiview/image-to-3d at its defaults with three views, and the
// export is the 3MF request of an Offering that asks for nothing special; the
// adapter side is pinned in internal/providers/tripo.
func TestMultiviewTo3MFScriptBodiesAreTheProductionDefaults(t *testing.T) {
	byName := map[string]align.Step{}
	for _, step := range selectSteps(t, multiviewRun...) {
		byName[step.Name] = step
	}
	const generation = `{"auto_size":false,"export_uv":true,"face_limit":2000000,"generate_parts":false,` +
		`"geometry_quality":"detailed","inputs":[{"front":"{mv_front}"},{"left":"{mv_left}"},{"back":"{mv_back}"}],` +
		`"model":"v3.1-20260211","orientation":"default","pbr":true,"quad":false,"smart_low_poly":false,` +
		`"texture":true,"texture_alignment":"original_image","texture_quality":"extreme","texture_version":"v3.5-20260815"}`
	if got := marshal(t, byName["multiview-to-model"].Body); got != generation {
		t.Errorf("generation body:\n got %s\nwant %s", got, generation)
	}
	if got := marshal(t, byName["convert-3mf"].Body); got != `{"format":"3MF","input":"{m2m}"}` {
		t.Errorf("export body = %s", got)
	}
	for name, path := range map[string]string{
		"mv-upload-front": "/files", "mv-upload-left": "/files", "mv-upload-back": "/files",
		"multiview-to-model": "/generation/multiview-to-model", "convert-3mf": "/models/convert",
		"multiview-to-model-poll": "/tasks/{m2m}", "convert-3mf-poll": "/tasks/{m2m_convert}",
	} {
		if byName[name].Path != path {
			t.Errorf("%s path = %s, want %s", name, byName[name].Path, path)
		}
	}
	// Both polls fetch what the task outputs the moment it is done: the URLs expire.
	// The generation poll has the Offering's own budget (timeout_seconds 3600).
	for _, name := range []string{"multiview-to-model-poll", "convert-3mf-poll"} {
		step := byName[name]
		if strings.Join(step.Fetch, ",") != "data.output.*" || step.Poll == nil || !strings.Contains(strings.Join(step.Poll.Terminal, ","), "success") {
			t.Errorf("%s: fetch %v, poll %+v", name, step.Fetch, step.Poll)
		}
	}
	if budget := time.Duration(byName["multiview-to-model-poll"].Poll.Max) * byName["multiview-to-model-poll"].Poll.Interval; budget < time.Hour {
		t.Errorf("the generation poll gives up after %s, before the Offering's hour", budget)
	}
}

// The three uploads are three different 512x512 pictures sent the way the adapter
// sends a reference (field file, input.png, image/png).
func TestMultiviewUploadsAreThreeDifferentPictures(t *testing.T) {
	seen := map[string]string{}
	for _, step := range selectSteps(t, "mv-upload-front", "mv-upload-left", "mv-upload-back") {
		file := step.Multipart
		if file == nil || file.Field != "file" || file.Filename != "input.png" || file.MediaType != "image/png" {
			t.Fatalf("%s: upload %+v", step.Name, file)
		}
		decoded, err := png.Decode(bytes.NewReader(file.Data))
		if err != nil || decoded.Bounds().Dx() != 512 || decoded.Bounds().Dy() != 512 {
			t.Fatalf("%s: not a 512x512 PNG: %v", step.Name, err)
		}
		if other, duplicate := seen[string(file.Data)]; duplicate {
			t.Errorf("%s sends the same picture as %s", step.Name, other)
		}
		seen[string(file.Data)] = step.Name
	}
}

// RequireVar keeps a paid request from going out with a placeholder instead of an
// id: with the uploads missing, or a predecessor missing, everything after is
// skipped and nothing is paid; the export alone runs once the generation's task id
// is given (a resume of a run that stopped after the generation).
func TestMultiviewTo3MFScriptNeverSendsAPaidRequestWithoutItsInputs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	run := func(seed map[string]string, names ...string) *align.Bundle {
		t.Helper()
		bundle, err := align.Run(context.Background(), "tripo", "v3",
			align.Target{Name: "fake", BaseURL: f.base(), Key: key, NoPollDelay: true}, selectSteps(t, names...), nil, seed)
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	for name, steps := range map[string][]string{
		"no upload at all":      {"multiview-to-model", "multiview-to-model-poll", "convert-3mf", "convert-3mf-poll"},
		"only the last upload":  {"mv-upload-back", "multiview-to-model", "multiview-to-model-poll", "convert-3mf", "convert-3mf-poll"},
		"the front and the end": {"mv-upload-front", "mv-upload-back", "multiview-to-model", "convert-3mf"},
		"the export alone":      {"convert-3mf", "convert-3mf-poll"},
	} {
		bundle := run(nil, steps...)
		if bundle.PaidRequests != 0 {
			t.Errorf("%s: %d paid request(s) went out", name, bundle.PaidRequests)
		}
		for _, obs := range bundle.Obs {
			if obs.Step == "mv-upload-front" {
				continue // the first upload needs nothing and is free
			}
			if obs.Skipped == "" {
				t.Errorf("%s: step %s ran", name, obs.Step)
			}
		}
	}
	bundle := run(map[string]string{"m2m": "00000000-0000-4000-8000-000000000386"}, "convert-3mf", "convert-3mf-poll")
	if bundle.PaidRequests != 1 || bundle.Obs[0].Skipped != "" || bundle.Obs[1].Skipped != "" || len(bundle.Obs[1].Polls) == 0 {
		t.Fatalf("the export with the generation's id given: %+v", bundle)
	}
}

// The whole script runs against the fake, as `fake-upstream-align fake` runs it, and
// with an ArtifactDir leaves the model, its picture and the 3MF as files; the
// requests the fake received are the bodies of the script with the real ids in.
func TestMultiviewTo3MFScriptRunsAgainstTheFakeAndKeepsTheBytes(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, Version())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	posted := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/files") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			mu.Lock()
			posted[strings.TrimPrefix(r.URL.Path, Prefix)] = raw
			mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := align.Run(context.Background(), "tripo", "v3",
		align.Target{Name: "fake", BaseURL: server.URL + Prefix, Key: key, NoPollDelay: true, ArtifactDir: dir},
		selectSteps(t, multiviewRun...), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.PaidRequests != 2 {
		t.Fatalf("paid requests = %d", bundle.PaidRequests)
	}
	byStep := map[string]align.Obs{}
	for _, obs := range bundle.Obs {
		if obs.Skipped != "" || obs.Status != http.StatusOK {
			t.Fatalf("step %s: skipped %q, status %d", obs.Step, obs.Skipped, obs.Status)
		}
		byStep[obs.Step] = obs
	}
	taskID := func(step string) string {
		var created struct {
			Data struct {
				TaskID string `json:"task_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(byStep[step].Body, &created); err != nil || created.Data.TaskID == "" {
			t.Fatalf("%s: no task id in %s", step, byStep[step].Body)
		}
		return created.Data.TaskID
	}
	var generation map[string]any
	if err := json.Unmarshal(posted["/generation/multiview-to-model"], &generation); err != nil {
		t.Fatal(err)
	}
	inputs, _ := generation["inputs"].([]any)
	delete(generation, "inputs")
	if got := marshal(t, generation); got != `{"auto_size":false,"export_uv":true,"face_limit":2000000,"generate_parts":false,`+
		`"geometry_quality":"detailed","model":"v3.1-20260211","orientation":"default","pbr":true,"quad":false,`+
		`"smart_low_poly":false,"texture":true,"texture_alignment":"original_image","texture_quality":"extreme","texture_version":"v3.5-20260815"}` {
		t.Errorf("generation request: %s", got)
	}
	tokens := map[string]bool{}
	for i, view := range []string{"front", "left", "back"} {
		entry, _ := inputs[i].(map[string]any)
		token, _ := entry[view].(string)
		if len(entry) != 1 || !strings.HasPrefix(token, "file_") || tokens[token] {
			t.Errorf("inputs[%d] = %v", i, inputs[i])
		}
		tokens[token] = true
	}
	if len(inputs) != 3 {
		t.Errorf("inputs = %v", inputs)
	}
	if got, want := string(posted["/models/convert"]), marshal(t, map[string]any{"format": "3MF", "input": taskID("multiview-to-model")}); got != want {
		t.Errorf("export request = %s, want %s", got, want)
	}

	// The kept bytes: the generation's model and picture, and the 3MF.
	threeMF, err := fakeupstream.ModelFile("3mf:tripo")
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string][]byte{"convert-3mf-poll-1.3mf": threeMF.Data} {
		got, readErr := os.ReadFile(filepath.Join(dir, file))
		if readErr != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: kept %d bytes (%v), want the fake's %d", file, len(got), readErr, len(want))
		}
	}
	exported := byStep["convert-3mf-poll"].Artifacts
	if len(exported) != 1 || exported[0].File != "convert-3mf-poll-1.3mf" || exported[0].Format != "zip" || len(exported[0].SHA256) != 64 {
		t.Errorf("the 3MF observation: %+v", exported)
	}
	generated := byStep["multiview-to-model-poll"].Artifacts
	if len(generated) != 2 || generated[0].File != "multiview-to-model-poll-1.glb" || generated[1].File != "multiview-to-model-poll-2.webp" {
		t.Errorf("the generation's artifacts: %+v", generated)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("directory holds %d entries (%v)", len(entries), err)
	}
	for _, entry := range entries {
		if info, _ := entry.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, want 0600", entry.Name(), info.Mode().Perm())
		}
	}
}
