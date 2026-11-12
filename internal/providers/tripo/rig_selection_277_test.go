package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #277: the skeleton type and naming of a Rig and the presets of a Retarget,
// exercised with the shipped Product Schema and the real adapter against the
// Tripo v3 fake. The fake's rig and retarget values are constructed from the
// official pages (see its doc.go): no real request backs them yet, so these
// tests prove the Schema, the adapter and the fake agree, not the upstream.

type rigWire struct {
	path, body string
}

// rigRecorder sees every request the adapter sends to the fake.
type rigRecorder struct {
	mu       sync.Mutex
	requests []rigWire
}

func (r *rigRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	var raw []byte
	if request.Body != nil {
		raw, _ = io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(raw))
	}
	r.mu.Lock()
	r.requests = append(r.requests, rigWire{path: request.Method + " " + request.URL.Path, body: string(raw)})
	r.mu.Unlock()
	return http.DefaultTransport.RoundTrip(request)
}

// posts returns the bodies sent to a path, oldest first.
func (r *rigRecorder) posts(path string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var bodies []string
	for _, request := range r.requests {
		if request.path == "POST "+tripov3.Prefix+"/"+path {
			bodies = append(bodies, request.body)
		}
	}
	return bodies
}

type rigStack struct {
	t        *testing.T
	server   *httptest.Server
	adapter  *Adapter
	recorder *rigRecorder
	catalog  *catalog.Catalog
	source   string
}

func newRigStack(t *testing.T) *rigStack {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	errorMapping, statusMapping := realSampleMappings(t)
	recorder := &rigRecorder{}
	adapter, err := New(Config{BaseURL: server.URL + tripov3.Prefix, APIKey: "test-key", HTTPClient: &http.Client{Transport: recorder},
		AllowHTTPAssets: true, ErrorMapping: errorMapping, StatusMapping: statusMapping})
	if err != nil {
		t.Fatal(err)
	}
	stack := &rigStack{t: t, server: server, adapter: adapter, recorder: recorder, catalog: product}
	// The model every Rig takes: a finished text-to-model task of the fake.
	stack.source = stack.createDirect("generation/text-to-model", `{"model":"v3.1-20260211","prompt":"a robot"}`)
	return stack
}

func (s *rigStack) request(method, path, body string) map[string]any {
	s.t.Helper()
	request, err := http.NewRequest(method, s.server.URL+tripov3.Prefix+"/"+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-key")
	request.Header.Set("Content-Type", "application/json")
	response, err := s.server.Client().Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		s.t.Fatal(err)
	}
	return decoded
}

// createDirect posts straight to the fake (outside the recorder) and returns the task id.
func (s *rigStack) createDirect(path, body string) string {
	s.t.Helper()
	response := s.request(http.MethodPost, path, body)
	data, _ := response["data"].(map[string]any)
	id, _ := data["task_id"].(string)
	if id == "" {
		s.t.Fatalf("POST %s: %v", path, response)
	}
	return id
}

// finish polls a fake task to its terminal status and returns the task object.
func (s *rigStack) finish(id string) map[string]any {
	s.t.Helper()
	for range 8 {
		response := s.request(http.MethodGet, "tasks/"+id, "")
		data, _ := response["data"].(map[string]any)
		if status, _ := data["status"].(string); status == "success" || status == "failed" {
			return data
		}
	}
	s.t.Fatalf("task %s never finished", id)
	return nil
}

func (s *rigStack) normalize(capability generation.Capability, id, input string) (generation.Offering, json.RawMessage) {
	s.t.Helper()
	offering, normalized, err := s.catalog.Normalize(capability, generation.OfferingID(id), json.RawMessage(input))
	if err != nil {
		s.t.Fatalf("%s %s: %v", id, input, err)
	}
	return offering, normalized
}

// rigged is a Rig Task the adapter started on the fake.
type rigged struct {
	offering   generation.Offering
	input      json.RawMessage
	job        generation.UpstreamJob
	wire       string
	fakeStatus map[string]any
}

