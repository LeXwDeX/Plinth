package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #326 P: a plan paused for the person's consent froze its input while the
// source's original Meshy task was fresh. Days later that task is gone, so the
// frozen input is certain to fail. Before a paid continue is offered the
// paused step is judged by the same rule as a failed step's retry.
func pausedRetexturePlan(t *testing.T, withFileRoute bool) (*Service, *gatewayFake, *memoryStore, Plan) {
	t.Helper()
	ctx := context.Background()
	parameters := []generation.Parameter{{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, SameProvider: true,
		TaskCapabilities: []generation.Capability{"multi-image-to-3d"}, UpstreamJobMaxAgeSeconds: 2 * 24 * 60 * 60}}
	if withFileRoute {
		parameters = append(parameters, generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MaxBytes: 62914560})
	}
	catalog := memoryCatalog{"retexture": {ID: "retexture", Revision: "v1", ProviderID: "meshy", Capability: "retexture",
		Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 15, Parameters: parameters}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "multi-image-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, CreatedAt: time.Now().UTC().Add(-time.Hour),
		Assets: []generation.Asset{{ID: "model", Role: "model", Variant: "glb", StorageKey: "ready", Size: 14_944_872, Required: true}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "paused-plan-key-0001", Steps: []StepRequest{{OfferingID: "retexture", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	paused := store.plans[plan.ID]
	if paused.State != Paused || paused.Failure != ReasonConsentRequired || paused.Steps[0].PreparedInputID == "" {
		t.Fatalf("fixture plan = %#v", paused)
	}
	if frozen := frozenInput(t, gateway, paused.Steps[0]); frozen["source_task"] != "source" {
		t.Fatalf("fixture froze %v; want the original task", frozen)
	}
	// Days later Meshy no longer keeps the original task (#326 N1: the step
	// that named it failed with 404, so the job is marked gone).
	source.UpstreamJobGone = true
	gateway.tasks["source"] = source
	return service, gateway, store, paused
}

func frozenInput(t *testing.T, gateway *gatewayFake, step Step) map[string]string {
	t.Helper()
	prepared, ok := gateway.prepared[step.PreparedInputID]
	if !ok {
		t.Fatalf("no prepared input %q", step.PreparedInputID)
	}
	var input map[string]string
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestAPausedPlanCertainToFailOffersNoPaidContinueAndNoLongerHoldsTheModel(t *testing.T) {
	ctx := context.Background()
	service, gateway, store, paused := pausedRetexturePlan(t, false)
	shown, err := service.Get(ctx, "owner", paused.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Shown as a finished failure with the reason: nothing to continue, and
	// the panel and the format coverage no longer treat it as active.
	if shown.State != Failed || shown.Failure != ReasonResumeSourceExpired || shown.Steps[0].State != Failed ||
		shown.Steps[0].Failure != ReasonResumeSourceExpired || shown.Steps[0].Resumable || shown.Steps[0].Retryable {
		t.Fatalf("shown plan = %#v", shown)
	}
	for name, list := range map[string]func() ([]Plan, error){
		"List":         func() ([]Plan, error) { return service.List(ctx, "owner", "session") },
		"ListBySource": func() ([]Plan, error) { return service.ListBySource(ctx, "owner", "session", "source") },
	} {
		listed, err := list()
		if err != nil || len(listed) != 1 || listed[0].State != Failed || listed[0].Failure != ReasonResumeSourceExpired {
			t.Fatalf("%s = %#v, %v", name, listed, err)
		}
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: paused.ID, ExpectedPlanVersion: paused.Version,
		ExpectedOfferingRevision: "v1", AuthorizationRef: "test-human-approved"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume = %v; want ErrConflict", err)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: paused.ID, ExpectedPlanVersion: paused.Version,
		ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry = %v; want ErrConflict", err)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("submitted %d tasks", len(gateway.commands))
	}
	// The stored plan is not rewritten: the judgment is today's.
	if stored := store.plans[paused.ID]; stored.State != Paused || stored.Version != paused.Version {
		t.Fatalf("stored plan = %#v", stored)
	}
	// A shown ID still names a paused step to the grant route, but nothing
	// can run it: Resume refuses whatever grant comes with it.
	if _, err := service.ExecutionGrantIdentity(ctx, "owner", paused.ID, string(paused.Steps[0].PreparedInputID), paused.Steps[0].PlanStepID); err != nil {
		t.Fatalf("grant identity = %v", err)
	}
}

func TestAPausedPlanWhoseFrozenInputNamesAGoneTaskIsFrozenAgainOnTheSavedFile(t *testing.T) {
	ctx := context.Background()
	service, gateway, store, paused := pausedRetexturePlan(t, true)
	oldKey, oldPrepared := paused.Steps[0].IdempotencyKey, paused.Steps[0].PreparedInputID
	shown, err := service.Get(ctx, "owner", paused.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != Paused || shown.Failure != ReasonResumeSavedFile || shown.Steps[0].Failure != ReasonResumeSavedFile ||
		!shown.Steps[0].Resumable || shown.Version != paused.Version {
		t.Fatalf("shown plan = %#v; want a continue that freezes the saved file", shown)
	}
	// A browser that approved a grant for the old frozen input before the
	// judgment changed still cannot run it: the step is frozen again.
	resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: paused.ID, ExpectedPlanVersion: paused.Version,
		ExpectedOfferingRevision: "v1", AuthorizationRef: "test-human-approved"})
	if err != nil {
		t.Fatal(err)
	}
	step := resumed.Steps[0]
	if resumed.State != Pending || step.State != Pending || step.PreparedInputID != "" || step.AuthorizationRef != "" ||
		step.IdempotencyKey != "" || step.Retry != 1 || step.EstimatedCredits != 15 {
		t.Fatalf("resumed plan = %#v", resumed)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("submitted %d tasks before the new input was confirmed", len(gateway.commands))
	}
	waiting := store.plans[paused.ID]
	if waiting.State != Paused || waiting.Failure != ReasonConsentRequired || waiting.Steps[0].PreparedInputID == oldPrepared ||
		waiting.Steps[0].IdempotencyKey == oldKey {
		t.Fatalf("plan after freezing again = %#v", waiting)
	}
	if frozen := frozenInput(t, gateway, waiting.Steps[0]); frozen["source_asset"] != "model" || frozen["source_task"] != "" {
		t.Fatalf("new frozen input = %v; want the saved file", frozen)
	}
	// The new frozen input is an ordinary consent step now.
	if shown, _ := service.Get(ctx, "owner", paused.ID); shown.Failure != ReasonConsentRequired {
		t.Fatalf("shown after freezing again = %#v", shown)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	var input map[string]string
	if len(gateway.commands) != 1 || json.Unmarshal(gateway.commands[0].Input, &input) != nil || input["source_asset"] != "model" || input["source_task"] != "" {
		t.Fatalf("submitted %#v; want one step on the saved file", gateway.commands)
	}
}

func TestAPausedPlanOnAFreshSourceIsUnchanged(t *testing.T) {
	ctx := context.Background()
	service, gateway, _, paused := pausedRetexturePlan(t, true)
	source := gateway.tasks["source"]
	source.UpstreamJobGone = false
	gateway.tasks["source"] = source
	shown, _ := service.Get(ctx, "owner", paused.ID)
	if shown.State != Paused || shown.Failure != ReasonConsentRequired || !shown.Steps[0].Resumable {
		t.Fatalf("shown plan = %#v; want it unchanged", shown)
	}
}

// A step whose frozen input an approved grant already holds keeps that input
// and key (a new key could admit a second paid Task, see Resume); when that
// input names a gone task the continue is certain to fail and is not offered.
func TestAPausedStepHoldingAnApprovedGrantOnAGoneTaskOffersNoContinue(t *testing.T) {
	ctx := context.Background()
	service, gateway, store, paused := pausedRetexturePlan(t, true)
	held := store.plans[paused.ID]
	held.Steps[0].AuthorizationRef = "approved-earlier"
	held.Steps[0].Failure, held.Failure = ReasonOfferingUnavailable, ReasonOfferingUnavailable
	store.plans[paused.ID] = held
	shown, _ := service.Get(ctx, "owner", paused.ID)
	if shown.State != Failed || shown.Failure != ReasonResumeFrozenExpired || shown.Steps[0].Resumable {
		t.Fatalf("shown plan = %#v", shown)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: paused.ID, ExpectedPlanVersion: held.Version,
		ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume = %v; want ErrConflict", err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("submitted %d tasks", len(gateway.commands))
	}
}

func TestAPausedPlanWhoseFrozenInputCannotBeReadIsStillJudged(t *testing.T) {
	ctx := context.Background()
	for _, withFileRoute := range []bool{false, true} {
		service, gateway, _, paused := pausedRetexturePlan(t, withFileRoute)
		delete(gateway.prepared, paused.Steps[0].PreparedInputID)
		shown, _ := service.Get(ctx, "owner", paused.ID)
		if !withFileRoute && (shown.State != Failed || shown.Failure != ReasonResumeSourceExpired) {
			t.Fatalf("no file route: shown plan = %#v; want no continue", shown)
		}
		// Nothing is frozen again on a guess when the frozen input is unknown.
		if withFileRoute && (shown.State != Paused || shown.Failure != ReasonConsentRequired) {
			t.Fatalf("file route: shown plan = %#v; want it unchanged", shown)
		}
	}
}
