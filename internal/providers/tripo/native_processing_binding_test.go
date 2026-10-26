package tripo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const testAssetSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func nativeRigBindingFixture(d generation.TaskDependency) json.RawMessage {
	raw, _ := json.Marshal(nativeRigBinding{TaskID: d.TaskID, AttemptID: "rig-attempt", RemoteID: d.UpstreamJob.RemoteID, OfferingID: d.OfferingID, OfferingRevision: "2026-10-17.1", OfferingSHA256: testAssetSHA, Model: "v3.0-20260909", RigType: "biped", Spec: "tripo"})
	return raw
}

func validation(r generation.StartRequest) generation.InputValidationRequest {
	return generation.InputValidationRequest{OwnerID: r.OwnerID, Offering: r.Offering, Input: r.Input, InputAssets: r.InputAssets, TaskDependencies: r.TaskDependencies}
}

func shippedOffering(t *testing.T, id generation.OfferingID) generation.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(id)
	if !ok {
		t.Fatalf("missing %s", id)
	}
	return o
}

// The expected body is the current Retarget contract qualified on a v3.0 Rig
// source: walk only, no Retarget model, and no deprecated bake_animation.
func TestRetargetCatalogSendsQualifiedWalkWire(t *testing.T) {
	r := nativeProcessingFixture(generation.Animation)
	r.Offering = shippedOffering(t, "tripo/processing/animation")
	r.Input = json.RawMessage(`{"source_task":"source","animation_action":"preset:walk","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`)
	permit := &rigCheckPermit{}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		posts++
		body, _ := io.ReadAll(q.Body)
		want := `{"animate_in_place":false,"animation":"preset:walk","export_with_geometry":true,"input":"task_native","out_format":"glb"}`
		if q.Method != http.MethodPost || q.URL.Path != "/v3/animations/retarget" || string(body) != want || permit.calls != 1 {
			t.Errorf("unexpected Retarget request %s %s %s checkpoint=%d", q.Method, q.URL.Path, body, permit.calls)
		}
		io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
	}))
	defer server.Close()
	accepted, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), permit), r)
	if err != nil || accepted.Job == nil || posts != 1 {
		t.Fatalf("Retarget create failed: %+v posts=%d err=%v", accepted, posts, err)
	}
	for _, forbidden := range []string{
		`{"source_task":"source","animation_action":"preset:idle","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`,
		`{"source_task":"source","animation_action":"preset:walk","out_format":"glb","bake_animation":true,"export_with_geometry":true,"animate_in_place":false}`,
	} {
		if _, _, _, _, err := nativeProcessingInput(r.Offering, json.RawMessage(forbidden)); err == nil {
			t.Fatalf("unqualified Retarget input reached wire: %s", forbidden)
		}
	}
}