// rig starts the shipped Rig Offering with an input and waits for the fake to finish it.
func (s *rigStack) rig(offeringID, input string) rigged {
	s.t.Helper()
	offering, normalized := s.normalize(generation.Rigging, offeringID, input)
	before := len(s.recorder.posts("animations/rig"))
	accepted, err := s.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), generation.StartRequest{
		OwnerID: "owner", TaskID: "rig-task", AttemptID: "rig-attempt", SubmissionKey: "rig-key",
		Offering: offering, Input: normalized,
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "source", OfferingID: "tripo/tripo-v3.1-full/text-to-3d", Capability: generation.TextTo3D, ProviderID: "tripo",
			UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: s.source},
		}},
	})
	if err != nil || accepted.Job == nil {
		s.t.Fatalf("%s %s: the adapter or the fake refused the Rig: %v", offeringID, input, err)
	}
	posts := s.recorder.posts("animations/rig")
	if len(posts) != before+1 {
		s.t.Fatalf("expected one Rig POST, got %v", posts)
	}
	return rigged{offering: offering, input: normalized, job: *accepted.Job, wire: posts[len(posts)-1], fakeStatus: s.finish(accepted.Job.RemoteID)}
}

// binding projects the Rig the way the kernel does for a Retarget source.
func (s *rigStack) binding(r rigged) json.RawMessage {
	s.t.Helper()
	raw, err := s.adapter.ResolveDependencyBinding(generation.DependencyBindingRequest{
		Offering: r.offering, TaskID: "rig-task", AttemptID: "rig-attempt", Job: r.job, Input: r.input,
		Assets: []generation.Asset{
			{ID: "glb", TaskID: "rig-task", AttemptID: "rig-attempt", Role: "model", Variant: "glb", Required: true, Size: 10, SHA256: testAssetSHA},
			{ID: "fbx", TaskID: "rig-task", AttemptID: "rig-attempt", Role: "model", Variant: "fbx", Required: true, Size: 10, SHA256: testAssetSHA},
		},
	})
	if err != nil {
		s.t.Fatalf("binding of %s: %v", r.input, err)
	}
	canonical, err := generation.CanonicalDependencyBinding(raw)
	if err != nil {
		s.t.Fatal(err)
	}
	return canonical
}

// retarget starts the shipped Retarget Offering on a Rig. It returns the error of
// the adapter, the number of dispatch permits it took and the body it posted ("" when none).
func (s *rigStack) retarget(r rigged, preset string) (accepted generation.Observation, permits int, wire string, err error) {
	s.t.Helper()
	offering, normalized := s.normalize(generation.Animation, "tripo/processing/animation", `{"source_task":"rig-task","animation_action":"`+preset+`"}`)
	permit := &rigCheckPermit{}
	before := len(s.recorder.posts("animations/retarget"))
	accepted, err = s.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), permit), generation.StartRequest{
		OwnerID: "owner", TaskID: "retarget-task", AttemptID: "retarget-attempt", SubmissionKey: "retarget-key",
		Offering: offering, Input: normalized,
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "rig-task", OfferingID: r.offering.ID, Capability: generation.Rigging, ProviderID: "tripo",
			UpstreamJob: r.job, ProviderBinding: s.binding(r),
		}},
	})
	posts := s.recorder.posts("animations/retarget")
	if len(posts) > before {
		wire = posts[len(posts)-1]
	}
	return accepted, permit.calls, wire, err
}

const rigV3 = "tripo/processing/rigging-v3"

func rigInput(rigType, spec string) string {
	return `{"source_task":"source","authorize_extra_credits":true,"rig_type":"` + rigType + `","spec":"` + spec + `"}`
}

func wantRigWire(source, rigType, spec string) string {
	return `{"input":"` + source + `","model":"v3.0-20260909","out_format":"glb","rig_type":"` + rigType + `","spec":"` + spec + `"}`
}

