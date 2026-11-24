package tripov3

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// The approved real run of 2026-11-19 (#386): three uploads and a multiview
// model at Plinth's production defaults (70 credits), then a 3MF export of that
// model (5 credits). The candidates hold the whole sequence of polls of each
// task. They are evidence for the fake, not templates it derives answers from
// (they are not in sampleFS): the checks below hold the fake's answers to them.
// The 3MF the run delivered was 35,952,441 bytes and was refused by the archive
// isolation for the vendor namespace it declares; tripo3mf.go describes it.
//
//go:embed testdata/evidence-386-multiview-model.candidate.json testdata/evidence-386-convert-3mf.candidate.json
var evidence386FS embed.FS

const (
	evidenceMultiviewFile  = "testdata/evidence-386-multiview-model.candidate.json"
	evidenceConvert3MFFile = "testdata/evidence-386-convert-3mf.candidate.json"
)

func loadEvidence386(t *testing.T, file string) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(evidence386FS, file)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

// firstPoll is the first poll of the task of the evidence that has the status.
func firstPoll(t *testing.T, samples *fakeupstream.Samples, status string) wirefixture.Exchange {
	t.Helper()
	for _, exchange := range samples.Find("GET /v3/tasks/{id}", "") {
		if body := mustBody(t, exchange); field(t, body, "data", "status") == status {
			return exchange
		}
	}
	t.Fatalf("the evidence has no %s poll", status)
	return wirefixture.Exchange{}
}

// requestOf decodes the body of the evidence's request for key.
func requestOf(t *testing.T, samples *fakeupstream.Samples, key string) map[string]any {
	t.Helper()
	exchanges := samples.Find(key, "")
	if len(exchanges) != 1 {
		t.Fatalf("%s: %d exchanges in the evidence", key, len(exchanges))
	}
	decoder := json.NewDecoder(bytes.NewReader(exchanges[0].Request.Body.JSON))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The fake's multiview model task answers in the structure of the real one at
// each status it shows: the request's parameters under input with model_version
// and the files array, a queued poll without output, and a finished task whose
// output holds the model and a WebP render.
func TestMultiviewModelPollsKeepTheStructureOfTheRealTask(t *testing.T) {
	samples := loadEvidence386(t, evidenceMultiviewFile)
	f := newFixture(t, fakeupstream.Config{})
	// The request of the run, with the file tokens the evidence replaced by ids.
	status, _, created := f.do(http.MethodPost, "/generation/multiview-to-model", key, requestOf(t, samples, "POST /v3/generation/multiview-to-model"))
	if status != http.StatusOK {
		t.Fatalf("create = %d %v", status, created)
	}
	assertSameShape(t, "multiview create", sampleShape(t, samples.Find("POST /v3/generation/multiview-to-model", "")[0]), shapeOfBody(t, created))
	id := field(t, created, "data", "task_id").(string)

	// queued, then running, then success: the statuses the fake plays.
	for _, want := range []string{statusQueued, statusRunning, statusSuccess} {
		_, _, poll := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		if got := field(t, poll, "data", "status"); got != want {
			t.Fatalf("poll returned %v, want %s", got, want)
		}
		// The real running polls show an empty output until the render exists
		// (progress 99); the fake's running poll is that empty output.
		assertSameShape(t, "multiview "+want, sampleShape(t, firstPoll(t, samples, want)), shapeOfBody(t, poll))
	}
}

// The files array repeats the request's views in the order front, left, back,
// right, and the poll shows the request's own parameters under their own names
// with the model as model_version, as the real task did.
func TestMultiviewModelPollEchoesTheRequest(t *testing.T) {
	samples := loadEvidence386(t, evidenceMultiviewFile)
	f := newFixture(t, fakeupstream.Config{})
	request := requestOf(t, samples, "POST /v3/generation/multiview-to-model")
	id := f.create("/generation/multiview-to-model", request, key)
	_, done := f.finish(id, key)
	input, _ := fakeupstream.DigMap(done, "data", "input")
	if input == nil {
		t.Fatalf("no input in %v", done)
	}
	for name, want := range request {
		switch name {
		case "model":
			if input["model_version"] != want {
				t.Errorf("model_version = %v, want %v", input["model_version"], want)
			}
			if _, named := input["model"]; named {
				t.Error("the poll names the model model, not model_version")
			}
		case "inputs":
			if _, named := input["inputs"]; named {
				t.Error("the poll repeats the request's inputs instead of showing files")
			}
		default:
			if got, _ := json.Marshal(input[name]); string(got) != mustMarshal(t, want) {
				t.Errorf("input.%s = %s, want %s", name, got, mustMarshal(t, want))
			}
		}
	}
	files, _ := input["files"].([]any)
	if len(files) != 4 {
		t.Fatalf("files = %v, want four entries", input["files"])
	}
	for i, entry := range files {
		object, _ := entry.(map[string]any)
		if i == 3 {
			if entry != nil {
				t.Errorf("files[3] = %v, want null (no right view was sent)", entry)
			}
			continue
		}
		stored, _ := object["object"].(map[string]any)
		if len(object) != 1 || stored["bucket"] != "tripo-data" || stored["key"] == "" || stored["key"] == nil {
			t.Errorf("files[%d] = %v, want one stored object", i, entry)
		}
	}
}

// Constructed, not observed: the run sent three views in order. Views given out
// of order or with a gap still land in their place of the files array, and a
// parameter the request leaves out is not echoed.
func TestMultiviewModelPollPlacesViewsByNameAndEchoesOnlyWhatWasSent(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/generation/multiview-to-model", map[string]any{
		"model":  "v3.1-20260211",
		"inputs": []map[string]string{{"right": "file_r"}, {"front": "file_f"}},
	}, key)
	_, done := f.finish(id, key)
	input, _ := fakeupstream.DigMap(done, "data", "input")
	files, _ := input["files"].([]any)
	if len(files) != 4 || files[1] != nil || files[2] != nil {
		t.Fatalf("files = %v, want [front, null, null, right]", input["files"])
	}
	for index, token := range map[int]string{0: "file_f", 3: "file_r"} {
		object, _ := files[index].(map[string]any)
		if stored, _ := object["object"].(map[string]any); !strings.HasSuffix(stored["key"].(string), token) {
			t.Errorf("files[%d] = %v, want the upload %s", index, files[index], token)
		}
	}
	if len(input) != 2 || input["model_version"] != "v3.1-20260211" {
		t.Errorf("input = %v, want only model_version and files", input)
	}
}