// A v2.5 Rig, an unbound source or a binding whose frozen configuration does
// not match the declared v3.0 Rig is rejected before any dispatch permit.
func TestRetargetRejectsWrongRigSourceBeforeDispatch(t *testing.T) {
	for name, mutate := range map[string]func(*generation.TaskDependency){
		"v2.5 offering": func(d *generation.TaskDependency) {
			d.OfferingID = "tripo/processing/rigging"
			d.ProviderBinding = nativeRigBindingFixture(*d)
		},
		"missing binding": func(d *generation.TaskDependency) { d.ProviderBinding = nil },
		"v2.5 model": func(d *generation.TaskDependency) {
			d.ProviderBinding = json.RawMessage(strings.Replace(string(d.ProviderBinding), "v3.0-20260909", "v2.5-20260210", 1))
		},
		"other skeleton": func(d *generation.TaskDependency) {
			d.ProviderBinding = json.RawMessage(strings.Replace(string(d.ProviderBinding), `"spec":"tripo"`, `"spec":"mixamo"`, 1))
		},
		"other remote": func(d *generation.TaskDependency) { d.UpstreamJob.RemoteID = "task_other" },
		"other task":   func(d *generation.TaskDependency) { d.TaskID = "other" },
		"binding for other offering": func(d *generation.TaskDependency) {
			d.ProviderBinding = json.RawMessage(strings.Replace(string(d.ProviderBinding), "rigging-v3", "rigging", 1))
		},
		"other provider": func(d *generation.TaskDependency) { d.ProviderID = "meshy" },
		// Only the private binding drifts; the resolved dependency is unchanged.
		"binding task": func(d *generation.TaskDependency) {
			d.ProviderBinding = json.RawMessage(strings.Replace(string(d.ProviderBinding), `"task_id":"source"`, `"task_id":"elsewhere"`, 1))
		},
		"binding attempt": func(d *generation.TaskDependency) {
			d.ProviderBinding = json.RawMessage(strings.Replace(string(d.ProviderBinding), `"attempt_id":"rig-attempt"`, `"attempt_id":""`, 1))
		},
		"generation source": func(d *generation.TaskDependency) {
			d.OfferingID, d.Capability = "tripo/tripo-v3.1-full/text-to-3d", generation.TextTo3D
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := nativeProcessingFixture(generation.Animation)
			r.Offering = shippedOffering(t, "tripo/processing/animation")
			r.Input = json.RawMessage(`{"source_task":"source","animation_action":"preset:walk","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`)
			d := r.TaskDependencies["source_task"]
			mutate(&d)
			r.TaskDependencies["source_task"] = d
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts++ }))
			defer server.Close()
			a := rigAdapter(t, server)
			if err := a.ValidateInput(context.Background(), validation(r)); err == nil {
				t.Fatal("wrong Rig source validated")
			}
			permit := &rigCheckPermit{}
			if _, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), permit), r); err == nil || posts != 0 || permit.calls != 0 {
				t.Fatalf("wrong Rig source dispatched: err=%v posts=%d permits=%d", err, posts, permit.calls)
			}
		})
	}
}

func rigBindingRequest(t *testing.T, id generation.OfferingID) generation.DependencyBindingRequest {
	t.Helper()
	o := shippedOffering(t, id)
	return generation.DependencyBindingRequest{Offering: o, TaskID: "rig", AttemptID: "rig-attempt", Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "rig-native"}, Assets: []generation.Asset{
		{ID: "glb", TaskID: "rig", AttemptID: "rig-attempt", Role: "model", Variant: "glb", Required: true, Size: 10, SHA256: testAssetSHA},
		{ID: "fbx", TaskID: "rig", AttemptID: "rig-attempt", Role: "model", Variant: "fbx", Required: true, Size: 10, SHA256: testAssetSHA},
	}}
}

