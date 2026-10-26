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

// #326: a plan that failed before #325 is FAILED with plan_task_failed and a
// retryable step: its upstream refused the original task (404, class
// rejected). A retry prepares the step again under today's rules, so the
// offer of a paid retry is judged again before it is shown and before it runs.
func failedOldPlan(t *testing.T, withFileRoute bool) (*Service, *gatewayFake, Plan) {
	t.Helper()
	ctx := context.Background()
	parameters := []generation.Parameter{{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, SameProvider: true,
		TaskCapabilities: []generation.Capability{"text-to-3d"}, UpstreamJobMaxAgeSeconds: 2 * 24 * 60 * 60}}
	if withFileRoute {
		parameters = append(parameters, generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MaxBytes: 62914560})
	}
	catalog := memoryCatalog{"retexture": {ID: "retexture", Revision: "v1", ProviderID: "meshy", Capability: "retexture",
		Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 15, Parameters: parameters}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, CreatedAt: time.Now().UTC().Add(-time.Hour),
		Assets: []generation.Asset{{ID: "model", Role: "model", Variant: "glb", StorageKey: "ready", Size: 13_564_852, Required: true}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "old-failed-plan-key", Steps: []StepRequest{{OfferingID: "retexture", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	// The upstream refused the original task: the failure an old plan kept.
	task := gateway.tasks["step-1"]
	task.State, task.Input = generation.TaskFailed, gateway.commands[0].Input
	task.Failure = &generation.Failure{Class: "rejected", Retryable: false}
	gateway.tasks[task.ID] = task
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, "owner", plan.ID)
	if failed.Failure != ReasonTaskFailed || !failed.Steps[0].Retryable {
		t.Fatalf("fixture plan = %#v", failed)
	}
	// Days later the original task is past Meshy's retention.
	source.CreatedAt = time.Now().UTC().Add(-9 * 24 * time.Hour)
	gateway.tasks["source"] = source
	return service, gateway, failed
}

func TestAnOldFailedPlanThatCannotSucceedIsNotOfferedAPaidRetry(t *testing.T) {
	ctx := context.Background()
	service, gateway, failed := failedOldPlan(t, false)
	shown, err := service.Get(ctx, "owner", failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Steps[0].Retryable || shown.Failure != ReasonRetrySourceExpired || shown.Steps[0].Failure != ReasonRetrySourceExpired {
		t.Fatalf("shown plan = %#v; want no retry and the reason", shown)
	}
	listed, _ := service.List(ctx, "owner", "session")
	if len(listed) != 1 || listed[0].Steps[0].Retryable || listed[0].Failure != ReasonRetrySourceExpired {
		t.Fatalf("listed plan = %#v", listed)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: failed.ID, ExpectedPlanVersion: failed.Version,
		ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry = %v; want ErrConflict", err)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("submitted %d tasks", len(gateway.commands))
	}
}

func TestAnOldFailedPlanThatNowUsesTheSavedFileSaysSo(t *testing.T) {
	ctx := context.Background()
	service, gateway, failed := failedOldPlan(t, true)
	shown, _ := service.Get(ctx, "owner", failed.ID)
	if !shown.Steps[0].Retryable || shown.Failure != ReasonRetrySavedFile || shown.Steps[0].Failure != ReasonRetrySavedFile {
		t.Fatalf("shown plan = %#v; want a retry with the new reason", shown)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: failed.ID, ExpectedPlanVersion: failed.Version,
		ExpectedOfferingRevision: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 2 {
		t.Fatalf("submitted %d tasks; want the retry", len(gateway.commands))
	}
	var input map[string]string
	if err := json.Unmarshal(gateway.commands[1].Input, &input); err != nil || input["source_asset"] != "model" || input["source_task"] != "" {
		t.Fatalf("retried input = %s; want the saved file", gateway.commands[1].Input)
	}
}

func TestAFailedPlanOnAFreshSourceKeepsItsReason(t *testing.T) {
	ctx := context.Background()
	service, gateway, failed := failedOldPlan(t, true)
	source := gateway.tasks["source"]
	source.CreatedAt = time.Now().UTC().Add(-time.Hour)
	gateway.tasks["source"] = source
	shown, _ := service.Get(ctx, "owner", failed.ID)
	if !shown.Steps[0].Retryable || shown.Failure != ReasonTaskFailed {
		t.Fatalf("shown plan = %#v; want it unchanged", shown)
	}
}
