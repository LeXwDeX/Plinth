package tripo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func nativeProcessingFixture(capability generation.Capability) generation.StartRequest {
	r := rigCheckFixture()
	r.Offering.Capability = capability
	r.Offering.EstimatedCredits = 10
	r.Offering.Parameters[0].TaskOfferings = []generation.OfferingID{"tripo/tripo-v3.1-full/text-to-3d", "tripo/tripo-v3.1-full/image-to-3d"}
	d := r.TaskDependencies["source_task"]
	d.OfferingID = r.Offering.Parameters[0].TaskOfferings[0]
	endpoint, model := "models/texture", "v3.5-20260815"
	r.Offering.ID = "tripo/processing/texture"
	r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: "prompt", Type: "text", WireName: "texture_prompt.text", Required: true, MaxLength: 400}, generation.Parameter{Name: "pbr", Type: "boolean", Fixed: true, Default: json.RawMessage(`true`)}, generation.Parameter{Name: "texture_quality", Type: "select", Fixed: true, Default: json.RawMessage(`"extreme"`), Options: []generation.ParameterOption{{Value: "extreme"}}}, generation.Parameter{Name: "bake", Type: "boolean", Fixed: true, Default: json.RawMessage(`true`)}, generation.Parameter{Name: "delight", Type: "boolean", Fixed: true, Default: json.RawMessage(`true`)})
	r.Input = json.RawMessage(`{"source_task":"source","prompt":"blue coat","pbr":true,"texture_quality":"extreme","bake":true,"delight":true}`)
	if capability == generation.Rigging {
		endpoint, model = "animations/rig", "v2.5-20260210"
		r.Offering.ID = "tripo/processing/rigging"
		r.Offering.Parameters = r.Offering.Parameters[:1]
		for name, value := range map[string]string{"rig_type": "biped", "spec": "tripo", "out_format": "glb"} {
			r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: name, Type: "select", Fixed: true, Default: json.RawMessage(`"` + value + `"`), Options: []generation.ParameterOption{{Value: value}}})
		}
		r.Input = json.RawMessage(`{"source_task":"source","rig_type":"biped","spec":"tripo","out_format":"glb"}`)
	}
	if capability == generation.Animation {
		endpoint, model = "animations/retarget", ""
		r.Offering.ID = "tripo/processing/animation"
		r.Offering.Parameters = r.Offering.Parameters[:1]
		r.Offering.Parameters[0].TaskOfferings = []generation.OfferingID{"tripo/processing/rigging-v3"}
		r.Offering.Parameters[0].TaskCapabilities = []generation.Capability{generation.Rigging}
		r.Offering.Parameters[0].TaskSourceBinding = "provider"
		d.OfferingID = "tripo/processing/rigging-v3"
		d.Capability = generation.Rigging
		d.ProviderBinding = nativeRigBindingFixture(d)
		r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: "animation_action", Type: "select", WireName: "animation", Required: true, Options: []generation.ParameterOption{{Value: "preset:walk"}}}, generation.Parameter{Name: "out_format", Type: "select", Fixed: true, Default: json.RawMessage(`"glb"`), Options: []generation.ParameterOption{{Value: "glb"}}})
		for name, value := range map[string]string{"export_with_geometry": "true", "animate_in_place": "false"} {
			r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: name, Type: "boolean", Fixed: true, Default: json.RawMessage(value)})
		}
		r.Input = json.RawMessage(`{"source_task":"source","animation_action":"preset:walk","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`)
	}
	r.TaskDependencies["source_task"] = d
	one := 1
	zero := 0
	r.Offering.Outputs = []generation.AssetRequirement{{Role: "model", Variant: "glb", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}}, {Role: "thumbnail", Variant: "preview", Minimum: &zero, MediaTypes: []string{"image/png"}}}
	declared := map[string]any{"endpoint": endpoint, "observe_endpoint": "tasks/{task_id}", "model": model, "response_aliases": []generation.ResponseAlias{{Key: "model_url", Role: "model", Variant: "glb", Fields: []string{"model_url"}}, {Key: "rendered_image_url", Role: "thumbnail", Variant: "preview", Fields: []string{"rendered_image_url"}}}}
	if capability == generation.Animation {
		declared["source_rig"] = nativeSourceRig{Model: "v3.0-20260909", RigType: "biped", Spec: "tripo"}
	}
	options, _ := json.Marshal(declared)
	r.Offering.ProviderOptions = options
	return r
}

