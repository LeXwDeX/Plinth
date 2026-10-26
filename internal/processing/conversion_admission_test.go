package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 final-review P1: a Plan step refused because another active Task already
// runs the same conversion must pause without a Task and be resumable. It must
// not be recorded as a definitive submission rejection, which would give the
// formats up for good.

func TestPlanStepPausesOnAnOverlappingActiveConversion330(t *testing.T) {
	ctx := context.Background()
	catalog := memoryCatalog{
		"remesh": {ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh",
			Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 5,
			Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}},
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d",
		State: generation.TaskSucceeded, Offering: generation.Offering{ProviderID: "meshy"},
		Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Create(ctx, CreateCommand{
		OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "conversion-admission-plan",
		Steps:          []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pretend a human already approved this frozen step, so advance submits it.
	stored := store.plans[plan.ID]
	stored.Steps[0].AuthorizationRef = "test-human-approved"
	store.plans[plan.ID] = stored

	gateway.submitError = &generation.Error{Code: generation.ErrConflict, Cause: generation.ErrConversionInProgress}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err := service.Get(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != Paused || paused.Failure != ReasonPrepareUnavailable ||
		paused.Steps[0].State != Paused || paused.Steps[0].Failure != ReasonPrepareUnavailable ||
		!paused.Steps[0].Resumable || paused.Steps[0].TaskID != "" {
		t.Fatalf("overlapping conversion plan = %#v", paused)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("an overlapping conversion submitted a Task: %#v", gateway.commands)
	}

	// Once the other conversion is gone, an explicit Resume continues the same
	// frozen step and pays once.
	gateway.submitError = nil
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	continued, err := service.Get(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if continued.State != Running || continued.Steps[0].TaskID == "" {
		t.Fatalf("resumed plan = %#v", continued)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("resume submitted %d tasks, want 1", len(gateway.commands))
	}
}
