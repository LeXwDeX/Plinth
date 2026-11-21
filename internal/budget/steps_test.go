package budget

import (
	"testing"
	"time"
)

// The session projection of a plan's steps (#410): the server pairs every step
// with the Task that ran it, so the browser no longer pairs them itself. The
// rule is the one the card used: same capability, the pinned Offering when both
// sides know it, one Task per step, and a Task that started before the plan was
// decided never counts.

func stepTestTime(seconds int) time.Time {
	return time.Date(2026, 11, 21, 10, 0, seconds, 0, time.UTC)
}

func TestPlanStepStatusesPairsCapabilityPinnedOfferingAndOneTaskPerStep(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{Capability: "text-to-3d", OfferingID: "tripo-v3"},
		{Capability: "text-to-3d", OfferingID: "tripo-v3"},
		{Capability: "retexture"},
	}}
	statuses := PlanStepStatuses(StepStatusInput{
		Plan:      plan,
		PlanState: StateActive,
		Tasks: []StepTask{
			{TaskID: "t1", Capability: "text-to-3d", OfferingID: "tripo-v3", State: "SUCCEEDED", Succeeded: true, Finished: true},
			{TaskID: "t2", Capability: "text-to-3d", OfferingID: "tripo-v3", State: "RUNNING"},
			{TaskID: "t3", Capability: "retexture", OfferingID: "other", State: "SUCCEEDED", Succeeded: true, Finished: true},
		},
	})
	if len(statuses) != 3 {
		t.Fatalf("statuses = %+v", statuses)
	}
	if statuses[0].State != StepDone || statuses[0].TaskID != "t1" {
		t.Fatalf("step 1 = %+v", statuses[0])
	}
	if statuses[1].State != StepRunning || statuses[1].TaskID != "t2" {
		t.Fatalf("step 2 = %+v", statuses[1])
	}
	// The step pins no Offering, so it takes the next Task of its capability
	// even though that Task ran another Offering.
	if statuses[2].State != StepDone || statuses[2].TaskID != "t3" {
		t.Fatalf("step 3 = %+v", statuses[2])
	}
	for index, status := range statuses {
		if status.Index != index+1 {
			t.Fatalf("step %d index = %d", index, status.Index)
		}
	}
}

func TestPlanStepStatusesLeavesAStepWithoutTaskWaitingWhileTheTurnRuns(t *testing.T) {
	plan := Plan{Steps: []PlanStep{{Capability: "text-to-3d"}}}
	statuses := PlanStepStatuses(StepStatusInput{Plan: plan, PlanState: StateActive, TurnRunning: true})
	if statuses[0].State != StepPending || statuses[0].TaskID != "" {
		t.Fatalf("running turn step = %+v", statuses[0])
	}
	statuses = PlanStepStatuses(StepStatusInput{Plan: plan, PlanState: StatePending})
	if statuses[0].State != StepPending {
		t.Fatalf("undecided plan step = %+v", statuses[0])
	}
	statuses = PlanStepStatuses(StepStatusInput{Plan: plan, PlanState: StateActive})
	if statuses[0].State != StepNotRun {
		t.Fatalf("ended turn step = %+v", statuses[0])
	}
}

func TestPlanStepStatusesMarksAnOptionalStepThatNeverRan(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{Capability: "text-to-image", Purpose: "正面"},
		{Capability: "text-to-image", Purpose: "右侧（可选）", Optional: true},
	}}
	statuses := PlanStepStatuses(StepStatusInput{
		Plan:      plan,
		PlanState: StateActive,
		Tasks: []StepTask{
			{TaskID: "t1", Capability: "text-to-image", State: "SUCCEEDED", Succeeded: true, Finished: true},
		},
	})
	if statuses[0].State != StepDone {
		t.Fatalf("required step = %+v", statuses[0])
	}
	if statuses[1].State != StepOptionalNotRun || !statuses[1].Optional {
		t.Fatalf("optional step = %+v", statuses[1])
	}
	if !RequiredStepsDone(statuses) {
		t.Fatal("an optional step that never ran blocked completion")
	}
}

func TestRequiredStepsDoneWaitsForEveryRequiredStep(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{Capability: "text-to-3d"},
		{Capability: "text-to-image", Optional: true},
		{Capability: "retexture"},
	}}
	statuses := PlanStepStatuses(StepStatusInput{
		Plan:      plan,
		PlanState: StateActive,
		Tasks: []StepTask{
			{TaskID: "t1", Capability: "text-to-3d", State: "SUCCEEDED", Succeeded: true, Finished: true},
		},
	})
	if RequiredStepsDone(statuses) {
		t.Fatalf("a required step without a Task completed the plan: %+v", statuses)
	}
	statuses = PlanStepStatuses(StepStatusInput{
		Plan:      plan,
		PlanState: StateActive,
		Tasks: []StepTask{
			{TaskID: "t1", Capability: "text-to-3d", State: "SUCCEEDED", Succeeded: true, Finished: true},
			{TaskID: "t2", Capability: "retexture", State: "FAILED", Finished: true},
		},
	})
	if RequiredStepsDone(statuses) {
		t.Fatalf("a failed required step completed the plan: %+v", statuses)
	}
	statuses = PlanStepStatuses(StepStatusInput{
		Plan:      plan,
		PlanState: StateActive,
		Tasks: []StepTask{
			{TaskID: "t1", Capability: "text-to-3d", State: "SUCCEEDED", Succeeded: true, Finished: true},
			{TaskID: "t2", Capability: "retexture", State: "SUCCEEDED", Succeeded: true, Finished: true},
		},
	})
	if !RequiredStepsDone(statuses) {
		t.Fatalf("every required step succeeded but the plan is not done: %+v", statuses)
	}
}

func TestEligibleStepTasksDropsTasksOlderThanTheDecision(t *testing.T) {
	decided := stepTestTime(10)
	tasks := []StepTask{
		{TaskID: "old", Capability: "text-to-3d", CreatedAt: stepTestTime(5)},
		{TaskID: "new", Capability: "text-to-3d", CreatedAt: stepTestTime(20)},
		{TaskID: "undated", Capability: "text-to-3d"},
	}
	eligible := EligibleStepTasks(tasks, decided)
	if len(eligible) != 2 || eligible[0].TaskID != "new" || eligible[1].TaskID != "undated" {
		t.Fatalf("eligible = %+v", eligible)
	}
	if kept := EligibleStepTasks(tasks, time.Time{}); len(kept) != 3 {
		t.Fatalf("an undated decision kept %+v", kept)
	}
}
