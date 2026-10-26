package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// retargetRigSourcePort completes a Rig fixture synchronously but keeps the
// real Tripo adapter's validation and frozen-configuration binding.
type retargetRigSourcePort struct{ *tripo.Adapter }

func (p retargetRigSourcePort) Start(_ context.Context, r generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{Phase: generation.UpstreamCompleted, Job: &generation.UpstreamJob{ProviderID: r.Offering.ProviderID, RemoteID: "task_rig_" + string(r.TaskID)}, Progress: 100, Artifacts: []generation.ArtifactClaim{{ID: string(r.AttemptID) + ":rig", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Inline: validGLB("rig " + string(r.Offering.ID))}}}, nil
}
func (retargetRigSourcePort) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("rig source is synchronous fixture")
}
func (retargetRigSourcePort) OpenAsset(_ context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(r.Claim.Inline)), generation.AssetMetadata{MediaType: r.Claim.MediaType}, nil
}

// syntheticRig keeps a shipped Rig's identity, model and fixed skeleton while
// removing the private FBX export, whose real bytes are out of scope here.
func syntheticRig(t *testing.T, c *catalog.Catalog, id generation.OfferingID) generation.Offering {
	t.Helper()
	o, ok := c.Get(id)
	if !ok {
		t.Fatal(id)
	}
	o.Enabled, o.AdapterKey, o.Cost, o.CostPolicy = true, "rig.fixture", nil, nil
	o.Parameters = slices.DeleteFunc(slices.Clone(o.Parameters), func(p generation.Parameter) bool {
		return p.Name == "output_formats" || p.Name == "authorize_extra_credits"
	})
	one, zero := 1, 0
	o.Outputs = []generation.AssetRequirement{{Role: "model", Variant: "glb", Delivery: "required", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 1 << 20}, {Role: "thumbnail", Variant: "rendered_image", Delivery: "background", Minimum: &zero}}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	delete(options, "native_conversion")
	delete(options, "convert_endpoint")
	o.ProviderOptions, _ = json.Marshal(options)
	return o
}