// The render of a multiview model task is a WebP file named so, like the real one
// (the bytes of the run: webp, binary/octet-stream). Since the round of 2026-11-24
// (#577) the render of every task is a WebP; the PNG of 2026-10-29 is gone.
func TestMultiviewModelRenderIsAWebP(t *testing.T) {
	samples := loadEvidence386(t, evidenceMultiviewFile)
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/generation/multiview-to-model", requestOf(t, samples, "POST /v3/generation/multiview-to-model"), key)
	_, done := f.finish(id, key)
	renderURL := f.outputURL(done, "rendered_image_url")
	if !strings.HasSuffix(strings.Split(renderURL, "?")[0], "/rendered_image.webp") {
		t.Errorf("rendered_image_url = %s, want a .webp file", renderURL)
	}
	status, contentType, data := f.download(renderURL)
	if status != http.StatusOK || contentType != ArtifactContentType || align.DetectFormat(data) != "webp" {
		t.Errorf("render: %d %q %s, want 200 %s webp", status, contentType, align.DetectFormat(data), ArtifactContentType)
	}
	_, _, model := f.download(f.outputURL(done, "model_url"))
	if align.DetectFormat(model) != "glb" {
		t.Errorf("model is %s, want glb", align.DetectFormat(model))
	}

	text := f.create("/generation/text-to-model", textBody(), key)
	_, textDone := f.finish(text, key)
	_, _, render := f.download(f.outputURL(textDone, "rendered_image_url"))
	if align.DetectFormat(render) != "webp" {
		t.Errorf("the text task's render is %s, want the webp of 2026-11-24", align.DetectFormat(render))
	}
}

// The 3MF export of that model answers in the structure of the real conversion at
// each status it shows, and delivers a package the archive isolation accepts: the
// real one was refused for the vendor namespace it declares, so the fake's has it.
func TestConvert3MFPollsKeepTheStructureOfTheRealExport(t *testing.T) {
	samples := loadEvidence386(t, evidenceConvert3MFFile)
	multiview := loadEvidence386(t, evidenceMultiviewFile)
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/multiview-to-model", requestOf(t, multiview, "POST /v3/generation/multiview-to-model"), key)
	f.finish(source, key)

	// Plinth's request of the run: the task id and the format, nothing else.
	id := f.create("/models/convert", map[string]any{"input": source, "format": "3MF"}, key)
	for _, want := range []string{statusQueued, statusRunning, statusSuccess} {
		_, _, poll := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		if got := field(t, poll, "data", "status"); got != want {
			t.Fatalf("poll returned %v, want %s", got, want)
		}
		assertSameShape(t, "3MF export "+want, sampleShape(t, firstPoll(t, samples, want)), shapeOfBody(t, poll))
		if want != statusSuccess {
			continue
		}
		exportURL := f.outputURL(poll, "model_url")
		if !strings.HasSuffix(strings.Split(exportURL, "?")[0], "/model.3mf") {
			t.Errorf("the 3MF export is delivered as %s, want a model.3mf", exportURL)
		}
		status, contentType, data := f.download(exportURL)
		if status != http.StatusOK || contentType != ArtifactContentType || align.DetectFormat(data) != "zip" {
			t.Fatalf("3MF: %d %q %s, want 200 %s zip", status, contentType, align.DetectFormat(data), ArtifactContentType)
		}
		// The package of the real file's structure (vendor namespace, ZIP64 framing),
		// not the generic 3MF fixture the other fakes deliver.
		if tripoPackage, err := fakeupstream.ModelFile("3mf:tripo"); err != nil || !bytes.Equal(data, tripoPackage.Data) {
			t.Errorf("the 3MF the fake delivers is not the Tripo package (%d bytes, %v)", len(data), err)
		}
		// The check the real file was run through.
		path := filepath.Join(t.TempDir(), "converted.3mf")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := local.ValidateDeliveredFile(file, "converted_model", "converted.3mf", "binary/octet-stream"); err != nil {
			t.Errorf("the 3MF the fake delivers is refused by the archive isolation: %v", err)
		}
	}
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
