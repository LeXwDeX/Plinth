package tripo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func rigCheckFixture() generation.StartRequest {
	one := 1
	offering := generation.Offering{ID: "tripo/processing/rig-check", Revision: "v1", ProviderID: "tripo", Capability: schema.RigCheck, Retry: generation.RetryPolicy{MaxAttempts: 1}, CreditsMetered: true, Parameters: []generation.Parameter{{Name: "source_task", Type: "task", Required: true, Semantic: generation.SemanticSourceTask, WireName: "input", TaskSourceKind: schema.TaskSourceUpstreamJob, SameProvider: true, TaskCapabilities: []generation.Capability{generation.TextTo3D, generation.ImageTo3D}, TaskOfferings: []generation.OfferingID{"tripo/text", "tripo/image"}}}, Outputs: []generation.AssetRequirement{{Role: "other", Variant: "report", Minimum: &one, MaxBytes: rigcheckreport.MaxBytes, MediaTypes: []string{rigcheckreport.MediaType}}}, ProviderOptions: json.RawMessage(`{"endpoint":"animations/rig-check","observe_endpoint":"tasks/{task_id}"}`)}
	return generation.StartRequest{OwnerID: "owner", TaskID: "public", AttemptID: "attempt", SubmissionKey: "key", Offering: offering, Input: json.RawMessage(`{"source_task":"source"}`), TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "source", OfferingID: "tripo/text", Capability: generation.TextTo3D, ProviderID: "tripo", UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "task_native"}}}}
}

type rigCheckPermit struct {
	pending bool
	calls   int
	intent  json.RawMessage
}

func (p *rigCheckPermit) Checkpoint(_ context.Context, intent generation.DispatchIntent) (generation.DispatchAck, error) {
	p.calls++
	if p.pending {
		return generation.DispatchAck{}, generation.ErrDispatchPending
	}
	p.pending = true
	p.intent = append(json.RawMessage(nil), intent.Bytes...)
	return generation.DispatchAck{Sequence: 1, AckToken: "ack", Opaque: intent.Bytes}, nil
}
func rigAdapter(t *testing.T, server *httptest.Server) *Adapter {
	t.Helper()
	a, err := New(Config{BaseURL: server.URL + "/v3", APIKey: "test", HTTPClient: server.Client(), StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"success": {Phase: "completed"}, "running": {Phase: "running"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func rigObserve(request generation.StartRequest, accepted generation.Observation) generation.ObserveRequest {
	return generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: request.Offering, Input: request.Input, Job: *accepted.Job, Opaque: accepted.Opaque}
}
func TestRigCheckCapturesExactNativeWireAndReport(t *testing.T) {
	for _, output := range []string{`{"riggable":false,"rig_type":"quadruped"}`, `{"riggable":true,"rig_type":"avian","private_url":"https://secret/TOKEN"}`} {
		t.Run(output, func(t *testing.T) {
			request := rigCheckFixture()
			permit := &rigCheckPermit{}
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/v3/animations/rig-check" {
					posts++
					if permit.calls != 1 {
						t.Error("dispatch before checkpoint")
					}
					raw, _ := io.ReadAll(r.Body)
					if string(raw) != `{"input":"task_native"}` {
						t.Errorf("wire changed %s", raw)
					}
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_check"}}`)
					return
				}
				if r.Method == "GET" && r.URL.Path == "/v3/tasks/task_check" {
					gets++
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_check","status":"success","progress":100,"output":`+output+`}}`)
					return
				}
				t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			}))
			defer server.Close()
			adapter := rigAdapter(t, server)
			accepted, err := adapter.Start(generation.WithDispatchCheckpointer(context.Background(), permit), request)
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Dispatch == nil || accepted.Job == nil {
				t.Fatal("missing dispatch/remote identity")
			}
			for i := 0; i < 2; i++ { // A new adapter restores only the committed remote ID.
				adapter = rigAdapter(t, server)
				completed, err := adapter.Observe(context.Background(), rigObserve(request, accepted))
				if err != nil {
					t.Fatal(err)
				}
				if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 || completed.Artifacts[0].Locator != "" || completed.Artifacts[0].Role != "other" {
					t.Fatalf("bad completed report %+v", completed)
				}
				report, err := rigcheckreport.Decode(completed.Artifacts[0].Inline)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(completed.Artifacts[0].Inline), "TOKEN") || (output == `{"riggable":false,"rig_type":"quadruped"}` && report.Riggable) {
					t.Fatal("wire data corrupted or leaked")
				}
			}
			if posts != 1 || gets != 2 {
				t.Fatalf("unexpected HTTP count %d/%d", posts, gets)
			}
		})
	}
}
func TestRigCheckInvalidWireNeverCompletes(t *testing.T) {
	invalidOutputs := []string{`{}`, `null`, `{"riggable":null}`, `{"riggable":"false"}`, `{"riggable":false,"riggable":true}`, `{"riggable":false,"rig_type":"alien"}`, `{"riggable":false,"private":"` + strings.Repeat("x", rigcheckreport.MaxBytes) + `"}`}
	for _, riggable := range []string{"true", "false"} {
		for _, field := range []string{"", `,"rig_type":null`, `,"rig_type":""`, `,"rig_type":"unknown"`} {
			invalidOutputs = append(invalidOutputs, `{"riggable":`+riggable+field+`}`)
		}
	}
	for _, output := range invalidOutputs {
		t.Run(output[:min(80, len(output))], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_check"}}`)
					return
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_check","status":"success","output":`+output+`}}`)
			}))
			defer server.Close()
			adapter := rigAdapter(t, server)
			request := rigCheckFixture()
			accepted, err := adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), request)
			if err != nil {
				t.Fatal(err)
			}
			if observation, err := adapter.Observe(context.Background(), rigObserve(request, accepted)); err == nil || len(observation.Artifacts) > 0 {
				t.Fatalf("invalid report completed %+v %v", observation, err)
			}
		})
	}
}
func TestRigCheckRequiresPermitAndNeverReplaysUnknownDispatch(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; w.WriteHeader(502) }))
	defer server.Close()
	a := rigAdapter(t, server)
	request := rigCheckFixture()
	if _, err := a.Start(context.Background(), request); err == nil || posts != 0 {
		t.Fatal("unfenced request sent")
	}
	permit := &rigCheckPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	_, err := a.Start(ctx, request)
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || !provider.Failure.Ambiguous || posts != 1 {
		t.Fatalf("ambiguous post not stopped: %v", err)
	}
	if _, err := rigAdapter(t, server).Start(ctx, request); !errors.Is(err, generation.ErrDispatchPending) || posts != 1 {
		t.Fatalf("unknown was resubmitted: %v posts %d", err, posts)
	}
}
func TestRigCheckRejectsUnresolvedOrNonNativeSourcesBeforePermit(t *testing.T) {
	for _, mutate := range []func(*generation.StartRequest){
		func(r *generation.StartRequest) { r.TaskDependencies = nil },
		func(r *generation.StartRequest) { r.OwnerID = "" },
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.ProviderID = "other"
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.Capability = generation.FormatConversion
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.OfferingID = "tripo/uploaded"
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.UpstreamJob.RemoteID = ""
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) { r.Input = json.RawMessage(`{"source_task":"changed"}`) },
	} {
		request := rigCheckFixture()
		mutate(&request)
		permit := &rigCheckPermit{}
		a := &Adapter{}
		if _, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), permit), request); err == nil || permit.calls != 0 {
			t.Fatalf("invalid source accepted: %v", err)
		}
	}
}

