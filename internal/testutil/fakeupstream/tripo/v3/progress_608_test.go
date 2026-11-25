package tripov3

import (
	"embed"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #608: one geometry-only v3.0 text to model task polled every 2 seconds on
// 2026-11-25 (10 credits). It is evidence, not a template (not in sampleFS).
//
//go:embed testdata/evidence-608-progress.candidate.json
var evidence608FS embed.FS

type pollState struct {
	Status   string
	Progress string
	Output   []string // output keys; nil when the poll has no output key
}

func pollStateOf(t *testing.T, data map[string]any) pollState {
	t.Helper()
	state := pollState{Status: data["status"].(string)}
	if progress, ok := data["progress"]; ok {
		state.Progress = jsonText(t, progress)
	}
	if output, ok := data["output"].(map[string]any); ok {
		state.Output = []string{}
		for key := range output {
			state.Output = append(state.Output, key)
		}
		sort.Strings(state.Output)
	}
	return state
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTextTaskPollsFollowTheRealSequence(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(evidence608FS, "testdata/evidence-608-progress.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var real []pollState
	var realBodies []map[string]any
	for _, exchange := range samples.Exchanges {
		if exchange.Request.Method != http.MethodGet {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		data := body["data"].(map[string]any)
		real = append(real, pollStateOf(t, data))
		realBodies = append(realBodies, body)
	}
	want := []pollState{
		{"queued", "0", nil},
		{"running", "15", []string{}},
		{"running", "23", []string{"generated_image_url"}},
		{"running", "30", []string{"generated_image_url"}},
		{"running", "38", []string{"generated_image_url"}},
		{"running", "46", []string{"generated_image_url"}},
		{"running", "58", []string{"generated_image_url"}},
		{"running", "65", []string{"generated_image_url"}},
		{"success", "100", []string{"generated_image_url", "model_url", "rendered_image_url"}},
	}
	if !reflect.DeepEqual(real, want) {
		t.Fatalf("the evidence reads %v", real)
	}

	// The fake, configured with the real number of queued and running polls,
	// answers the same sequence, and each poll has the real poll's structure.
	f := newFixture(t, fakeupstream.Config{QueuedPolls: 1, RunningPolls: 7})
	id := f.create("/generation/text-to-model", map[string]any{
		"model": "v3.0-20250812", "prompt": "a plain low poly humanoid robot", "texture": false, "pbr": false, "face_limit": 10000,
	}, "test-key")
	for i, wantState := range want {
		status, _, body := f.do(http.MethodGet, "/tasks/"+id, "test-key", nil)
		if status != http.StatusOK {
			t.Fatalf("poll %d = %d", i, status)
		}
		got := pollStateOf(t, body["data"].(map[string]any))
		if !reflect.DeepEqual(got, wantState) {
			t.Errorf("poll %d: fake %v, real %v", i, got, wantState)
		}
		realShape, _ := fakeupstream.ShapeOf([]byte(jsonText(t, realBodies[i])))
		fakeShape, _ := fakeupstream.ShapeOf([]byte(jsonText(t, body)))
		if diff := fakeupstream.DiffShape(realShape, fakeShape); !diff.Empty() {
			t.Errorf("poll %d drifted from the real poll: %s", i, diff)
		}
		// The running preview picture is served.
		if url, ok := fakeupstream.Dig(body, "data", "output", "generated_image_url"); ok && got.Status == "running" {
			if code, _, _ := f.download(url.(string)); code != http.StatusOK {
				t.Errorf("poll %d: generated_image_url answers %d", i, code)
			}
		}
	}

	// More running polls than the real task had repeat its last progress.
	f = newFixture(t, fakeupstream.Config{QueuedPolls: 1, RunningPolls: 9})
	id = f.create("/generation/text-to-model", map[string]any{"model": "v3.1-20260211", "prompt": "a cube", "texture": false}, "test-key")
	var progress []string
	for i := 0; i < 10; i++ {
		_, _, body := f.do(http.MethodGet, "/tasks/"+id, "test-key", nil)
		progress = append(progress, jsonText(t, body["data"].(map[string]any)["progress"]))
	}
	if wantProgress := []string{"0", "15", "23", "30", "38", "46", "58", "65", "65", "65"}; !reflect.DeepEqual(progress, wantProgress) {
		t.Errorf("progress %v, want %v", progress, wantProgress)
	}
}
