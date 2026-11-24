package tripov3

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #577: the real round of 2026-11-24 on dev-test ran every Tripo function of
// smoke-l5-tripo-endpoints.py once through Plinth (855 credits, as quoted). The
// runtime recorder kept 1,289 sanitized exchanges; evidence-577-real-round holds the
// minimal set the fake is held to: the create, the first poll and the finished poll
// of a rig (v2.5), a decimation to 1,000 faces, a segmentation (detailed), a quick_cap
// completion, an extreme PBR texture, an imported-model and a file-token conversion
// and a conversion by task id, a Smart Segmentation of an uploaded model, an image to
// model, an image to splat and the product-default text to model (extreme texture,
// detailed geometry). It is evidence, not a template (it is not in sampleFS).
//
//go:embed testdata/evidence-577-real-round.candidate.json
var evidence577FS embed.FS

const evidence577File = "testdata/evidence-577-real-round.candidate.json"

func loadEvidence577(t *testing.T) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(evidence577FS, evidence577File)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

// realRoundPoll is the first poll of the 2026-11-24 evidence with the type and
// status, nil when the round has none.
func realRoundPoll(t *testing.T, typ, status string) *wirefixture.Exchange {
	t.Helper()
	return realPoll(loadEvidence577(t), typ, status)
}

// supersededPoll is the newer real poll a drift comparison is held to where the
// 2026-11-24 round contradicts the 2026-10-29 sample of the same request: the
// conversion of an imported model (the sample's STL from a URL) reported
// normalize_scale and normalize_translation in 2026-11 and not in 2026-10. The
// newest real answer wins; nil keeps the committed sample.
func supersededPoll(t *testing.T, typ, status string) *wirefixture.Exchange {
	t.Helper()
	if typ != "convert_model" || status != statusSuccess {
		return nil
	}
	for _, exchange := range loadEvidence577(t).Find("GET /v3/tasks/{id}", "") {
		data, _ := fakeupstream.DigMap(mustBody(t, exchange), "data")
		input, _ := data["input"].(map[string]any)
		if data["status"] == statusSuccess && data["type"] == typ && input["texture_size"] != nil {
			return &exchange
		}
	}
	t.Fatal("the 2026-11-24 evidence has no finished conversion of an imported model")
	return nil
}

// realRoundTask is one task of the evidence: its create request and its polls.
type realRoundTask struct {
	path    string
	request map[string]any
	polls   []map[string]any
}

func realRoundTasks(t *testing.T) []realRoundTask {
	t.Helper()
	samples := loadEvidence577(t)
	var tasks []realRoundTask
	index := map[string]int{}
	for _, exchange := range samples.Exchanges {
		body := mustBody(t, exchange)
		data, _ := fakeupstream.DigMap(body, "data")
		id, _ := data["task_id"].(string)
		switch {
		case exchange.Request.Method == http.MethodPost && exchange.Request.Path != "/v3/files":
			decoder := json.NewDecoder(strings.NewReader(string(exchange.Request.Body.JSON)))
			decoder.UseNumber()
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				t.Fatal(err)
			}
			index[id] = len(tasks)
			tasks = append(tasks, realRoundTask{path: strings.TrimPrefix(exchange.Request.Path, "/v3"), request: request})
		case exchange.Request.Method == http.MethodGet:
			if at, ok := index[id]; ok {
				tasks[at].polls = append(tasks[at].polls, body)
			}
		}
	}
	return tasks
}

// realRoundSource points a recorded request's input (a redacted token, task id or
// URL) at a source the fixture made of the same kind.
func (f *fixture) realRoundSource(task realRoundTask, sources map[string]string) {
	f.t.Helper()
	input, _ := task.request["input"].(string)
	if input == "" {
		return
	}
	text := func() string {
		id := f.create("/generation/text-to-model", textBody(), key)
		f.finish(id, key)
		return id
	}
	glbToken := func() string {
		_, done := f.finish(text(), key)
		_, _, model := f.download(f.outputURL(done, "model_url"))
		_, _, uploaded := f.upload("model.glb", "model/gltf-binary", model, key)
		return field(f.t, uploaded, "data", "file_token").(string)
	}
	source := ""
	switch task.path {
	case "/generation/image-to-model", "/generation/image-to-splat":
		_, _, uploaded := f.upload("input.png", "image/png", uploadPNG(), key)
		source = field(f.t, uploaded, "data", "file_token").(string)
	case "/mesh/smartsegment":
		source = glbToken()
	case "/mesh/complete":
		segment := f.create("/mesh/segment", map[string]any{"input": text(), "model": "v2.0-20260430"}, key)
		f.finish(segment, key)
		source = segment
	case "/models/convert":
		data, _ := fakeupstream.DigMap(task.polls[0], "data")
		input, _ := data["input"].(map[string]any)
		original, _ := input["original_model_id"].(string)
		switch {
		case isHTTP(task.request["input"].(string)):
			_, done := f.finish(text(), key)
			source = f.outputURL(done, "model_url")
		case sources[original] != "":
			source = sources[original] // a task of the round named by its id
		default:
			source = glbToken() // an uploaded model
		}
	default:
		source = text()
	}
	task.request["input"] = source
}