// Retarget must bind the exact Rig v3 Offering and its frozen configuration.
// A v2.5 Rig or another owner's v3 Rig is refused during preparation, before a
// Grant or any upstream POST; the accepted source freezes its binding and the
// Retarget wire carries only that source's native Rig ID.
func TestTripoRetargetAdmitsOnlyOwnedRigV3SourceBeforePayment(t *testing.T) {
	ctx := context.Background()
	shipped, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int64
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/animations/retarget":
			posts.Add(1)
			raw, _ := io.ReadAll(r.Body)
			body.Store(string(raw))
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_retarget"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/tasks/task_retarget":
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_retarget","status":"success","progress":100,"credits_consumed":10,"output":{"model_url":"http://`+r.Host+`/walk.glb"}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/walk.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			w.Write(fidelityFixtureGLB("animation"))
		default:
			t.Errorf("unexpected upstream request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host, _ := url.Parse(server.URL)

	one := 1
	source := generation.Offering{ID: "tripo/tripo-v3.1-full/text-to-3d", Revision: "fixture", ProviderID: "tripo", ModelID: "native", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "source.fixture", Enabled: true, Timeout: time.Hour, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one, MaxBytes: 1 << 20}}}
	rigV3 := syntheticRig(t, shipped, "tripo/processing/rigging-v3")
	rigV25 := syntheticRig(t, shipped, "tripo/processing/rigging")
	animation, _ := shipped.Get("tripo/processing/animation")
	animation.Enabled, animation.AssetHosts = true, []string{host.Hostname()}
	if err := tripo.ValidateOffering(animation); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "retarget.db"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)}
	repository.SetClock(clock.Now)
	store, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "fixture", HTTPClient: server.Client(), AllowHTTPAssets: true, StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"success": {Phase: "completed"}}}})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	for key, port := range map[string]generation.ProviderPort{source.AdapterKey: rigReportSourcePort{}, rigV3.AdapterKey: retargetRigSourcePort{adapter}, animation.AdapterKey: adapter} {
		if err := registry.Register(key, port); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: rigReportCatalog{[]generation.Offering{source, rigV3, rigV25, animation}}, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "retarget-source-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	reconcile := func() {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "retarget", LeaseFor: time.Minute, BatchSize: 4}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(6 * time.Second)
	}
	state := submitFixtureState{}
	succeeded := func(owner generation.OwnerID, offering generation.Offering, input map[string]any) generation.Task {
		raw, _ := json.Marshal(input)
		accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: owner, Capability: offering.Capability, OfferingID: offering.ID, Input: raw})
		if err != nil {
			t.Fatalf("%s: %v", offering.ID, err)
		}
		for range 4 {
			reconcile()
		}
		task, err := module.Get(ctx, owner, accepted.TaskID)
		if err != nil || task.State != generation.TaskSucceeded {
			t.Fatalf("%s not delivered %+v %v", offering.ID, task, err)
		}
		return task
	}
	rigInput := func(model generation.Task) map[string]any {
		return map[string]any{"source_task": string(model.ID), "rig_type": "biped", "spec": "tripo", "out_format": "glb"}
	}
	model := succeeded("owner", source, map[string]any{})
	v3 := succeeded("owner", rigV3, rigInput(model))
	v25 := succeeded("owner", rigV25, rigInput(model))
	foreign := succeeded("foreign", rigV3, rigInput(succeeded("foreign", source, map[string]any{})))

	retarget := func(owner generation.OwnerID, rig generation.Task) (generation.PreparedInput, error) {
		state.ensureSession(t, ctx, repository, clock, string(owner), "retarget-session:"+string(owner))
		raw, _ := json.Marshal(map[string]any{"source_task": string(rig.ID), "animation_action": "preset:walk", "out_format": "glb", "export_with_geometry": true, "animate_in_place": false})
		return module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: owner, SessionID: "retarget-session:" + string(owner), Capability: generation.Animation, OfferingID: animation.ID, Input: raw})
	}
	for name, attempt := range map[string]func() (generation.PreparedInput, error){
		"v2.5 rig":       func() (generation.PreparedInput, error) { return retarget("owner", v25) },
		"foreign v3 rig": func() (generation.PreparedInput, error) { return retarget("owner", foreign) },
		"generation":     func() (generation.PreparedInput, error) { return retarget("owner", model) },
	} {
		if _, err := attempt(); !generation.IsCode(err, generation.ErrInvalidInput) || posts.Load() != 0 {
			t.Fatalf("%s source prepared: %v posts=%d", name, err, posts.Load())
		}
	}

	prepared, err := retarget("owner", v3)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].TaskID != v3.ID || !bytes.Contains(prepared.Dependencies[0].ProviderBinding, []byte(`"model":"v3.0-20260909"`)) || !bytes.Contains(prepared.Dependencies[0].ProviderBinding, []byte(`"offering_id":"tripo/processing/rigging-v3"`)) {
		t.Fatalf("frozen Rig binding missing: %+v", prepared.Dependencies)
	}
	if posts.Load() != 0 {
		t.Fatal("preparation dispatched upstream")
	}
	accepted, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, repository, prepared, "retarget-walk"))
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		reconcile()
	}
	animated, err := module.Get(ctx, "owner", accepted.TaskID)
	if err != nil || animated.State != generation.TaskSucceeded || posts.Load() != 1 {
		t.Fatalf("retarget not delivered %+v %v posts=%d", animated, err, posts.Load())
	}
	want := `{"animate_in_place":false,"animation":"preset:walk","export_with_geometry":true,"input":"task_rig_` + string(v3.ID) + `","out_format":"glb"}`
	if got, _ := body.Load().(string); got != want {
		t.Fatalf("retarget wire = %s, want %s", got, want)
	}
}
