package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// #277: the skeleton type and naming of a Rig are the user's choice, so what a
// Retarget may animate is decided from the choice the source Task was frozen
// with (the kernel hands that input to the Provider's binding resolver). The
// shipped Offerings, the real Tripo adapter and the real kernel are used; only
// the Rig's own upstream call is a synchronous fixture.
func TestTripoRetargetFollowsTheSkeletonChoiceOfItsSource(t *testing.T) {
	ctx := context.Background()
	shipped, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("preparation reached the upstream: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	one := 1
	source := generation.Offering{ID: "tripo/tripo-v3.1-full/text-to-3d", Revision: "fixture", ProviderID: "tripo", ModelID: "native", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "source.fixture", Enabled: true, Timeout: time.Hour, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one, MaxBytes: 1 << 20}}}
	rigV3 := syntheticRig(t, shipped, "tripo/processing/rigging-v3")
	animation, _ := shipped.Get("tripo/processing/animation")
	animation.Enabled = true
	if err := tripo.ValidateOffering(animation); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "retarget.db"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)}
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
	module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: rigReportCatalog{[]generation.Offering{source, rigV3, animation}}, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "retarget-selection-fixture"})
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
	succeeded := func(offering generation.Offering, input map[string]any) generation.Task {
		raw, _ := json.Marshal(input)
		accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "owner", Capability: offering.Capability, OfferingID: offering.ID, Input: raw})
		if err != nil {
			t.Fatalf("%s: %v", offering.ID, err)
		}
		for range 4 {
			reconcile()
		}
		task, err := module.Get(ctx, "owner", accepted.TaskID)
		if err != nil || task.State != generation.TaskSucceeded {
			t.Fatalf("%s not delivered %+v %v", offering.ID, task, err)
		}
		return task
	}
	model := succeeded(source, map[string]any{})
	rig := func(rigType, spec string) generation.Task {
		return succeeded(rigV3, map[string]any{"source_task": string(model.ID), "rig_type": rigType, "spec": spec, "out_format": "glb"})
	}
	retarget := func(rigTask generation.Task, preset string) (generation.PreparedInput, error) {
		state.ensureSession(t, ctx, repository, clock, "owner", "retarget-session")
		// The fixture Catalog applies no defaults, so the frozen fields are given.
		raw, _ := json.Marshal(map[string]any{"source_task": string(rigTask.ID), "animation_action": preset, "out_format": "glb", "export_with_geometry": true, "animate_in_place": false})
		return module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "retarget-session", Capability: generation.Animation, OfferingID: animation.ID, Input: raw})
	}

	for name, tc := range map[string]struct {
		rigType, spec, preset string
		accepted              bool
	}{
		"a biped with Tripo naming takes a biped preset":   {"biped", "tripo", "preset:jump", true},
		"a quadruped takes its walk":                       {"quadruped", "tripo", "preset:quadruped:walk", true},
		"an aquatic rig takes its march":                   {"aquatic", "tripo", "preset:aquatic:march", true},
		"a quadruped is not given a biped preset":          {"quadruped", "tripo", "preset:walk", false},
		"a biped is not given a creature preset":           {"biped", "tripo", "preset:quadruped:walk", false},
		"a biped with an engine naming cannot be animated": {"biped", "unreal", "preset:walk", false},
		"a biped with another naming cannot be animated":   {"biped", "vrm", "preset:walk", false},
		"an avian rig has no preset":                       {"avian", "tripo", "preset:walk", false},
		// The shipped Catalog forces Tripo naming on a quadruped; this fixture Catalog
		// does not, and the binding still refuses what it was not frozen as.
		"a quadruped frozen with an engine naming is refused": {"quadruped", "unreal", "preset:quadruped:walk", false},
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := retarget(rig(tc.rigType, tc.spec), tc.preset)
			if !tc.accepted {
				if !generation.IsCode(err, generation.ErrInvalidInput) {
					t.Fatalf("prepared: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantType := tc.rigType
			if len(prepared.Dependencies) != 1 || !bytes.Contains(prepared.Dependencies[0].ProviderBinding, []byte(`"rig_type":"`+wantType+`"`)) || !bytes.Contains(prepared.Dependencies[0].ProviderBinding, []byte(`"spec":"tripo"`)) {
				t.Fatalf("frozen Rig binding does not record the frozen choice: %+v", prepared.Dependencies)
			}
		})
	}
}
