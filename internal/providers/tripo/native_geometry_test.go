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

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Models, endpoints and scalar constraints belong to the frozen Offering.
func geometryFixture(kind string) generation.StartRequest {
	r := nativeProcessingFixture(generation.Rigging)
	r.Offering.ID = generation.OfferingID("tripo/geometry/" + kind)
	r.Offering.Capability = generation.Capability("mesh-completion")
	r.Offering.Parameters = r.Offering.Parameters[:1]
	endpoint, model := "mesh/complete", "v1.0-20250506"
	values := map[string]any{"source_task": "source"}
	fixed := func(name string, value any) {
		raw, _ := json.Marshal(value)
		p := generation.Parameter{Name: name, Type: "boolean", Fixed: true, Default: raw}
		if text, ok := value.(string); ok {
			p.Type = "select"
			p.Options = []generation.ParameterOption{{Value: text}}
		}
		r.Offering.Parameters = append(r.Offering.Parameters, p)
		values[name] = value
	}
	switch kind {
	case "decimate-triangle", "decimate-quad":
		r.Offering.Capability = generation.Remesh
		endpoint, model = "mesh/decimate", "v3.0"
		fixed("quad", kind == "decimate-quad")
		fixed("bake", true)
		fixed("smart_uv", true)
		lo, hi := float64(500), float64(20000)
		if kind == "decimate-quad" {
			hi = 10000
		}
		r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: "face_limit", Type: "number", Integer: true, Minimum: &lo, Maximum: &hi})
		values["face_limit"] = 5000
	case "segment":
		r.Offering.Capability = generation.Capability("mesh-segmentation")
		endpoint, model = "mesh/segment", "v2.0-20260430"
		fixed("split_by_connectivity", true)
		r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: "segmentation_granularity", Type: "select", Options: []generation.ParameterOption{{Value: "detailed"}, {Value: "balanced"}, {Value: "simple"}}})
		values["segmentation_granularity"] = "detailed"
	default:
		d := r.TaskDependencies["source_task"]
		d.Capability = generation.Capability("mesh-segmentation")
		d.OfferingID = "tripo/geometry/segment"
		d.UpstreamJob.RemoteID = "task_segment"
		r.TaskDependencies["source_task"] = d
		r.Offering.Parameters[0].TaskCapabilities = []generation.Capability{d.Capability}
		r.Offering.Parameters[0].TaskOfferings = []generation.OfferingID{d.OfferingID}
		mode := "ai_completion"
		if kind == "quick-cap" {
			mode = "quick_cap"
		}
		fixed("completion_mode", mode)
	}
	var opts map[string]any
	json.Unmarshal(r.Offering.ProviderOptions, &opts)
	opts["endpoint"] = endpoint
	opts["model"] = model
	r.Offering.ProviderOptions, _ = json.Marshal(opts)
	r.Input, _ = json.Marshal(values)
	return r
}

