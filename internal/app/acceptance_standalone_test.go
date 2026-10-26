package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func standaloneScenario(t *testing.T, prepared generation.PreparedInput, key string) acceptanceScenario {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"id": "standalone", "owner_id": prepared.OwnerID, "session_id": prepared.SessionID,
		"offering_id": prepared.OfferingID, "offering_revision": prepared.OfferingRevision,
		"capability": prepared.Capability, "idempotency_key": key, "fault": "standalone",
		"prepared_input_id": prepared.ID, "prepared_input_hash": prepared.InputHash,
		"max_provider_starts": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var scenario acceptanceScenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	return scenario
}

func TestAcceptanceStandaloneRequiresExactFrozenScope(t *testing.T) {
	m := acceptanceManifest{OwnerID: "owner", SourceSHA: strings.Repeat("a", 40), Address: "127.0.0.1:24567", CatalogFile: "catalog", WebAssetsRoot: "web"}
	s := standaloneScenario(t, generation.PreparedInput{ID: "prepared", OwnerID: "owner", SessionID: "session", OfferingID: "tripo/geometry/smartsegment-model", OfferingRevision: "revision", Capability: "mesh-segmentation", InputHash: strings.Repeat("b", 64)}, "standalone-intent-1")
	m.Scenarios = []acceptanceScenario{s}
	if err := m.validate(); err != nil {
		t.Fatalf("bounded standalone scenario rejected: %v", err)
	}
	for field, invalid := range map[string]any{
		"prepared_input_id": "", "prepared_input_hash": "not-a-hash", "offering_revision": "",
		"max_provider_starts": 2, "idempotency_key": "short", "plan_id": strings.Repeat("a", 64), "step_index": 1,
	} {
		t.Run(field, func(t *testing.T) {
			raw, _ := json.Marshal(s)
			var changed map[string]any
			_ = json.Unmarshal(raw, &changed)
			changed[field] = invalid
			raw, _ = json.Marshal(changed)
			var bad acceptanceScenario
			_ = json.Unmarshal(raw, &bad)
			candidate := m
			candidate.Scenarios = []acceptanceScenario{bad}
			if err := candidate.validate(); err == nil {
				t.Fatal("incomplete or expanded standalone scope accepted")
			}
		})
	}
}

