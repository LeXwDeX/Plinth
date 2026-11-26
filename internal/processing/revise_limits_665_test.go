package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #665: every Agent retry is a new paid attempt. Once a step was started again
// MaxAgentStepRetries times, the Agent's revise retry leaves the next one to
// the person.
func TestReviseRetryIsCappedPerStepForTheAgent(t *testing.T) {
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
	capped := store.plans[plan.ID]
	capped.Steps[1].Retry = MaxAgentStepRetries
	capped.State, capped.Failure = failed.State, failed.Failure
	capped.Steps[1].State, capped.Steps[1].TaskID = Failed, "step-2"
	store.plans[plan.ID] = capped
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: capped.Version, Operation: OperationRetry, StepIndex: 1, Reason: "再试一次"}); !errors.Is(err, ErrRevisionRetryPerson) {
		t.Fatalf("retry past the cap = %v, want ErrRevisionRetryPerson", err)
	}
}

// #665: a declared contingency is one approved repair; once its step is in the
// plan the Agent cannot insert it again.
func TestReviseInsertUsesEachContingencyOnce(t *testing.T) {
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
	paused.Steps[1].ContingencyID = "pivot" // the contingency already ran once
	store.plans[plan.ID] = paused
	contingency := budget.PlanContingency{ID: "pivot", Trigger: []string{"pivot_bottom_center"},
		Step: budget.PlanStep{Capability: "resize", OfferingID: "resize", Estimate: budget.Legacy(3)}}
	if _, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, Operation: OperationInsert, StepIndex: 0,
		ContingencyID: "pivot", Contingency: &contingency, Parameters: json.RawMessage(`{"origin_at":"bottom"}`),
		Reason: "再用一次预案"}); !errors.Is(err, ErrRevisionOutsidePlan) {
		t.Fatalf("second insert of one contingency = %v, want ErrRevisionOutsidePlan", err)
	}
}