func TestGeometryExactNativeWireAndRestoredGET(t *testing.T) {
	cases := []struct{ kind, endpoint, body string }{
		{"decimate-triangle", "mesh/decimate", `{"bake":true,"face_limit":5000,"input":"task_native","model":"v3.0","quad":false,"smart_uv":true}`},
		{"decimate-quad", "mesh/decimate", `{"bake":true,"face_limit":5000,"input":"task_native","model":"v3.0","quad":true,"smart_uv":true}`},
		{"segment", "mesh/segment", `{"input":"task_native","model":"v2.0-20260430","segmentation_granularity":"detailed","split_by_connectivity":true}`},
		{"complete", "mesh/complete", `{"completion_mode":"ai_completion","input":"task_segment","model":"v1.0-20250506"}`},
		{"quick-cap", "mesh/complete", `{"completion_mode":"quick_cap","input":"task_segment","model":"v1.0-20250506"}`},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			r := geometryFixture(tc.kind)
			p := &rigCheckPermit{}
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "POST" {
					posts++
					raw, _ := io.ReadAll(q.Body)
					if q.URL.Path != "/v3/"+tc.endpoint || string(raw) != tc.body || p.calls != 1 {
						t.Errorf("wire %s %s permits%d", q.URL.Path, raw, p.calls)
					}
					io.WriteString(w, `{"code":0,"data":{"task_id":"geometry_result"}}`)
					return
				}
				gets++
				if q.Method != "GET" || q.URL.Path != "/v3/tasks/geometry_result" {
					t.Error("wrong resumed request")
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"geometry_result","status":"success","progress":100,"credits_consumed":3.75,"output":{"model_url":"https://assets.example/result.glb"}}}`)
			}))
			defer server.Close()
			accepted, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), p), r)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := rigAdapter(t, server).Observe(context.Background(), rigObserve(r, accepted))
			if err != nil {
				t.Fatal(err)
			}
			if posts != 1 || gets != 1 || accepted.Dispatch == nil || observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 1 || observed.Artifacts[0].MediaType != "model/gltf-binary" || !strings.Contains(string(observed.Opaque), `"consumed_credit":3.75`) {
				t.Fatalf("lost native binding/credits: %+v", observed)
			}
		})
	}
}

func TestGeometryRejectsBeforePermitAndUnknownDoesNotReplay(t *testing.T) {
	for _, kind := range []string{"decimate-triangle", "decimate-quad", "segment", "complete", "quick-cap"} {
		t.Run(kind, func(t *testing.T) {
			for _, mutate := range []func(*generation.StartRequest){
				func(r *generation.StartRequest) { r.TaskDependencies = nil },
				func(r *generation.StartRequest) {
					d := r.TaskDependencies["source_task"]
					d.ProviderID = "other"
					r.TaskDependencies["source_task"] = d
				},
				func(r *generation.StartRequest) {
					d := r.TaskDependencies["source_task"]
					d.OfferingID = "unrelated"
					r.TaskDependencies["source_task"] = d
				},
				func(r *generation.StartRequest) {
					var v map[string]any
					json.Unmarshal(r.Input, &v)
					v["part_names"] = []string{"invented"}
					r.Input, _ = json.Marshal(v)
				},
			} {
				r := geometryFixture(kind)
				mutate(&r)
				p := &rigCheckPermit{}
				if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
					t.Fatalf("invalid geometry dispatch err%v calls%d", err, p.calls)
				}
			}
			r := geometryFixture(kind)
			if _, err := (&Adapter{}).Start(context.Background(), r); err == nil {
				t.Fatal("missing permit accepted")
			}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				posts++
				io.WriteString(w, `{"code":0,"data":{"task_id":"one","task_id":"two"}}`)
			}))
			defer server.Close()
			p := &rigCheckPermit{}
			_, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), p), r)
			var provider *generation.ProviderError
			if !errors.As(err, &provider) || provider.Failure.Class != generation.FailureSubmissionUnknown {
				t.Fatalf("not unknown: %v", err)
			}
			_, err = rigAdapter(t, server).ResumePreparation(context.Background(), generation.PreparationRequest{TaskID: r.TaskID, AttemptID: r.AttemptID, SubmissionKey: r.SubmissionKey, OwnerID: r.OwnerID, Offering: r.Offering, Input: r.Input, Opaque: p.intent})
			if !errors.As(err, &provider) || provider.Failure.Class != generation.FailureSubmissionUnknown || posts != 1 {
				t.Fatalf("unknown replayed %d %v", posts, err)
			}
		})
	}
}

func TestGeometryCompleteCannotDeclareGenerationAsPrerequisite(t *testing.T) {
	r := geometryFixture("complete")
	r.Offering.Parameters[0].TaskCapabilities = []generation.Capability{generation.TextTo3D}
	r.Offering.Parameters[0].TaskOfferings = []generation.OfferingID{"tripo/tripo-v3.1-full/text-to-3d"}
	if err := ValidateOffering(r.Offering); err == nil {
		t.Fatal("Complete accepted a non-Segment declaration")
	}
}

func TestGeometryBounds(t *testing.T) {
	for _, kind := range []string{"decimate-triangle", "decimate-quad"} {
		for _, value := range []any{499, 20001, 1.5, "5000"} {
			r := geometryFixture(kind)
			var v map[string]any
			json.Unmarshal(r.Input, &v)
			v["face_limit"] = value
			r.Input, _ = json.Marshal(v)
			if err := (&Adapter{}).ValidateInput(context.Background(), generation.InputValidationRequest{OwnerID: r.OwnerID, Offering: r.Offering, Input: r.Input, TaskDependencies: r.TaskDependencies}); err == nil {
				t.Fatalf("accepted %s face limit %v", kind, value)
			}
		}
	}
}

// Use the real declarations, including all mandatory exports and their costs.
// These tests prove protocol composition, not actual upstream byte qualification.
func geometryCatalogFixture(t *testing.T, kind string) generation.StartRequest {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(generation.OfferingID("tripo/geometry/" + kind))
	if !ok {
		t.Fatal("missing geometry candidate")
	}
	r := geometryFixture(kind)
	r.Offering = o
	values := map[string]json.RawMessage{"source_task": json.RawMessage(`"source"`), "authorize_extra_credits": json.RawMessage(`true`)}
	for _, p := range o.Parameters {
		if len(p.Default) > 0 {
			values[p.Name] = p.Default
		}
	}
	r.Input, _ = json.Marshal(values)
	return r
}

func TestGeometryCatalogSixRequiredFormatsAndFrozenQuote(t *testing.T) {
	for kind, total := range map[string]int{"decimate-triangle": 85, "decimate-quad": 90, "segment": 90, "complete": 100, "quick-cap": 80} {
		t.Run(kind, func(t *testing.T) {
			r := geometryCatalogFixture(t, kind)
			if r.Offering.Enabled != (kind != "decimate-quad") || r.Offering.Default {
				t.Fatal("geometry Offering enablement drifted from the #185 qualification")
			}
			if err := ValidateOffering(r.Offering); err != nil {
				t.Fatal(err)
			}
			_, _, body, options, err := nativeProcessingInput(r.Offering, r.Input)
			if err != nil {
				t.Fatal(err)
			}
			if r.Offering.EstimateTotalCredits(r.Input) != total || len(options.NativeConversion.Formats) != 5 {
				t.Fatal("incomplete quote")
			}
			if body["output_formats"] != nil || body["authorize_extra_credits"] != nil || body["part_names"] != nil || body["face_limit"] != nil {
				t.Fatalf("private control or absent optional leaked: %+v", body)
			}
			required := 0
			for _, out := range r.Offering.Outputs {
				if out.Delivery == schema.AssetDeliveryRequired && out.Minimum != nil && *out.Minimum == 1 {
					required++
				}
			}
			if required != 6 {
				t.Fatalf("required formats %d", required)
			}
			if options.NativeConversion.Parameters["quad"] != nil {
				t.Fatal("decimation quad leaked into conversion forcing FBX")
			}
			var values map[string]json.RawMessage
			json.Unmarshal(r.Input, &values)
			for _, bad := range []json.RawMessage{json.RawMessage(`false`), nil} {
				if bad == nil {
					delete(values, "authorize_extra_credits")
				} else {
					values["authorize_extra_credits"] = bad
				}
				invalid, _ := json.Marshal(values)
				r.Input = invalid
				p := &rigCheckPermit{}
				if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
					t.Fatal("base POST allowed without complete export consent")
				}
			}
		})
	}
}

func TestGeometrySixFormatsSurviveAdapterRestarts(t *testing.T) {
	for _, kind := range []string{"decimate-triangle", "decimate-quad", "segment", "complete", "quick-cap"} {
		t.Run(kind, func(t *testing.T) {
			r := geometryCatalogFixture(t, kind)
			r.Offering.AssetHosts = []string{"assets.example"}
			nativeDeliveryFiveFormatsSurviveRestarts(t, r)
		})
	}
}

func TestGeometryFrozenIdentityRejectsBeforeGET(t *testing.T) {
	for _, kind := range []string{"decimate-triangle", "decimate-quad", "segment", "complete", "quick-cap"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				calls++
				io.WriteString(w, `{"code":0,"data":{"task_id":"geometry_result"}}`)
			}))
			defer server.Close()
			r := geometryCatalogFixture(t, kind)
			accepted, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
			if err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(*generation.ObserveRequest){
				func(q *generation.ObserveRequest) { q.Input = json.RawMessage(`{"source_task":"replacement"}`) },
				func(q *generation.ObserveRequest) { q.Offering.Revision = "changed" },
				func(q *generation.ObserveRequest) { q.Job.RemoteID = "replacement" },
				func(q *generation.ObserveRequest) { q.AttemptID = "replacement" },
			} {
				q := rigObserve(r, accepted)
				mutate(&q)
				if _, err := rigAdapter(t, server).Observe(context.Background(), q); err == nil || calls != 1 {
					t.Fatal("mutated frozen identity polled")
				}
			}
		})
	}
}