// The expected v3 wire is taken from the current Auto Rig endpoint contract,
// while the Offering itself comes from the shipped Product Schema.
func TestLatestRigCatalogSendsDocumentedNativeRequest(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/processing/rigging-v3")
	if !ok {
		t.Fatal("missing latest Rig candidate")
	}
	r := nativeProcessingFixture(generation.Rigging)
	r.Offering = offering
	r.Input = json.RawMessage(`{"source_task":"source","rig_type":"biped","spec":"tripo","out_format":"glb","output_formats":["FBX"],"authorize_extra_credits":true}`)
	permit := &rigCheckPermit{}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		posts++
		body, _ := io.ReadAll(q.Body)
		want := `{"input":"task_native","model":"v3.0-20260909","out_format":"glb","rig_type":"biped","spec":"tripo"}`
		if q.Method != http.MethodPost || q.URL.Path != "/v3/animations/rig" || string(body) != want || permit.calls != 1 {
			t.Errorf("unexpected Rig request %s %s %s checkpoint=%d", q.Method, q.URL.Path, body, permit.calls)
		}
		io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
	}))
	defer server.Close()
	accepted, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), permit), r)
	if err != nil || accepted.Job == nil || posts != 1 {
		t.Fatalf("Rig v3 create failed: observation=%+v posts=%d err=%v", accepted, posts, err)
	}
	for _, forbidden := range []string{
		`{"source_task":"source","rig_type":"others","spec":"tripo","out_format":"glb","output_formats":["FBX"],"authorize_extra_credits":true}`,
		`{"source_task":"source","rig_type":"biped","spec":"vrm","out_format":"glb","output_formats":["FBX"],"authorize_extra_credits":true}`,
		`{"source_task":"source","rig_type":"biped","spec":"tripo","out_format":"glb","output_formats":["FBX"],"authorize_extra_credits":true,"file_token":"file_unsafe"}`,
	} {
		if _, _, _, _, err := nativeProcessingInput(offering, json.RawMessage(forbidden)); err == nil {
			t.Fatalf("unqualified or extra field reached native wire: %s", forbidden)
		}
	}
}

func TestNativeProcessingExactWireRestoreAndCredits(t *testing.T) {
	for _, tc := range []struct {
		cap            generation.Capability
		endpoint, body string
	}{
		{generation.Retexture, "models/texture", `{"bake":true,"delight":true,"input":"task_native","model":"v3.5-20260815","pbr":true,"texture_prompt":{"text":"blue coat"},"texture_quality":"extreme"}`},
		{generation.Rigging, "animations/rig", `{"input":"task_native","model":"v2.5-20260210","out_format":"glb","rig_type":"biped","spec":"tripo"}`},
		{generation.Animation, "animations/retarget", `{"animate_in_place":false,"animation":"preset:walk","export_with_geometry":true,"input":"task_native","out_format":"glb"}`},
	} {
		t.Run(string(tc.cap), func(t *testing.T) {
			r := nativeProcessingFixture(tc.cap)
			p := &rigCheckPermit{}
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "POST" {
					posts++
					raw, _ := io.ReadAll(q.Body)
					if q.URL.Path != "/v3/"+tc.endpoint || string(raw) != tc.body || p.calls != 1 {
						t.Errorf("wire %s %s checkpoint %d", q.URL.Path, raw, p.calls)
					}
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
					return
				}
				gets++
				if q.Method != "GET" || q.URL.Path != "/v3/tasks/task_processed" {
					t.Error("wrong observation route")
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed","status":"success","progress":100,"credits_consumed":3.75,"output":{"model_url":"https://assets.example/result.glb","rendered_image_url":"https://assets.example/result.png"}}}`)
			}))
			defer server.Close()
			accepted, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), p), r)
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Dispatch == nil || accepted.Job == nil {
				t.Fatal("missing durable receipt/job")
			}
			for i := 0; i < 2; i++ {
				observed, err := rigAdapter(t, server).Observe(context.Background(), rigObserve(r, accepted))
				if err != nil {
					t.Fatal(err)
				}
				if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 2 || observed.Artifacts[0].Variant != "glb" || observed.Artifacts[0].MediaType != "model/gltf-binary" {
					t.Fatalf("claims %+v", observed)
				}
				if !strings.Contains(string(observed.Opaque), `"consumed_credit":3.75`) || !strings.Contains(string(observed.Opaque), `"source_remote_id":"task_native"`) {
					t.Fatalf("lost source/credits %s", observed.Opaque)
				}
			}
			if posts != 1 || gets != 2 {
				t.Fatalf("requests %d/%d", posts, gets)
			}
		})
	}
}