func TestNativeRigDependencyBindingProjectsFrozenRigConfiguration(t *testing.T) {
	a := &Adapter{}
	request := rigBindingRequest(t, "tripo/processing/rigging-v3")
	raw, err := a.ResolveDependencyBinding(request)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := generation.CanonicalDependencyBinding(raw)
	if err != nil {
		t.Fatal(err)
	}
	var binding nativeRigBinding
	if err := strictUploadedJSON(canonical, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.TaskID != "rig" || binding.AttemptID != "rig-attempt" || binding.RemoteID != "rig-native" || binding.OfferingID != "tripo/processing/rigging-v3" || binding.OfferingRevision != request.Offering.Revision || binding.OfferingSHA256 != nativeOfferingDigest(request.Offering) || binding.Model != "v3.0-20260909" || binding.RigType != "biped" || binding.Spec != "tripo" {
		t.Fatalf("unexpected binding %+v", binding)
	}

	// The projected binding is exactly what the Retarget validator accepts.
	r := nativeProcessingFixture(generation.Animation)
	r.Offering = shippedOffering(t, "tripo/processing/animation")
	r.Input = json.RawMessage(`{"source_task":"rig","animation_action":"preset:walk","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`)
	r.TaskDependencies["source_task"] = generation.TaskDependency{TaskID: "rig", OfferingID: "tripo/processing/rigging-v3", Capability: generation.Rigging, ProviderID: "tripo", UpstreamJob: request.Job, ProviderBinding: canonical}
	if err := a.ValidateInput(context.Background(), validation(r)); err != nil {
		t.Fatalf("v3 Rig binding rejected: %v", err)
	}

	// The accepted v2.5 Rig projects its own model, which Retarget refuses
	// even if a stale or widened Schema were to admit its Offering ID.
	legacy, err := a.ResolveDependencyBinding(rigBindingRequest(t, "tripo/processing/rigging"))
	if err != nil {
		t.Fatal(err)
	}
	d := r.TaskDependencies["source_task"]
	d.ProviderBinding, d.OfferingID = legacy, "tripo/processing/rigging-v3"
	r.TaskDependencies["source_task"] = d
	if err := a.ValidateInput(context.Background(), validation(r)); err == nil {
		t.Fatal("v2.5 Rig binding accepted by Retarget")
	}

	for name, mutate := range map[string]func(*generation.DependencyBindingRequest){
		"missing fbx":    func(q *generation.DependencyBindingRequest) { q.Assets = q.Assets[:1] },
		"optional asset": func(q *generation.DependencyBindingRequest) { q.Assets[1].Required = false },
		"other task":     func(q *generation.DependencyBindingRequest) { q.Assets[0].TaskID = "other" },
		"other attempt":  func(q *generation.DependencyBindingRequest) { q.Assets[1].AttemptID = "older" },
		"unset minimum": func(q *generation.DependencyBindingRequest) {
			q.Offering.Outputs = append([]generation.AssetRequirement(nil), q.Offering.Outputs...)
			q.Offering.Outputs[1].Minimum = nil
			q.Assets = q.Assets[:1]
		},
		"bad hash":        func(q *generation.DependencyBindingRequest) { q.Assets[0].SHA256 = "x" },
		"missing attempt": func(q *generation.DependencyBindingRequest) { q.AttemptID = "" },
		"other provider":  func(q *generation.DependencyBindingRequest) { q.Job.ProviderID = "meshy" },
		"unfixed skeleton": func(q *generation.DependencyBindingRequest) {
			for i := range q.Offering.Parameters {
				if q.Offering.Parameters[i].Name == "rig_type" {
					q.Offering.Parameters[i].Fixed = false
				}
			}
		},
		"no model": func(q *generation.DependencyBindingRequest) {
			var options map[string]json.RawMessage
			json.Unmarshal(q.Offering.ProviderOptions, &options)
			delete(options, "model")
			q.Offering.ProviderOptions, _ = json.Marshal(options)
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := rigBindingRequest(t, "tripo/processing/rigging-v3")
			mutate(&q)
			if raw, err := a.ResolveDependencyBinding(q); err == nil {
				t.Fatalf("incomplete Rig source bound: %s", raw)
			}
		})
	}
}

func TestNativeSourceRigDeclarationIsExclusiveToBoundRetarget(t *testing.T) {
	for name, mutate := range map[string]func(*generation.Offering){
		"binding without source rig": func(o *generation.Offering) {
			var options map[string]json.RawMessage
			json.Unmarshal(o.ProviderOptions, &options)
			delete(options, "source_rig")
			o.ProviderOptions, _ = json.Marshal(options)
		},
		"source rig without binding": func(o *generation.Offering) { o.Parameters[0].TaskSourceBinding = "" },
		"retarget model": func(o *generation.Offering) {
			var options map[string]json.RawMessage
			json.Unmarshal(o.ProviderOptions, &options)
			options["model"] = json.RawMessage(`"v3.0-20260909"`)
			o.ProviderOptions, _ = json.Marshal(options)
		},
		"empty source model": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(strings.Replace(string(o.ProviderOptions), `"v3.0-20260909"`, `""`, 1))
		},
		"widened sources": func(o *generation.Offering) {
			o.Parameters[0].TaskOfferings = append(o.Parameters[0].TaskOfferings, "tripo/processing/rigging")
		},
	} {
		t.Run(name, func(t *testing.T) {
			o := shippedOffering(t, "tripo/processing/animation")
			o.Parameters = append([]generation.Parameter(nil), o.Parameters...)
			o.Parameters[0].TaskOfferings = append([]generation.OfferingID(nil), o.Parameters[0].TaskOfferings...)
			mutate(&o)
			if _, _, err := nativeProcessingDeclaration(o); err == nil {
				t.Fatal("invalid Retarget source contract accepted")
			}
		})
	}
	if _, _, err := nativeProcessingDeclaration(shippedOffering(t, "tripo/processing/animation")); err != nil {
		t.Fatalf("shipped Retarget declaration rejected: %v", err)
	}
}