// Every task of the round, sent to the fake with its recorded request, answers in
// the structure of the real task at the status of its first poll and when it
// finishes, repeats the request under input as the real poll did, and reports the
// real price from its first poll on.
func TestRealRoundPollsKeepTheStructureOfTheRealTasks(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	sources := map[string]string{}
	for _, task := range realRoundTasks(t) {
		first, last := task.polls[0], task.polls[len(task.polls)-1]
		realData, _ := fakeupstream.DigMap(last, "data")
		what := fmt.Sprintf("%s (%v)", task.path, realData["type"])
		f.realRoundSource(task, sources)
		id := f.create(task.path, task.request, key)
		createdID, _ := fakeupstream.Dig(first, "data", "task_id")
		sources[fmt.Sprint(createdID)] = id
		firstStatus, _ := fakeupstream.Dig(first, "data", "status")
		var finished map[string]any
		for i := 0; i < 4 && finished == nil; i++ {
			_, _, poll := f.do(http.MethodGet, "/tasks/"+id, key, nil)
			status := field(t, poll, "data", "status")
			if status == firstStatus && status != statusSuccess {
				assertSameShape(t, what+" "+fmt.Sprint(status), shapeOfBody(t, first), shapeOfBody(t, poll))
				assertSameCredits(t, what+" "+fmt.Sprint(status), first, poll)
			}
			if status == statusSuccess {
				finished = poll
			}
		}
		if finished == nil {
			t.Fatalf("%s never finished", what)
		}
		assertSameShape(t, what+" success", shapeOfBody(t, last), shapeOfBody(t, finished))
		assertSameCredits(t, what+" success", last, finished)
		realInput, _ := fakeupstream.DigMap(last, "data", "input")
		fakeInput, _ := fakeupstream.DigMap(finished, "data", "input")
		for name, want := range realInput {
			switch name {
			case "original_model_id", "original_task_id", "file", "files":
				continue // ids and stored objects are placeholders on both sides
			}
			if mustMarshal(t, fakeInput[name]) != mustMarshal(t, want) {
				t.Errorf("%s input.%s = %s, the real poll showed %s", what, name, mustMarshal(t, fakeInput[name]), mustMarshal(t, want))
			}
		}
	}
}

// assertSameCredits compares credits_consumed of a real and a fake poll.
func assertSameCredits(t *testing.T, what string, real, fake map[string]any) {
	t.Helper()
	want, _ := fakeupstream.Dig(real, "data", "credits_consumed")
	got, _ := fakeupstream.Dig(fake, "data", "credits_consumed")
	if fmt.Sprint(toFloat(want)) != fmt.Sprint(toFloat(got)) {
		t.Errorf("%s credits_consumed = %v, the real poll showed %v", what, got, want)
	}
}

func toFloat(value any) any {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Float64()
		if err == nil {
			return parsed
		}
	}
	return value
}

// Every render of the round was a WebP (the URLs named legacy_mesh.webp and Plinth
// read each file as image/webp): the fake's render of every task that has one is a
// WebP file named so.
func TestEveryRenderIsAWebP(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	sources := map[string]string{}
	for _, task := range realRoundTasks(t) {
		f.realRoundSource(task, sources)
		_, done := f.finish(f.create(task.path, task.request, key), key)
		renderURL, has := field(t, done, "data", "output").(map[string]any)["rendered_image_url"].(string)
		if !has {
			continue
		}
		if !strings.HasSuffix(strings.Split(renderURL, "?")[0], ".webp") {
			t.Errorf("%s: rendered_image_url = %s, want a .webp file", task.path, renderURL)
		}
		if _, _, data := f.download(renderURL); align.DetectFormat(data) != "webp" {
			t.Errorf("%s: the render is %s, want webp", task.path, align.DetectFormat(data))
		}
	}
}
