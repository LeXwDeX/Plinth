package processing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 final-review P1: a Plan step refused because another active Task already
// runs the same conversion must stay queued without a Task (no timeout) and be
// submitted again by the background once that run has ended. It must not be
// recorded as a definitive submission rejection, which would give the formats
// up for good.

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
	queued, err := service.Get(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The overlap is not a pause that waits for a person: the step stays queued
	// (no timeout) with a retry time, nothing is submitted or charged.
	if queued.State == Paused || queued.State == Failed || queued.Failure != "" ||
		queued.Steps[0].State == Paused || queued.Steps[0].State == Failed ||
		queued.Steps[0].QueuedUntil == nil || queued.Steps[0].TaskID != "" {
		t.Fatalf("overlapping conversion plan = %#v", queued)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("an overlapping conversion submitted a Task: %#v", gateway.commands)
	}
	// Before its retry time the step is not submitted again.
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}

	// Once the other conversion is gone and the retry time has come, the
	// background submits the same frozen step on its own and pays once.
	gateway.submitError = nil
	stored = store.plans[plan.ID]
	past := time.Now().UTC().Add(-time.Second)
	stored.Steps[0].QueuedUntil = &past
	store.plans[plan.ID] = stored
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	continued, err := service.Get(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if continued.State != Running || continued.Steps[0].TaskID == "" {
		t.Fatalf("retried plan = %#v", continued)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("retry submitted %d tasks, want 1", len(gateway.commands))
	}
}