func TestNativeProcessingRejectsInputBeforePermit(t *testing.T) {
	for _, mutate := range []func(*generation.StartRequest){
		func(r *generation.StartRequest) { r.TaskDependencies = nil }, func(r *generation.StartRequest) { r.OwnerID = "" },
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.ProviderID = "other"
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.UpstreamJob.ProviderID = "other"
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.OfferingID = "undeclared"
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			d := r.TaskDependencies["source_task"]
			d.Capability = generation.Rigging
			r.TaskDependencies["source_task"] = d
		},
		func(r *generation.StartRequest) {
			r.Input = json.RawMessage(`{"source_task":"source","prompt":"blue","pbr":false}`)
		},
		func(r *generation.StartRequest) {
			r.Input = json.RawMessage(`{"source_task":"source","source_task":"source"}`)
		},
		func(r *generation.StartRequest) {
			r.Input = json.RawMessage(`{"source_task":"source","model":"override"}`)
		},
	} {
		r := nativeProcessingFixture(generation.Retexture)
		mutate(&r)
		p := &rigCheckPermit{}
		a := &Adapter{}
		if err := a.ValidateInput(context.Background(), generation.InputValidationRequest{OwnerID: r.OwnerID, Offering: r.Offering, Input: r.Input, TaskDependencies: r.TaskDependencies, InputAssets: r.InputAssets}); err == nil {
			t.Fatal("invalid local input accepted")
		}
		if _, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
			t.Fatalf("invalid input dispatched: %v", err)
		}
	}
}

