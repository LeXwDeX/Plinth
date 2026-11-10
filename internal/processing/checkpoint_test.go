package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// checkpointFixture builds an Agent plan "remesh -> rigging" with a pivot
// checkpoint after remesh and a resize contingency. The mock report fails the
// pivot check unless the test changes it.
func checkpointFixture(t *testing.T) (*Service, *memoryStore, *gatewayFake, Plan) {
	t.Helper()
	catalog := memoryCatalog{
		"remesh": {ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh", Domain: generation.DomainProcessing,
			Enabled: true, EstimatedCredits: 5, Parameters: []generation.Parameter{
				{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}},
		"resize": {ID: "resize", Revision: "v1", ProviderID: "meshy", Capability: "resize", Domain: generation.DomainProcessing,
			Enabled: true, EstimatedCredits: 3, Parameters: []generation.Parameter{
				{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"remesh"}},
				{Name: "origin_at", Type: "text"}}},
		"rigging": {ID: "rigging", Revision: "v1", ProviderID: "meshy", Capability: "rigging", Domain: generation.DomainProcessing,
			Enabled: true, EstimatedCredits: 5, Parameters: []generation.Parameter{
				{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"remesh", "resize"}}}},
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	ratio := 0.01
	service.WithModelReport(func(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (modelinspect.Report, error) {
		return modelinspect.Report{
			Triangles: 100,
			BBox: modelinspect.BBoxReport{Size: [3]float64{1, 1, 1},
				BottomCenterOffset: [3]float64{0, 0.5, 0}},
		}, nil
	})
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session",
		SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "checkpoint-plan-key",
		Origin: OriginAgent,
		Steps: []StepRequest{
			{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`),
				Checkpoint: &Checkpoint{Checks: []budget.PlanCheck{{Check: "pivot_bottom_center", MaximumRatio: &ratio}}}},
			{OfferingID: "rigging", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, gateway, plan
}

func succeedTask(gateway *gatewayFake, id generation.TaskID) {
	task := gateway.tasks[id]
	task.State = generation.TaskSucceeded
	task.Offering.ProviderID = "meshy"
	task.Assets = []generation.Asset{{ID: "out", Role: "model", StorageKey: "ready"}}
	gateway.tasks[id] = task
}

func TestCheckpointFailureStopsTheNextStep(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("first step submissions = %d", len(gateway.commands))
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	stored := store.plans[plan.ID]
	if stored.State != Paused || stored.Failure != ReasonCheckpointFailed {
		t.Fatalf("plan after failed checkpoint = %s/%s", stored.State, stored.Failure)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("a failed checkpoint must not start the next step: %d submissions", len(gateway.commands))
	}
	step := stored.Steps[0]
	if step.State != Succeeded || step.Checkpoint == nil || step.Checkpoint.State != CheckpointFailed {
		t.Fatalf("step checkpoint = %+v", step.Checkpoint)
	}
	if len(step.Checkpoint.Results) != 1 || step.Checkpoint.Results[0].Status != "fail" {
		t.Fatalf("checkpoint results = %+v", step.Checkpoint.Results)
	}
	report := ClassifyStepFailure(ReasonCheckpointFailed, nil)
	if report.Category != generation.CategoryParameterInvalid || report.Action != generation.ActionFixParameters {
		t.Fatalf("checkpoint failure classification = %+v", report)
	}
}

func TestCheckpointPassAndUnknownContinueThePlan(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	service.WithModelReport(func(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (modelinspect.Report, error) {
		return modelinspect.Report{BBox: modelinspect.BBoxReport{Size: [3]float64{1, 1, 1}}}, nil
	})
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 2 {
		t.Fatalf("a passed checkpoint must let the next step start: %d submissions", len(gateway.commands))
	}
	stored := store.plans[plan.ID]
	if stored.Steps[0].Checkpoint.State != CheckpointPassed || stored.State != Running {
		t.Fatalf("passed checkpoint = %+v plan %s", stored.Steps[0].Checkpoint, stored.State)
	}

	// An unreadable report is unknown and must not block.
	service, store, gateway, plan = checkpointFixture(t)
	service.WithModelReport(func(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (modelinspect.Report, error) {
		return modelinspect.Report{}, errors.New("asset store unavailable")
	})
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	stored = store.plans[plan.ID]
	if stored.Steps[0].Checkpoint.State != CheckpointUnknown || stored.State != Running {
		t.Fatalf("unknown checkpoint = %+v plan %s", stored.Steps[0].Checkpoint, stored.State)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 2 {
		t.Fatalf("unknown must not block: %d submissions", len(gateway.commands))
	}
}

func TestCreateRefusesCheckpointsWithoutAnEvaluatorOrForBrowserPlans(t *testing.T) {
	ctx := context.Background()
	catalog := memoryCatalog{"remesh": {ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh",
		Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 5,
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	ratio := 0.1
	request := CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "checkpoint-no-evaluator", Origin: OriginAgent,
		Steps: []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`),
			Checkpoint: &Checkpoint{Checks: []budget.PlanCheck{{Check: "pivot_bottom_center", MaximumRatio: &ratio}}}}}}
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("checkpoint without evaluator = %v", err)
	}
	service.WithModelReport(func(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (modelinspect.Report, error) {
		return modelinspect.Report{}, nil
	})
	request.Origin = OriginBrowser
	request.IdempotencyKey = "checkpoint-browser-plan"
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("browser checkpoint = %v", err)
	}
	request.Origin = OriginAgent
	request.IdempotencyKey = "checkpoint-unknown-check"
	request.Steps[0].Checkpoint.Checks[0].Check = "made_up"
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown check = %v", err)
	}
}

