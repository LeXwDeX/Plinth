package meshy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type sourceResultTransport func(*http.Request) (*http.Response, error)

func (f sourceResultTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func sourceResultOffering() generation.Offering {
	return generation.Offering{ProviderID: "meshy", ProviderOptions: json.RawMessage(`{"endpoint":"keycap/v1/build","api_version":"creative-lab","source_result_bindings":[{"source_parameter":"source","endpoint":"keycap/v1/prototype","api_version":"creative-lab","task_type":"creative-lab-keycap-prototype","value_field":"candidate_ids","paired_field":"image_urls","wire_name":"candidate_id"}],"result_assets":[{"path":"model_urls","mode":"map","role":"model"}]}`),
		Parameters: []generation.Parameter{{Name: "source", Type: "task", WireName: "input_task_id", Required: true, SameProvider: true, TaskSourceBinding: "provider", TaskOfferings: []schema.OfferingID{"meshy/keycap/prototype"}}}}
}

func TestSourceResultBindsOnlyUniqueSuccessfulPrototype(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"unique", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["candidate-1"],"image_urls":["https://example.invalid/preview.png"]}`, true},
		{"opaque-punctuation", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["candidate:opaque/3"],"image_urls":["https://example.invalid/preview.png"]}`, true},
		{"multiple", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["a","b"],"image_urls":["a","b"]}`, false},
		{"missing-preview", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["a"]}`, false},
		{"mispaired", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["a"],"image_urls":["a","b"]}`, false},
		{"wrong-id", `{"id":"different","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["a"],"image_urls":["a"]}`, false},
		{"wrong-template", `{"id":"prototype-1","type":"creative-lab-figure-prototype","status":"SUCCEEDED","candidate_ids":["a"],"image_urls":["a"]}`, false},
		{"missing-type", `{"id":"prototype-1","status":"SUCCEEDED","candidate_ids":["a"],"image_urls":["a"]}`, false},
		{"not-completed", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"RUNNING","candidate_ids":["a"],"image_urls":["a"]}`, false},
		{"blank-candidate", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":[""],"image_urls":["a"]}`, false},
		{"bad-candidate-type", `{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":[3],"image_urls":["a"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &Adapter{statusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}, "running": {Phase: "running"}}}}
			var response taskResponse
			if err := json.Unmarshal([]byte(tc.response), &response); err != nil {
				t.Fatal(err)
			}
			source, asset := bindingPrototypeFixture()
			observation, err := adapter.observation(generation.ObserveRequest{TaskID: "public-task", AttemptID: "source-attempt", Offering: source, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}}, response)
			var binding json.RawMessage
			if len(observation.Artifacts) == 1 {
				observation.Artifacts[0].StoredAsset = &asset
			}
			if err == nil {
				binding, err = adapter.ResolveDependencyBinding(generation.DependencyBindingRequest{TaskID: "public-task", AttemptID: "source-attempt", Offering: source, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}, Claims: observation.Artifacts, Assets: []generation.Asset{asset}})
			}
			if (err == nil) != tc.valid {
				t.Fatalf("capture/project: %v", err)
			}
			if !tc.valid {
				return
			}
			offering := sourceResultOffering()
			if err := ValidateOffering(offering); err != nil {
				t.Fatal(err)
			}
			request := generation.StartRequest{Offering: offering, TaskDependencies: map[string]generation.TaskDependency{"source": {TaskID: "public-task", ProviderID: "meshy", OfferingID: source.ID, UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}, ProviderBinding: binding, Assets: []generation.Asset{asset}}}}
			// No HTTP client exists: restored binding must never choose via GET.
			body := map[string]any{"source": "public-task"}
			if err := adapter.projectInput(context.Background(), request, body); err != nil {
				t.Fatal(err)
			}
			var returned struct {
				CandidateIDs []string `json:"candidate_ids"`
			}
			_ = json.Unmarshal([]byte(tc.response), &returned)
			if body["candidate_id"] != returned.CandidateIDs[0] || body["input_task_id"] != "prototype-1" {
				t.Fatal("frozen wire identity differs")
			}

		})
	}
}

func TestSourceResultRejectsMissingForeignOrInjectedSourceBeforeRequest(t *testing.T) {
	offering := sourceResultOffering()
	for _, tc := range []struct {
		name, provider, offering, remote string
		inject                           bool
	}{
		{"missing", "", "", "", false},
		{"foreign-provider", "tripo", "meshy/keycap/prototype", "prototype-1", false},
		{"wrong-offering", "meshy", "meshy/figure/prototype", "prototype-1", false},
		{"unsafe-id", "meshy", "meshy/keycap/prototype", "../another", false},
		{"injected-candidate", "meshy", "meshy/keycap/prototype", "prototype-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"source": "task"}
			if tc.inject {
				body["candidate_id"] = "injected"
			}
			request := generation.StartRequest{Offering: offering, TaskDependencies: map[string]generation.TaskDependency{"source": {ProviderID: tc.provider, OfferingID: schema.OfferingID(tc.offering), UpstreamJob: generation.UpstreamJob{ProviderID: tc.provider, RemoteID: tc.remote}}}}
			// A zero-value adapter deliberately has no HTTP client.
			if err := (&Adapter{}).projectInput(context.Background(), request, body); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestSourceResultDeclarationRequiresBoundSource(t *testing.T) {
	for _, mutate := range []func(*generation.Offering){
		func(o *generation.Offering) { o.Parameters[0].SameProvider = false },
		func(o *generation.Offering) { o.Parameters[0].Required = false },
		func(o *generation.Offering) { o.Parameters[0].TaskOfferings = nil },
		func(o *generation.Offering) {
			o.Parameters[0].TaskOfferings = append(o.Parameters[0].TaskOfferings, "another")
		},
		func(o *generation.Offering) { o.Parameters[0].TaskSourceKind = "assets" },
		func(o *generation.Offering) {
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "candidate_id", Type: "text"})
		},
	} {
		offering := sourceResultOffering()
		mutate(&offering)
		if err := ValidateOffering(offering); err == nil {
			t.Fatal("invalid source declaration accepted")
		}
	}
}

func TestSourceResultValidationOnlyStillRejectsAmbiguousPrototype(t *testing.T) {
	for _, count := range []int{1, 2} {
		o := sourceResultOffering()
		var options wireOptions
		if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		o.Parameters[0].TaskSourceBinding = ""
		options.SourceResults[0].ValueField = ""
		options.SourceResults[0].WireName = ""
		o.ProviderOptions, _ = json.Marshal(options)
		if err := ValidateOffering(o); err != nil {
			t.Fatal(err)
		}
		images := []string{"https://example.invalid/one.png"}
		if count == 2 {
			images = append(images, "https://example.invalid/two.png")
		}
		payload, _ := json.Marshal(map[string]any{"id": "prototype-1", "type": "creative-lab-keycap-prototype", "status": "SUCCEEDED", "image_urls": images})
		a, err := New(Config{BaseURL: "https://meshy.invalid", APIKey: "fixture-key", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: sourceResultTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Header: http.Header{}}, nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"source": "public-task"}
		err = a.projectInput(context.Background(), generation.StartRequest{Offering: o, TaskDependencies: map[string]generation.TaskDependency{"source": {ProviderID: "meshy", OfferingID: "meshy/keycap/prototype", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}}}}, body)
		if (err == nil) != (count == 1) {
			t.Fatalf("images=%d err=%v", count, err)
		}
		if len(body) != 1 || body["input_task_id"] != "prototype-1" {
			t.Fatalf("validation-only lookup added private data: %#v", body)
		}
	}
}

func TestCreativeLabObservationChecksDeclaredTaskIdentityBeforeStatus(t *testing.T) {
	a := &Adapter{statusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"running": {Phase: "running"}}}}
	o := generation.Offering{ProviderOptions: json.RawMessage(`{"task_type":"creative-lab-keychain-prototype"}`)}
	for _, tc := range []struct {
		id, kind string
		valid    bool
	}{{"remote", "creative-lab-keychain-prototype", true}, {"other", "creative-lab-keychain-prototype", false}, {"remote", "creative-lab-lamp-prototype", false}, {"remote", "", false}} {
		var response taskResponse
		payload, _ := json.Marshal(map[string]any{"id": tc.id, "type": tc.kind, "status": "RUNNING"})
		if err := json.Unmarshal(payload, &response); err != nil {
			t.Fatal(err)
		}
		_, err := a.observation(generation.ObserveRequest{Offering: o, Job: generation.UpstreamJob{RemoteID: "remote"}}, response)
		if (err == nil) != tc.valid {
			t.Fatalf("id=%s kind=%s err=%v", tc.id, tc.kind, err)
		}
	}
	// Historical snapshots do not gain a new task-type requirement.
	_, err := a.observation(generation.ObserveRequest{Offering: generation.Offering{ProviderOptions: json.RawMessage(`{}`)}, Job: generation.UpstreamJob{RemoteID: "remote"}}, taskResponse{Status: "RUNNING"})
	if err != nil {
		t.Fatal(err)
	}
}
