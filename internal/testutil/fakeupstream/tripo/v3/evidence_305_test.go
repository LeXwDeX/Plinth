package tripov3

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// The paid run of 2026-11-16 (#305): a text-to-model task, a rig check, a rig and
// two conversions of the rig's GLB (GLTF and FBX, PNG textures of 2048 pixels,
// with_animation, no pivot), 65 credits. The candidate holds the first poll of
// each status of every task. It is evidence for the fake, not one of the samples
// the fake derives its templates from (it is not in sampleFS): the checks below
// hold the fake's answers to it.
//
//go:embed testdata/evidence-305-rig-convert.candidate.json
var evidenceFS embed.FS

const evidenceFile = "testdata/evidence-305-rig-convert.candidate.json"

func loadEvidence(t *testing.T) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(evidenceFS, evidenceFile)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

// evidencePolls are the polls of one task of the run, by status.
func evidencePolls(t *testing.T, samples *fakeupstream.Samples, taskType string, format string) map[string]wirefixture.Exchange {
	t.Helper()
	polls := map[string]wirefixture.Exchange{}
	for _, exchange := range samples.Find("GET /v3/tasks/{id}", "") {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := fakeupstream.DigMap(body, "data")
		if data["type"] != taskType {
			continue
		}
		if input, _ := fakeupstream.DigMap(data, "input"); format != "" && input["format"] != format {
			continue
		}
		polls[data["status"].(string)] = exchange
	}
	return polls
}

// withoutOutput is the shape of a poll body without its output object: the 2026-10-29
// STL conversion shows an empty output while it runs and the GLTF and FBX ones of
// 2026-11-16 show none, which the fake does not choose between.
func withoutOutput(t *testing.T, body map[string]any) fakeupstream.Shape {
	t.Helper()
	cloned := fakeupstream.Clone(body)
	if data, ok := fakeupstream.DigMap(cloned, "data"); ok {
		delete(data, "output")
	}
	return shapeOfBody(t, cloned)
}

// What the run was charged is what the fake reports: the credits of the evidence
// are the fake's price table, task by task (the generation, 20 credits, is not
// priced by the fake: its polls keep the sample's value, billing.go).
func TestFakePricesAreTheCreditsOfThePaidRun(t *testing.T) {
	samples := loadEvidence(t)
	want := map[string]float64{"animate_prerigcheck": 0, "animate_rig": creditsRig}
	for taskType, credits := range want {
		for status, exchange := range evidencePolls(t, samples, taskType, "") {
			body, _ := fakeupstream.JSONBody(exchange)
			data, _ := fakeupstream.DigMap(body, "data")
			if got, _ := data["credits_consumed"].(json.Number).Float64(); got != credits {
				t.Errorf("%s %s: the run was charged %v, the fake's price is %v", taskType, status, got, credits)
			}
		}
	}
	for _, format := range []string{"GLTF", "FBX"} {
		for status, exchange := range evidencePolls(t, samples, "convert_model", format) {
			body, _ := fakeupstream.JSONBody(exchange)
			data, _ := fakeupstream.DigMap(body, "data")
			if got, _ := data["credits_consumed"].(json.Number).Float64(); got != creditsConvertAdvanced {
				t.Errorf("%s conversion %s: the run was charged %v, the fake's advanced price is %v", format, status, got, creditsConvertAdvanced)
			}
		}
	}
	// The request of the run is advanced under the fake's reading of the page.
	var request map[string]any
	for _, exchange := range samples.Find("POST /v3/models/convert", "") {
		decoder := json.NewDecoder(bytes.NewReader(exchange.Request.Body.JSON))
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !convertAdvanced(request) {
			t.Errorf("the run's conversion request %v is not advanced for the fake", request)
		}
	}
}

// The fake's rig and conversions answer in the structure of the run.
func TestFakeConversionAndRigPollsKeepTheStructureOfThePaidRun(t *testing.T) {
	samples := loadEvidence(t)
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)

	// The create envelopes.
	rigBody := map[string]any{"input": gen, "model": "v3.0-20260909", "rig_type": "biped", "spec": "tripo", "out_format": "glb"}
	rigID := f.create("/animations/rig", rigBody, key)
	for _, create := range []struct {
		key  string
		path string
		body map[string]any
	}{
		{"POST /v3/animations/rig", "/animations/rig", rigBody},
		{"POST /v3/models/convert", "/models/convert", map[string]any{"input": "https://example.test/x.glb", "format": "GLTF"}},
	} {
		status, _, response := f.do(http.MethodPost, create.path, key, create.body)
		var real *wirefixture.Exchange
		for _, exchange := range samples.Find(create.key, "") {
			if exchange.Response.Status == http.StatusOK {
				real = &exchange
				break
			}
		}
		if real == nil || status != http.StatusOK {
			t.Fatalf("%s: status %d, evidence %v", create.key, status, real)
		}
		assertSameShape(t, create.key, sampleShape(t, *real), shapeOfBody(t, response))
	}

	// The rig: running and success polls.
	rigPolls := evidencePolls(t, samples, "animate_rig", "")
	f.poll(rigID, 1)
	for _, status := range []string{statusRunning, statusSuccess} {
		_, _, body := f.do(http.MethodGet, "/tasks/"+rigID, key, nil)
		real := rigPolls[status]
		assertSameShape(t, "rig "+status, withoutOutput(t, mustBody(t, real)), withoutOutput(t, body))
	}
	_, done := f.finish(rigID, key)
	assertSameShape(t, "rig success output", outputShape(t, mustBody(t, rigPolls[statusSuccess])), outputShape(t, done))

	// The conversions of the rig's GLB, with the requests of the run.
	_, rigDone := f.finish(rigID, key)
	modelURL := f.outputURL(rigDone, "model_url")
	for _, format := range []string{"GLTF", "FBX"} {
		polls := evidencePolls(t, samples, "convert_model", format)
		id := f.create("/models/convert", map[string]any{
			"input": modelURL, "format": format, "texture_format": "PNG", "texture_size": 2048,
			"with_animation": true, "bake": true, "fbx_preset": "blender",
		}, key)
		f.poll(id, 1)
		_, _, running := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		assertSameShape(t, format+" conversion running", withoutOutput(t, mustBody(t, polls[statusRunning])), withoutOutput(t, running))
		_, _, success := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		assertSameShape(t, format+" conversion success", sampleShape(t, polls[statusSuccess]), shapeOfBody(t, success))
	}
}

func mustBody(t *testing.T, exchange wirefixture.Exchange) map[string]any {
	t.Helper()
	body, err := fakeupstream.JSONBody(exchange)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// outputShape is the shape of a poll's output object alone.
func outputShape(t *testing.T, body map[string]any) fakeupstream.Shape {
	t.Helper()
	output, _ := fakeupstream.Dig(body, "data", "output")
	return shapeOfBody(t, output)
}
