package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// #561: a one-step plan priced in a provider's own unit (Tripo credits) whose
// only step charged the whole limit. The settlement found the step's SkillRun
// output not yet recorded, so it could not complete the plan and marked it
// exhausted instead. The plan must keep its decision time (the step pairing is
// bounded by it), its projection must read the charged amount in that unit,
// and recording the step's output must complete it.

const tripoUnit = "tripo.credit"

// insertBoundModelStep gives the succeeded Task "task-3d" its delivered model
// and a SkillRun step that submitted it and is still task_bound, the way the
// step stands between the Task's success and the run recording its output.
func insertBoundModelStep(t *testing.T, r *Repository, offeringID string, taskAt time.Time) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO generation_attempts
 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
 VALUES ('attempt-3d', 'task-3d', 0, ?, 'SUCCEEDED', 'sub-3d', ?, ?)`,
		`{"id":"`+offeringID+`"}`, formatTime(taskAt), formatTime(taskAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_assets
 (id, task_id, attempt_id, role, required, ordinal, storage_key, media_type, size_bytes, sha256, created_at)
 VALUES ('asset-3d', 'task-3d', 'attempt-3d', 'model', 1, 1, 'blobs/3d', 'model/gltf-binary', 1, '3d', ?)`,
		formatTime(taskAt)); err != nil {
		t.Fatal(err)
	}
	insertSkillRun(t, r, "run-3d", "owner-a", "session-a", "multiview")
	// The run is loaded by the output mutation, which reads a valid (here: no)
	// Harness binding.
	if _, err := r.db.Exec(`UPDATE skill_runs SET harness_revision = 0, harness_digest = '' WHERE id = 'run-3d'`); err != nil {
		t.Fatal(err)
	}
	spec, err := json.Marshal(map[string]any{"key": "model", "capability": "image-to-3d", "output_role": "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO skill_run_steps
 (run_id, ordinal, step_key, spec_json, phase, operation_id, idempotency_key, plan_step_id,
  prepared_input_id, prepared_input_hash, grant_request_id, task_id, output_asset_id,
  review, reviewed_at, attempt, replacement_asset_id)
 VALUES ('run-3d', 0, 'model', ?, 'task_bound', 'op-3d', 'idem-3d', '',
  'prep-3d', 'hash-3d', 'eg-3d', 'task-3d', NULL, 'unreviewed', NULL, 0, NULL)`, string(spec)); err != nil {
		t.Fatal(err)
	}
}

func TestExhaustedPlanKeepsItsDecisionTimeAndRecordedOutputCompletesIt(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	created := time.Now().UTC().Truncate(time.Second)
	decided := created.Add(time.Second)
	limit := tripoCredits(110)
	stored, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-tripo", OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "四视图生成石雕", Steps: []budget.PlanStep{{
			Capability: "image-to-3d", OfferingID: "tripo-p2-multiview", Purpose: "多视图低模", Estimate: limit,
		}}},
		Limits: []budget.Amount{limit}, State: budget.StatePending, CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", stored.ID, "human-a", true, decided); err != nil {
		t.Fatal(err)
	}

	// The step's Task ran after the decision and succeeded with its model.
	taskAt := decided.Add(time.Minute)
	insertPlanTask(t, r, "task-3d", "owner-a", "session-a", "image-to-3d", "tripo-p2-multiview", taskAt)
	// The SkillRun step that submitted it is still task_bound: its output has
	// not been recorded when the settlement runs.
	insertBoundModelStep(t, r, "tripo-p2-multiview", taskAt)
	// The step's reservation, in Tripo credits, bound to the Task.
	if _, err := r.db.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES ('eg-3d', ?, 'task-3d', ?, ?, 'reserved', ?)`,
		stored.ID, tripoUnit, limit.Micros, formatTime(taskAt)); err != nil {
		t.Fatal(err)
	}

	settledAt := taskAt.Add(time.Minute)
	exhausted, err := r.SettleBudgetReservations(ctx, settledAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(exhausted) != 1 || planState(t, r, stored.ID) != budget.StateExhausted {
		t.Fatalf("exhausted = %+v, state = %q", exhausted, planState(t, r, stored.ID))
	}

	summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found {
		t.Fatalf("summary found=%v err=%v", found, err)
	}
	if summary.State != budget.StateExhausted {
		t.Fatalf("summary state = %q", summary.State)
	}
	unit, ok := summary.Unit(tripoUnit)
	if !ok || unit.Spent != limit.Micros || unit.Reserved != 0 || unit.Remaining != 0 || unit.Limit != limit.Micros {
		t.Fatalf("Tripo credits = %+v (found %v), want spent=limit, remaining 0", unit, ok)
	}
	// The decision time is the person's decision, not the settlement: the
	// plan's own Task must still pair with its step.
	if summary.DecidedAt == nil || !summary.DecidedAt.Equal(decided) {
		t.Fatalf("decided_at = %v, want %v", summary.DecidedAt, decided)
	}
	statuses := budget.PlanStepStatuses(budget.StepStatusInput{
		Plan: summary.Plan, PlanState: summary.State, DecidedAt: *summary.DecidedAt,
		Tasks: budget.EligibleStepTasks(sessionStepTasks(ctx, r.db, "owner-a", "session-a"), *summary.DecidedAt),
	})
	if len(statuses) != 1 || statuses[0].State != budget.StepDone || statuses[0].TaskID != "task-3d" {
		t.Fatalf("step statuses = %+v, want the Task paired as done", statuses)
	}

	// Recording the step's output lets the plan be judged again: every
	// required step has a succeeded Task, so it is completed.
	if _, err := r.RecordSkillOutput(ctx, skillrun.OutputCommand{
		OwnerID: generation.OwnerID("owner-a"), RunID: "run-3d", StepKey: "model", AssetID: generation.AssetID("asset-3d"),
	}); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, stored.ID); state != budget.StateCompleted {
		t.Fatalf("after the recorded output = %q, want %q", state, budget.StateCompleted)
	}
	summary, _, err = r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if unit, _ := summary.Unit(tripoUnit); unit.Spent != limit.Micros || unit.Remaining != 0 {
		t.Fatalf("completed plan Tripo credits = %+v", unit)
	}
}

// The output hook judges only a used-up plan. An active plan whose steps all
// succeeded stays active until the settlement judges it: the view's review may
// still reject it, and the redraw is paid from that plan (#243).
func TestRecordedOutputLeavesAnActivePlanToTheSettlement(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-active", 20, "image-to-3d")
	taskAt := time.Now().UTC().Add(time.Minute)
	insertPlanTask(t, r, "task-3d", "owner-a", "session-a", "image-to-3d", "offering-a", taskAt)
	insertBoundModelStep(t, r, "offering-a", taskAt)
	if _, err := r.RecordSkillOutput(ctx, skillrun.OutputCommand{
		OwnerID: generation.OwnerID("owner-a"), RunID: "run-3d", StepKey: "model", AssetID: generation.AssetID("asset-3d"),
	}); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateActive {
		t.Fatalf("the output hook moved an active plan to %q", state)
	}
}