func TestReviseInsertRunsTheDeclaredContingencyInsideThePlan(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused := store.plans[plan.ID]
	contingency := budget.PlanContingency{ID: "pivot", Trigger: []string{"pivot_bottom_center"},
		Step: budget.PlanStep{Capability: "resize", OfferingID: "resize", Estimate: budget.Legacy(3)}}
	revised, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, Operation: OperationInsert, StepIndex: 0,
		ContingencyID: "pivot", Contingency: &contingency, Parameters: json.RawMessage(`{"origin_at":"bottom"}`),
		Reason: "检查点未通过，按预案把原点移到底部中心"})
	if err != nil {
		t.Fatal(err)
	}
	if len(revised.Steps) != 3 || revised.Steps[1].Capability != "resize" ||
		revised.Steps[1].ContingencyID != "pivot" || revised.Steps[1].RevisionOperation != OperationInsert {
		t.Fatalf("inserted step = %+v", revised.Steps)
	}
	if revised.State != Pending || revised.Failure != "" {
		t.Fatalf("plan after insert = %s/%s", revised.State, revised.Failure)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-2")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-3")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	final := store.plans[plan.ID]
	if final.State != Succeeded {
		t.Fatalf("final plan = %s/%s steps=%+v", final.State, final.Failure, final.Steps)
	}
	if len(gateway.commands) != 3 {
		t.Fatalf("paid submissions = %d", len(gateway.commands))
	}
}

func TestReviseOutsideTheDeclaredContingencyNeedsAPerson(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused := store.plans[plan.ID]
	other := budget.PlanContingency{ID: "other", Trigger: []string{"triangles"},
		Step: budget.PlanStep{Capability: "resize", OfferingID: "resize", Estimate: budget.Legacy(3)}}
	for name, command := range map[string]ReviseCommand{
		"unknown contingency": {OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
			ExpectedPlanVersion: paused.Version, Operation: OperationInsert, StepIndex: 0,
			ContingencyID: "missing", Parameters: json.RawMessage(`{"origin_at":"bottom"}`), Reason: "x"},
		"trigger does not match": {OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
			ExpectedPlanVersion: paused.Version, Operation: OperationInsert, StepIndex: 0,
			ContingencyID: "other", Contingency: &other, Parameters: json.RawMessage(`{"origin_at":"bottom"}`), Reason: "x"},
	} {
		if _, err := service.Revise(ctx, command); !errors.Is(err, ErrRevisionOutsidePlan) {
			t.Fatalf("%s = %v", name, err)
		}
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("a refused revision submitted a task: %d", len(gateway.commands))
	}
}