func TestRigNamingsReachTheFakeAndOnlyTripoNamingFeedsRetarget(t *testing.T) {
	stack := newRigStack(t)
	for _, spec := range []string{"tripo", "mixamo", "actorcore", "unity", "unreal", "vrm"} {
		t.Run(spec, func(t *testing.T) {
			rig := stack.rig(rigV3, rigInput("biped", spec))
			if rig.wire != wantRigWire(stack.source, "biped", spec) {
				t.Fatalf("Rig wire = %s", rig.wire)
			}
			// The fake repeats what it accepted, the way the real poll does.
			input, _ := rig.fakeStatus["input"].(map[string]any)
			if rig.fakeStatus["status"] != "success" || input["spec"] != spec || input["topology"] != "biped" {
				t.Fatalf("fake poll = %v", rig.fakeStatus)
			}
			binding := stack.binding(rig)
			if !strings.Contains(string(binding), `"spec":"`+spec+`"`) || !strings.Contains(string(binding), `"rig_type":"biped"`) {
				t.Fatalf("binding does not record the frozen choice: %s", binding)
			}
			accepted, permits, wire, err := stack.retarget(rig, "preset:walk")
			if spec != "tripo" {
				// A skeleton with another naming is refused before anything is dispatched or paid.
				if err == nil || permits != 0 || wire != "" {
					t.Fatalf("a %s skeleton was animated: err=%v permits=%d wire=%s", spec, err, permits, wire)
				}
				return
			}
			if err != nil || accepted.Job == nil || permits != 1 {
				t.Fatalf("a Tripo-named biped Rig was not animated: err=%v permits=%d", err, permits)
			}
			if finished := stack.finish(accepted.Job.RemoteID); finished["status"] != "success" {
				t.Fatalf("the fake failed the Retarget: %v", finished)
			}
		})
	}
}

func TestEveryBipedPresetAnimatesATripoNamedBipedRig(t *testing.T) {
	stack := newRigStack(t)
	rig := stack.rig(rigV3, rigInput("biped", "tripo"))
	for _, preset := range []string{"preset:idle", "preset:walk", "preset:run", "preset:dive", "preset:climb", "preset:jump", "preset:slash", "preset:shoot", "preset:hurt", "preset:fall", "preset:turn"} {
		accepted, permits, wire, err := stack.retarget(rig, preset)
		if err != nil || accepted.Job == nil || permits != 1 {
			t.Fatalf("%s: err=%v permits=%d", preset, err, permits)
		}
		want := `{"animate_in_place":false,"animation":"` + preset + `","export_with_geometry":true,"input":"` + rig.job.RemoteID + `","out_format":"glb"}`
		if wire != want {
			t.Fatalf("%s: Retarget wire = %s", preset, wire)
		}
		finished := stack.finish(accepted.Job.RemoteID)
		input, _ := finished["input"].(map[string]any)
		if finished["status"] != "success" || input["animation"] != preset {
			t.Fatalf("%s: fake poll = %v", preset, finished)
		}
	}
	// A creature preset does not fit a biped rig: refused before dispatch.
	for _, preset := range []string{"preset:quadruped:walk", "preset:hexapod:walk", "preset:aquatic:march"} {
		if _, permits, wire, err := stack.retarget(rig, preset); err == nil || permits != 0 || wire != "" {
			t.Fatalf("%s reached a biped rig: err=%v permits=%d wire=%s", preset, err, permits, wire)
		}
	}
}

func TestCreatureRigsTakeTheirOwnPresetAndNothingElse(t *testing.T) {
	stack := newRigStack(t)
	creatures := map[string]string{
		"quadruped": "preset:quadruped:walk", "hexapod": "preset:hexapod:walk", "octopod": "preset:octopod:walk",
		"serpentine": "preset:serpentine:march", "aquatic": "preset:aquatic:march",
	}
	for rigType, preset := range creatures {
		t.Run(rigType, func(t *testing.T) {
			rig := stack.rig(rigV3, rigInput(rigType, "tripo"))
			if rig.wire != wantRigWire(stack.source, rigType, "tripo") {
				t.Fatalf("Rig wire = %s", rig.wire)
			}
			if accepted, permits, _, err := stack.retarget(rig, preset); err != nil || accepted.Job == nil || permits != 1 {
				t.Fatalf("%s: err=%v permits=%d", preset, err, permits)
			} else if finished := stack.finish(accepted.Job.RemoteID); finished["status"] != "success" {
				t.Fatalf("the fake failed %s: %v", preset, finished)
			}
			for other, otherPreset := range creatures {
				if other == rigType {
					continue
				}
				if _, permits, wire, err := stack.retarget(rig, otherPreset); err == nil || permits != 0 || wire != "" {
					t.Fatalf("%s reached a %s rig: err=%v permits=%d", otherPreset, rigType, err, permits)
				}
			}
			// A biped preset does not fit a creature rig.
			if _, permits, _, err := stack.retarget(rig, "preset:walk"); err == nil || permits != 0 {
				t.Fatalf("a biped preset reached a %s rig: err=%v permits=%d", rigType, err, permits)
			}
		})
	}
	// Avian and others rig, but no preset exists for them yet.
	for _, rigType := range []string{"avian", "others"} {
		rig := stack.rig(rigV3, rigInput(rigType, "tripo"))
		for _, preset := range []string{"preset:walk", "preset:quadruped:walk"} {
			if _, permits, _, err := stack.retarget(rig, preset); err == nil || permits != 0 {
				t.Fatalf("%s rig took %s: err=%v permits=%d", rigType, preset, err, permits)
			}
		}
	}
}