func TestNativeProcessingRequiresPermitAndStopsAmbiguousReplay(t *testing.T) {
	for _, body := range []string{`{"code":null,"data":{"task_id":"x"}}`, `{"code":0,"data":{"task_id":"x","task_id":"y"}}`, `{"code":0,"data":{}}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; io.WriteString(w, body) }))
			defer server.Close()
			a := rigAdapter(t, server)
			r := nativeProcessingFixture(generation.Rigging)
			if _, err := a.Start(context.Background(), r); err == nil || posts != 0 {
				t.Fatal("missing permit sent")
			}
			p := &rigCheckPermit{}
			ctx := generation.WithDispatchCheckpointer(context.Background(), p)
			_, err := a.Start(ctx, r)
			var provider *generation.ProviderError
			if !errors.As(err, &provider) || !provider.Failure.Ambiguous {
				t.Fatalf("not ambiguous: %v", err)
			}
			if _, err = a.Start(ctx, r); !errors.Is(err, generation.ErrDispatchPending) || posts != 1 {
				t.Fatalf("replay %v/%d", err, posts)
			}
		})
	}
}

func TestNativeProcessingFrozenIdentityRejectsMutationBeforeGET(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			return
		}
		gets++
	}))
	defer server.Close()
	r := nativeProcessingFixture(generation.Animation)
	a := rigAdapter(t, server)
	accepted, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*generation.ObserveRequest){func(q *generation.ObserveRequest) { q.Input = json.RawMessage(`{"source_task":"other"}`) }, func(q *generation.ObserveRequest) { q.Job.RemoteID = "other" }, func(q *generation.ObserveRequest) { q.Job.ProviderID = "other" }, func(q *generation.ObserveRequest) { q.AttemptID = "other" }, func(q *generation.ObserveRequest) { q.Offering.Revision = "other" }, func(q *generation.ObserveRequest) { q.Opaque = json.RawMessage(`{"phase":"dispatching"}`) }} {
		q := rigObserve(r, accepted)
		mutate(&q)
		if _, err := a.Observe(context.Background(), q); err == nil || gets != 0 {
			t.Fatalf("mutated checkpoint polled %v", err)
		}
	}
}

func TestNativeProcessingMalformedObservationRejected(t *testing.T) {
	for _, data := range []string{
		`{"task_id":"other","status":"success"}`,
		`{"task_id":"task_processed","status":null}`,
		`{"task_id":"task_processed","status":"unknown"}`,
		`{"task_id":"task_processed","status":"success","progress":null}`,
		`{"task_id":"task_processed","status":"success","progress":101}`,
		`{"task_id":"task_processed","status":"success","progress":2.5}`,
		`{"task_id":"task_processed","status":"success","progress":20,"progress":100}`,
		`{"task_id":"task_processed","status":"success","output":[]}`,
		`{"task_id":"task_processed","status":"success","credits_consumed":-1}`,
		`{"task_id":"task_processed","status":"success","credits_consumed":"3.75"}`,
		`{"task_id":"task_processed","status":"success","credits_consumed":null}`,
	} {
		t.Run(data, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "POST" {
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
					return
				}
				io.WriteString(w, `{"code":0,"data":`+data+`}`)
			}))
			defer server.Close()
			r := nativeProcessingFixture(generation.Rigging)
			a := rigAdapter(t, server)
			accepted, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
			if err != nil {
				t.Fatal(err)
			}
			if observed, err := a.Observe(context.Background(), rigObserve(r, accepted)); err == nil || len(observed.Artifacts) > 0 {
				t.Fatalf("malformed observation accepted %+v %v", observed, err)
			}
		})
	}
}
func TestNativeProcessingUnknownPreparationNeverResubmits(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { posts++; w.WriteHeader(502) }))
	defer server.Close()
	r := nativeProcessingFixture(generation.Retexture)
	p := &rigCheckPermit{}
	a := rigAdapter(t, server)
	if _, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil {
		t.Fatal("unknown dispatch accepted")
	}
	observed, err := a.ResumePreparation(context.Background(), generation.PreparationRequest{TaskID: r.TaskID, AttemptID: r.AttemptID, SubmissionKey: r.SubmissionKey, OwnerID: r.OwnerID, Offering: r.Offering, Input: r.Input, Opaque: p.intent})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || !provider.Failure.Ambiguous || posts != 1 || observed.Job != nil {
		t.Fatalf("unknown preparation replay %+v %v posts=%d", observed, err, posts)
	}
}
func TestNativeProcessingBusinessRejectionCarriesReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		io.WriteString(w, `{"code":2001,"message":"private signed URL","data":{}}`)
	}))
	defer server.Close()
	r := nativeProcessingFixture(generation.Rigging)
	a := rigAdapter(t, server)
	observed, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
	if err != nil || observed.Phase != generation.UpstreamFailed || observed.Failure == nil || observed.Dispatch == nil || observed.Failure.Ambiguous || strings.Contains(observed.Failure.SafeDetail, "private") {
		t.Fatalf("business error lost receipt %+v %v", observed, err)
	}
}
func TestNativeProcessingAliasMediaAndCreditEvidenceAreDeclared(t *testing.T) {
	r := nativeProcessingFixture(generation.Animation)
	r.Offering.Outputs[0].Variant = "fbx"
	r.Offering.Outputs[0].MediaTypes = []string{"application/octet-stream"}
	var options providerOptions
	if err := json.Unmarshal(r.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	options.ResponseAliases[0].Variant = "fbx"
	r.Offering.ProviderOptions, _ = json.Marshal(options)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			return
		}
		calls++
		credit := `,"credits_consumed":2.50`
		if calls > 1 {
			credit = ""
		}
		io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed","status":"success"`+credit+`,"output":{"model_url":"https://assets.example/download"}}}`)
	}))
	defer server.Close()
	a := rigAdapter(t, server)
	accepted, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := a.Observe(context.Background(), rigObserve(r, accepted))
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Artifacts) != 1 || observed.Artifacts[0].Variant != "fbx" || observed.Artifacts[0].MediaType != "application/octet-stream" {
		t.Fatalf("hardcoded output %+v", observed.Artifacts)
	}
	q := rigObserve(r, accepted)
	q.Opaque = observed.Opaque
	observed, err = a.Observe(context.Background(), q)
	if err != nil || !strings.Contains(string(observed.Opaque), `"consumed_credit":2.50`) {
		t.Fatalf("credit not preserved %s %v", observed.Opaque, err)
	}
}
func TestNativeProcessingDeclarationRejectsAmbiguousPaths(t *testing.T) {
	for _, mutate := range []func(*generation.Offering){
		func(o *generation.Offering) {
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "wire_override", Type: "text", WireName: "model"})
		},
		func(o *generation.Offering) {
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "wire_override", Type: "text", WireName: "input.value"})
		},
		func(o *generation.Offering) {
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "wire_override", Type: "text", WireName: "texture_prompt"})
		},
		func(o *generation.Offering) { o.Parameters[0].SameProvider = false },
		func(o *generation.Offering) { o.Parameters[0].TaskSourceKind = schema.TaskSourceAssets },
		func(o *generation.Offering) { o.Parameters[0].TaskOfferings = nil },
		func(o *generation.Offering) { o.Parameters[0].TaskCapabilities = nil },
		func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"https://elsewhere/create","observe_endpoint":"tasks/{task_id}"}`)
		},
	} {
		o := nativeProcessingFixture(generation.Retexture).Offering
		mutate(&o)
		if err := ValidateOffering(o); err == nil {
			t.Fatal("ambiguous native declaration accepted")
		}
	}
}

func TestNativeProcessingCreditEvidenceChangesDedupeAtSameProgress(t *testing.T) {
	rounds := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			return
		}
		rounds++
		status, progress, credits := "running", 50, ""
		if rounds == 2 || rounds == 3 {
			credits = `,"credits_consumed":3.75`
		}
		if rounds == 5 {
			status, progress = "success", 100
		}
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":"task_processed","status":%q,"progress":%d%s}}`, status, progress, credits)
	}))
	defer server.Close()
	r := nativeProcessingFixture(generation.Rigging)
	a := rigAdapter(t, server)
	accepted, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	previousKey := ""
	for round := 1; round <= 5; round++ {
		observed, err := a.Observe(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if observed.CheckpointBaseSHA256 != uploadedDigest(q.Opaque) {
			t.Fatal("checkpoint update does not bind the exact received Opaque")
		}
		if round == 2 && observed.DedupeKey == previousKey {
			t.Fatal("same-progress credit update would be discarded")
		}
		if (round == 3 || round == 4) && observed.DedupeKey != previousKey {
			t.Fatal("unchanged merged credit evidence should dedupe")
		}
		if round > 1 && !strings.Contains(string(observed.Opaque), `"consumed_credit":3.75`) {
			t.Fatalf("lost credits at round %d: %s", round, observed.Opaque)
		}
		previousKey = observed.DedupeKey
		q.Opaque = observed.Opaque
	}
}
func TestNativeProcessingRejectsReplaySafeDeclarationBeforePermit(t *testing.T) {
	r := nativeProcessingFixture(generation.Rigging)
	r.Offering.IdempotentSubmission = true
	if err := ValidateOffering(r.Offering); err == nil {
		t.Fatal("native POST cannot guarantee remote idempotency")
	}
	p := &rigCheckPermit{}
	if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
		t.Fatalf("replay-safe declaration dispatched %v", err)
	}
}