func TestReviseReplaceSkipRetryAndWaiveRespectFreezingAndLimits(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	// Replace and skip only move a step that never started.
	replaced, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: plan.Version, Operation: OperationReplace, StepIndex: 1,
		Capability: "resize", OfferingID: "resize", Parameters: json.RawMessage(`{"origin_at":"bottom"}`), Reason: "换一个未开始的步骤"})
	if err != nil || replaced.Steps[1].Capability != "resize" {
		t.Fatalf("replace pending = %+v %v", replaced.Steps, err)
	}
	// A started step cannot be moved.
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	running := store.plans[plan.ID]
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version, Operation: OperationSkip, StepIndex: 0, Reason: "skip a started step"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("skip started = %v", err)
	}
	// Version, owner and session are checked before any mutation.
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version - 1, Operation: OperationSkip, StepIndex: 1, Reason: "stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version = %v", err)
	}
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "other", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version, Operation: OperationSkip, StepIndex: 1, Reason: "foreign"}); err == nil {
		t.Fatal("foreign owner was accepted")
	}
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "other", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version, Operation: OperationSkip, StepIndex: 1, Reason: "foreign"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign session = %v", err)
	}
	// Empty and overlong reasons are refused.
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version, Operation: OperationSkip, StepIndex: 1, Reason: " "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason = %v", err)
	}
	long := make([]byte, MaxRevisionReasonCharacters+1)
	for index := range long {
		long[index] = 'a'
	}
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: running.Version, Operation: OperationSkip, StepIndex: 1, Reason: string(long)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long reason = %v", err)
	}
	// Retry uses the existing failed-step rules. The checkpoint after the first
	// step is waived so the plan can continue to the replaced step.
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	checkpointFailed := store.plans[plan.ID]
	waived, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: checkpointFailed.Version, Operation: OperationWaiveCheckpoint, StepIndex: 0,
		Reason: "先接受原点位置，验证重试路径"})
	if err != nil {
		t.Fatal(err)
	}
	_ = waived
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failedTask := gateway.tasks["step-2"]
	failedTask.State = generation.TaskFailed
	failedTask.Failure = &generation.Failure{Class: generation.FailureRateLimited, Retryable: true}
	gateway.tasks["step-2"] = failedTask
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed := store.plans[plan.ID]
	if failed.State != Failed {
		t.Fatalf("plan after upstream failure = %s", failed.State)
	}
	retried, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: failed.Version, Operation: OperationRetry, StepIndex: 1, Reason: "上游限流，重试"})
	if err != nil || retried.Steps[1].Retry != 1 {
		t.Fatalf("revise retry = %+v %v", retried.Steps, err)
	}
}

func TestReviseWaivesAFailedCheckpointOnceWithAReason(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := checkpointFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused := store.plans[plan.ID]
	waived, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, Operation: OperationWaiveCheckpoint, StepIndex: 0,
		Reason: "用户接受当前原点位置"})
	if err != nil {
		t.Fatal(err)
	}
	if waived.Steps[0].Checkpoint.State != CheckpointWaived || waived.State != Pending {
		t.Fatalf("waived checkpoint = %+v plan %s", waived.Steps[0].Checkpoint, waived.State)
	}
	// The same plan cannot waive a second checkpoint.
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: waived.Version, Operation: OperationWaiveCheckpoint, StepIndex: 0,
		Reason: "again"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second waive = %v", err)
	}
}