// A naming has no effect on a skeleton that is not a biped: the Schema forces
// Tripo naming, so the request, the frozen input and the binding all say so.
func TestNonBipedRigsAlwaysUseTripoNaming(t *testing.T) {
	stack := newRigStack(t)
	rig := stack.rig(rigV3, rigInput("quadruped", "unreal"))
	if rig.wire != wantRigWire(stack.source, "quadruped", "tripo") {
		t.Fatalf("Rig wire = %s", rig.wire)
	}
	if !strings.Contains(string(rig.input), `"spec":"tripo"`) || !strings.Contains(string(stack.binding(rig)), `"spec":"tripo"`) {
		t.Fatalf("frozen input %s, binding %s", rig.input, stack.binding(rig))
	}
	if accepted, permits, _, err := stack.retarget(rig, "preset:quadruped:walk"); err != nil || accepted.Job == nil || permits != 1 {
		t.Fatalf("the quadruped rig was not animated: err=%v permits=%d", err, permits)
	}
}

// The v2.5 Offering rigs non-humanoid creatures too, but Retarget keeps taking
// only the exact v3.0 Offering as its source.
func TestV25RigSelectsASkeletonTypeButIsNotARetargetSource(t *testing.T) {
	stack := newRigStack(t)
	for _, rigType := range []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"} {
		rig := stack.rig("tripo/processing/rigging", `{"source_task":"source","authorize_extra_credits":true,"rig_type":"`+rigType+`"}`)
		want := `{"input":"` + stack.source + `","model":"v2.5-20260210","out_format":"glb","rig_type":"` + rigType + `","spec":"tripo"}`
		if rig.wire != want {
			t.Fatalf("%s: Rig wire = %s", rigType, rig.wire)
		}
		if input, _ := rig.fakeStatus["input"].(map[string]any); input["topology"] != rigType {
			t.Fatalf("%s: fake poll = %v", rigType, rig.fakeStatus)
		}
	}
	rig := stack.rig("tripo/processing/rigging", `{"source_task":"source","authorize_extra_credits":true,"rig_type":"quadruped"}`)
	if _, permits, wire, err := stack.retarget(rig, "preset:quadruped:walk"); err == nil || permits != 0 || wire != "" {
		t.Fatalf("a v2.5 rig was animated: err=%v permits=%d wire=%s", err, permits, wire)
	}
}

// Unqualified combinations never reach the wire: the Catalog refuses them first.
func TestRigRefusesWhatTheSchemaDoesNotDeclare(t *testing.T) {
	stack := newRigStack(t)
	for name, tc := range map[string]struct{ offering, input string }{
		"an unknown type on v3.0":   {rigV3, rigInput("humanoid", "tripo")},
		"an unknown naming on v3.0": {rigV3, rigInput("biped", "godot")},
		"others on v2.5":            {"tripo/processing/rigging", `{"source_task":"source","authorize_extra_credits":true,"rig_type":"others"}`},
		"an unknown type on v2.5":   {"tripo/processing/rigging", `{"source_task":"source","authorize_extra_credits":true,"rig_type":"humanoid"}`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, normalized, err := stack.catalog.Normalize(generation.Rigging, generation.OfferingID(tc.offering), json.RawMessage(tc.input)); err == nil {
				t.Fatalf("accepted %s: %s", tc.input, normalized)
			}
		})
	}
	// The v2.5 naming stays fixed: an engine naming is replaced by Tripo naming, not sent.
	_, normalized, err := stack.catalog.Normalize(generation.Rigging, "tripo/processing/rigging",
		json.RawMessage(`{"source_task":"source","authorize_extra_credits":true,"rig_type":"quadruped","spec":"unreal"}`))
	if err != nil || !strings.Contains(string(normalized), `"spec":"tripo"`) {
		t.Fatalf("v2.5 naming = %s, %v", normalized, err)
	}
}