func TestAcceptanceStandaloneBindsInputAndKeepsOneStartAcrossRestart(t *testing.T) {
	for _, entry := range []struct {
		capability generation.Capability
		offering   generation.OfferingID
	}{
		{generation.TextTo3D, "meshy/meshy-7.1/text-to-3d"},
		{generation.TextToImage, "meshy/nano-banana-pro/text-to-image"},
	} {
		t.Run(string(entry.capability), func(t *testing.T) {
			r := newAcceptanceTestRig(t)
			prepared, err := r.module.PrepareInput(r.ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: entry.capability, OfferingID: entry.offering, Input: json.RawMessage(`{"prompt":"standalone qualification"}`)})
			if err != nil {
				t.Fatal(err)
			}
			const key = "standalone-qualification-intent"
			human := executiongrant.Principal{OwnerID: "owner", ActorID: "test-human", Source: executiongrant.HumanBrowser}
			grant, err := r.grants.Prepare(r.ctx, executiongrant.PrepareCommand{Principal: human, SessionID: "session", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: key})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.grants.Decide(r.ctx, human, grant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			accepted, err := r.module.SubmitPrepared(r.ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID, AuthorizationRef: grant.RequestID, OperationID: key, IdempotencyKey: key})
			if err != nil {
				t.Fatal(err)
			}
			task, err := r.module.Get(r.ctx, "owner", accepted.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			s := standaloneScenario(t, prepared, key)
			duplicate, err := r.module.PrepareInput(r.ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: entry.capability, OfferingID: entry.offering, Input: json.RawMessage(`{"prompt":"standalone qualification"}`)})
			if err != nil || duplicate.ID == prepared.ID || duplicate.InputHash != prepared.InputHash {
				t.Fatal("same-content preparation must have a distinct identity", err)
			}
			m := r.control.manifest
			m.Scenarios = []acceptanceScenario{s}
			write := func(scenario acceptanceScenario) {
				t.Helper()
				m.Scenarios = []acceptanceScenario{scenario}
				if err := acceptanceWriteJSON(r.control.manifestPath, m); err != nil {
					t.Fatal(err)
				}
			}
			req := generation.StartRequest{OwnerID: task.OwnerID, SessionID: task.SessionID, TaskID: task.ID, AttemptID: task.CurrentAttemptID, Offering: task.Offering, Input: schema.WireInput(task.Offering, task.Input)}
			forwarded := 0
			forward := func(context.Context, generation.StartRequest) (generation.Observation, error) {
				forwarded++
				return generation.Observation{Phase: generation.UpstreamAccepted}, nil
			}
			for field, invalid := range map[string]any{
				"prepared_input_id": string(duplicate.ID), "prepared_input_hash": strings.Repeat("c", 64),
				"offering_revision": "changed", "session_id": "another-session", "idempotency_key": "another-qualification-key",
				"offering_id": "meshy/meshy-7.1/image-to-3d", "capability": "image-to-3d",
			} {
				t.Run(field, func(t *testing.T) {
					raw, _ := json.Marshal(s)
					var changed map[string]any
					_ = json.Unmarshal(raw, &changed)
					changed[field] = invalid
					raw, _ = json.Marshal(changed)
					var bad acceptanceScenario
					_ = json.Unmarshal(raw, &bad)
					write(bad)
					if _, err := r.control.start(r.ctx, req, forward); err == nil || forwarded != 0 {
						t.Fatal("changed frozen scope reached adapter")
					}
				})
			}
			write(s)
			preparation := generation.PreparationRequest{TaskID: task.ID, AttemptID: task.CurrentAttemptID, OwnerID: task.OwnerID, Offering: task.Offering, Input: schema.WireInput(task.Offering, task.Input)}
			resumed := 0
			resume := func(context.Context, generation.PreparationRequest) (generation.Observation, error) {
				resumed++
				return generation.Observation{Phase: generation.UpstreamAccepted}, nil
			}
			if _, err := r.control.resumePreparation(r.ctx, preparation, resume); err == nil || resumed != 0 {
				t.Fatal("private preparation resumed before the registered Start")
			}
			for _, alter := range []func(*generation.StartRequest){
				func(r *generation.StartRequest) { r.OwnerID = "other" },
				func(r *generation.StartRequest) { r.SessionID = "other" },
				func(r *generation.StartRequest) { r.AttemptID = "other" },
				func(r *generation.StartRequest) { r.Input = json.RawMessage(`{"prompt":"different"}`) },
				func(r *generation.StartRequest) { r.Offering.Revision = "different" },
			} {
				changed := req
				alter(&changed)
				if _, err := r.control.start(r.ctx, changed, forward); err == nil || forwarded != 0 {
					t.Fatal("changed outbound request reached adapter")
				}
			}
			if _, err := r.control.start(r.ctx, req, forward); err != nil || forwarded != 1 {
				t.Fatalf("exact standalone request rejected: %v, forwards=%d", err, forwarded)
			}
			restarted := &acceptanceController{root: r.root, manifestPath: r.control.manifestPath, manifest: r.control.manifest, module: r.module, admissions: r.repo}
			if _, err := restarted.start(r.ctx, req, forward); err == nil || forwarded != 1 {
				t.Fatal("restart permitted another Start for consumed intent")
			}
			renamed := s
			renamed.ID = "renamed-label"
			write(renamed)
			if _, err := restarted.start(r.ctx, req, forward); err == nil || forwarded != 1 {
				t.Fatal("renaming a scenario reset the consumed intent budget")
			}
			if _, err := restarted.resumePreparation(r.ctx, preparation, resume); err != nil || resumed != 1 {
				t.Fatal("registered private preparation failed to resume", err)
			}
			for _, alter := range []func(*generation.PreparationRequest){
				func(r *generation.PreparationRequest) { r.OwnerID = "other" },
				func(r *generation.PreparationRequest) { r.Offering.Revision = "different" },
				func(r *generation.PreparationRequest) { r.Offering.Capability = generation.ImageTo3D },
				func(r *generation.PreparationRequest) { r.Input = json.RawMessage(`{"prompt":"different"}`) },
			} {
				changedPreparation := preparation
				alter(&changedPreparation)
				if _, err := restarted.resumePreparation(r.ctx, changedPreparation, resume); err == nil || resumed != 1 {
					t.Fatal("changed private preparation reached adapter")
				}
			}
			observed := 0
			observe := func(context.Context, generation.ObserveRequest) (generation.Observation, error) {
				observed++
				return generation.Observation{Phase: generation.UpstreamRunning}, nil
			}
			observation := generation.ObserveRequest{TaskID: task.ID, AttemptID: task.CurrentAttemptID, Offering: task.Offering, Input: schema.WireInput(task.Offering, task.Input)}
			if _, err := restarted.observe(r.ctx, observation, observe); err != nil || observed != 1 {
				t.Fatal("exact standalone observation rejected", err)
			}
			for _, alter := range []func(*generation.ObserveRequest){
				func(r *generation.ObserveRequest) { r.Offering.Revision = "different" },
				func(r *generation.ObserveRequest) { r.Input = nil },
				func(r *generation.ObserveRequest) { r.Input = json.RawMessage(`{"prompt":"different"}`) },
			} {
				changed := observation
				alter(&changed)
				if _, err := restarted.observe(r.ctx, changed, observe); err == nil || observed != 1 {
					t.Error("changed observation reached adapter")
				}
			}
			if entry.capability == generation.TextToImage {
				switched := renamed
				switched.Fault = "image_insufficient_quota"
				for _, maximum := range []int{1, 2} {
					switched.MaxProviderStarts = maximum
					write(switched)
					if _, err := restarted.start(r.ctx, req, forward); err == nil || forwarded != 1 {
						t.Errorf("switching fault or budget reset consumed intent: maximum=%d forwards=%d", maximum, forwarded)
					}
				}
			}
		})
	}
}