// #358: a rejected scalar is refused with its public Schema label; the
// internal parameter name and cause stay in the error chain.
func TestNativeProcessingParameterRefusalKeepsInternalDiagnostic(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := nativeProcessingFixture(generation.Retexture)
	r.Offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	for i := range r.Offering.Parameters {
		if r.Offering.Parameters[i].Name == "prompt" {
			r.Offering.Parameters[i].Label = "Description"
		}
	}
	input := json.RawMessage(`{"source_task":"source","prompt":42,"pbr":true,"texture_quality":"extreme","bake":true,"delight":true}`)
	_, _, _, _, err = nativeProcessingInput(r.Offering, input)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != r.Offering.ExecutionMessages.InputParameterInvalid {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(refusal.Detail, "Description") || strings.Contains(refusal.Detail, "prompt") {
		t.Fatalf("public refusal names the internal parameter: %q", refusal.Detail)
	}
	if refusal.Cause == nil || !strings.Contains(refusal.Cause.Error(), `parameter "prompt"`) {
		t.Fatalf("internal cause lost: %v", refusal.Cause)
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		strings.Contains(provider.Failure.SafeDetail, "prompt") {
		t.Fatalf("SafeDetail names the internal parameter: %v", err)
	}
}

func TestNativeProcessingParameterRefusalWithoutLabelUsesGenericCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r := nativeProcessingFixture(generation.Retexture)
	r.Offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	for i := range r.Offering.Parameters {
		if r.Offering.Parameters[i].Name == "prompt" {
			r.Offering.Parameters[i].Label = ""
		}
	}
	input := json.RawMessage(`{"source_task":"source","prompt":42,"pbr":true,"texture_quality":"extreme","bake":true,"delight":true}`)
	_, _, _, _, err = nativeProcessingInput(r.Offering, input)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != r.Offering.ExecutionMessages.InputParametersInvalid {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(refusal.Detail, "prompt") {
		t.Fatalf("generic refusal names the internal parameter: %q", refusal.Detail)
	}
}