func TestRigCheckDeclarationFailsClosed(t *testing.T) {
	for _, mutate := range []func(*generation.Offering){
		func(o *generation.Offering) { o.Retry.MaxAttempts = 2 },
		func(o *generation.Offering) { o.CreditsMetered = false },
		func(o *generation.Offering) { o.EstimatedCredits = 1 },
		func(o *generation.Offering) { o.Parameters[0].SameProvider = false },
		func(o *generation.Offering) { o.Parameters[0].TaskSourceKind = schema.TaskSourceAssets },
		func(o *generation.Offering) { o.Parameters[0].TaskOfferings = nil },
		func(o *generation.Offering) {
			o.Parameters[0].TaskCapabilities = []generation.Capability{generation.Rigging}
		},
		func(o *generation.Offering) { o.Outputs[0].Minimum = nil },
		func(o *generation.Offering) { o.Outputs[0].MaxBytes = rigcheckreport.MaxBytes + 1 },
		func(o *generation.Offering) { o.Outputs[0].MediaTypes = []string{"application/json"} },
		func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"animations/rig-check","observe_endpoint":"tasks/{task_id}","model":"unrelated"}`)
		},
		func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"https://secret","observe_endpoint":"tasks/{task_id}"}`)
		},
	} {
		o := rigCheckFixture().Offering
		mutate(&o)
		if err := ValidateOffering(o); err == nil {
			t.Fatal("unsafe rig check declaration accepted")
		}
	}
}
func TestRigCheckMalformedCreateResponseIsUnknownAndNotReplayed(t *testing.T) {
	for _, body := range []string{`{"code":null,"data":{"task_id":"task_check"}}`, `{"data":{"task_id":"task_check"}}`, `{"code":0,"data":{"task_id":"task_check","task_id":"second"}}`, `{"code":0,"data":{}}`} {
		t.Run(body, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; io.WriteString(w, body) }))
			defer server.Close()
			a := rigAdapter(t, server)
			permit := &rigCheckPermit{}
			ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
			_, err := a.Start(ctx, rigCheckFixture())
			var p *generation.ProviderError
			if !errors.As(err, &p) || !p.Failure.Ambiguous {
				t.Fatalf("create result not unknown: %v", err)
			}
			if _, err := a.Start(ctx, rigCheckFixture()); !errors.Is(err, generation.ErrDispatchPending) || posts != 1 {
				t.Fatalf("replayed malformed create response: %v", err)
			}
		})
	}
}
func TestRigCheckFrozenIdentityRejectsMutationBeforeObserve(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_check"}}`)
			return
		}
		gets++
	}))
	defer server.Close()
	request := rigCheckFixture()
	adapter := rigAdapter(t, server)
	accepted, err := adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*generation.ObserveRequest){
		func(r *generation.ObserveRequest) { r.Input = json.RawMessage(`{"source_task":"changed"}`) },
		func(r *generation.ObserveRequest) { r.Job.RemoteID = "other" },
		func(r *generation.ObserveRequest) { r.Job.ProviderID = "other" },
		func(r *generation.ObserveRequest) { r.AttemptID = "other" },
		func(r *generation.ObserveRequest) { r.Offering.Revision = "other" },
		func(r *generation.ObserveRequest) { r.Opaque = json.RawMessage(`{"phase":"dispatching"}`) },
	} {
		r := rigObserve(request, accepted)
		mutate(&r)
		if _, err := adapter.Observe(context.Background(), r); err == nil || gets != 0 {
			t.Fatalf("mutated checkpoint polled: %v", err)
		}
	}
}