func TestAcceptanceTripoRetainsPreAdmissionAndPreparationPorts(t *testing.T) {
	adapter, err := tripo.New(tripo.Config{BaseURL: "https://example.invalid", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("tripo.v2", adapter); err != nil {
		t.Fatal(err)
	}
	wrapped, ok := (acceptanceRegistry{registry, &acceptanceController{}}).Provider(generation.Offering{AdapterKey: "tripo.v2"})
	if !ok {
		t.Fatal("Tripo adapter missing")
	}
	if _, ok := wrapped.(generation.ProviderInputValidator); !ok {
		t.Fatal("Tripo pre-admission validator lost")
	}
	if _, ok := wrapped.(generation.ProviderPreparationResumer); !ok {
		t.Fatal("Tripo preparation recovery port lost")
	}
}

// Creative Lab candidates are qualified through this isolated fixture while the
// default Catalog keeps them disabled (#189). The Keycap build freezes its
// candidate through the Meshy dependency binding, and every prototype checks
// the owned source image bytes before admission; the wrapper must keep both.
func TestAcceptanceMeshyRetainsCreativeLabAdmissionPorts(t *testing.T) {
	adapter, err := meshy.New(meshy.Config{BaseURL: "https://example.invalid", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", adapter); err != nil {
		t.Fatal(err)
	}
	wrapped, ok := (acceptanceRegistry{registry, &acceptanceController{}}).Provider(generation.Offering{AdapterKey: "meshy.v2"})
	if !ok {
		t.Fatal("Meshy adapter missing")
	}
	if _, ok := wrapped.(*acceptanceMeshy); !ok {
		t.Fatalf("Meshy dispatch is not bounded by the acceptance controller: %T", wrapped)
	}
	if _, ok := wrapped.(generation.ProviderInputValidator); !ok {
		t.Fatal("Meshy pre-admission validator lost")
	}
	if _, ok := wrapped.(generation.DependencyBindingResolver); !ok {
		t.Fatal("Meshy dependency binding resolver lost")
	}
}
