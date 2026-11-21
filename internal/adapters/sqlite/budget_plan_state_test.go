package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #410: a production plan whose required steps all have a succeeded Task is
// completed instead of staying "in progress" for ever. The store owns the
// transition (a settlement observes the Task facts) and the current-plan
// projection keeps showing the finished plan until a newer one replaces it.

// insertPlanTask writes one Task of a session with its own capability, Offering
// and submission time, so a plan's steps can be paired with it.
func insertPlanTask(t *testing.T, r *Repository, id, owner, session, capability, offeringID string, created time.Time) {
	t.Helper()
	_, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, state, progress,
  materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, ?, ?, ?, '{}', ?, 'SUCCEEDED', 100, 0, 1, ?, ?, ?)`,
		id, owner, capability, `{"id":"`+offeringID+`"}`, id+"-hash",
		formatTime(created), formatTime(created), session)
	if err != nil {
		t.Fatal(err)
	}
}

func insertPlanTurn(t *testing.T, r *Repository, sessionID, ownerID, state string) {
	t.Helper()
	_, err := r.db.Exec(`INSERT INTO go_chat_messages
 (id, session_id, role, content, image_urls, tool_calls, created_at)
 VALUES (?, ?, 'assistant', '', '[]', '[]', ?)`, "msg-"+sessionID+"-"+state, sessionID, formatTime(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO go_chat_turns
 (assistant_message_id, session_id, owner_id, options_json, state, created_at, updated_at)
 VALUES (?, ?, ?, '{}', ?, ?, ?)`,
		"msg-"+sessionID+"-"+state, sessionID, ownerID, state,
		formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
}

// reservePlanCredits gives the plan a reserved row, so a settlement pass has a
// reason to look at it: only the plans whose reservations a pass moves are
// reconsidered (#410).
func reservePlanCredits(t *testing.T, r *Repository, budgetID, requestID string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES (?, ?, '', 'legacy.credits', 1000000, 'reserved', ?)`,
		requestID, budgetID, formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
}

// chargePlanCredits gives the plan a reservation that already settled, so a
// settlement pass has nothing to move for it.
func chargePlanCredits(t *testing.T, r *Repository, budgetID, requestID string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at, settled_at)
 VALUES (?, ?, 'task-1', 'legacy.credits', 1000000, 'charged', ?, ?)`,
		requestID, budgetID, formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
}

// insertSkillRun writes one SkillRun of a session, the ledger a three-view or
// processing run keeps its steps in.
func insertSkillRun(t *testing.T, r *Repository, id, owner, session, recipe string) {
	t.Helper()
	stamp := formatTime(time.Now().UTC())
	if _, err := r.db.Exec(`INSERT INTO skill_runs
 (id, owner_id, session_id, recipe_id, recipe_revision, recipe_hash, selected_offering_id,
  source_asset_id, source_task_id, source_prepared_input_id, idempotency_key, request_hash,
  created_at, updated_at, harness_revision, harness_digest)
 VALUES (?, ?, ?, ?, 'r1', 'hash', 'offering-a', 'asset-src', 'task-src', NULL, ?, 'hash-req', ?, ?, 1, 'digest')`,
		id, owner, session, recipe, "idem-"+id, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

// insertSkillRunStep writes one step of that run with the phase and review the
// completion rule reads. taskID, grant and asset stay empty unless the step is
// output_ready, which the table's CHECK requires.
func insertSkillRunStep(t *testing.T, r *Repository, runID string, ordinal int, key, capability string,
	optional bool, phase, review, taskID, grant, asset string,
) {
	t.Helper()
	spec, err := json.Marshal(map[string]any{"key": key, "capability": capability, "optional": optional})
	if err != nil {
		t.Fatal(err)
	}
	reviewedAt := sql.NullString{}
	if review != "unreviewed" {
		reviewedAt = sql.NullString{String: formatTime(time.Now().UTC()), Valid: true}
	}
	task := sql.NullString{}
	if taskID != "" {
		task = sql.NullString{String: taskID, Valid: true}
	}
	grantRef := sql.NullString{}
	if grant != "" {
		grantRef = sql.NullString{String: grant, Valid: true}
	}
	assetRef := sql.NullString{}
	if asset != "" {
		assetRef = sql.NullString{String: asset, Valid: true}
	}
	if _, err := r.db.Exec(`INSERT INTO skill_run_steps
 (run_id, ordinal, step_key, spec_json, phase, operation_id, idempotency_key, plan_step_id,
  prepared_input_id, prepared_input_hash, grant_request_id, task_id, output_asset_id,
  review, reviewed_at, attempt, replacement_asset_id)
 VALUES (?, ?, ?, ?, ?, ?, ?, '', NULL, NULL, ?, ?, ?, ?, ?, 0, NULL)`,
		runID, ordinal, key, string(spec), phase, "op-"+runID+"-"+key, "idem-"+runID+"-"+key,
		grantRef, task, assetRef, review, reviewedAt); err != nil {
		t.Fatal(err)
	}
}

func planState(t *testing.T, r *Repository, budgetID string) string {
	t.Helper()
	var state string
	if err := r.db.QueryRow(`SELECT state FROM production_budgets WHERE id = ?`, budgetID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSettleCompletesAPlanWhoseRequiredStepsAllSucceeded(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-complete", 20, "text-to-3d", "retexture")
	reservePlanCredits(t, r, plan.ID, "eg-complete")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-2", "owner-a", "session-a", "retexture", "offering-a", now.Add(time.Minute))

	if _, err := r.SettleBudgetReservations(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateCompleted {
		t.Fatalf("plan state = %q, want %q", state, budget.StateCompleted)
	}
	// A completed plan is still the session's current plan, so the card keeps
	// showing it as done.
	current, found, err := r.CurrentProductionBudget(ctx, "owner-a", "session-a")
	if err != nil || !found || current.ID != plan.ID || current.State != budget.StateCompleted {
		t.Fatalf("current plan = %+v found=%v err=%v", current, found, err)
	}
	// Re-running the settlement must not move the plan again.
	if _, err := r.SettleBudgetReservations(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateCompleted {
		t.Fatalf("a repeated settlement moved the plan to %q", state)
	}
}

func TestSettleLeavesAPlanWhoseStepsAreNotAllSucceeded(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-partial", 20, "text-to-3d", "retexture")
	reservePlanCredits(t, r, plan.ID, "eg-partial")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-2", "owner-a", "session-a", "retexture", "offering-a", now.Add(time.Minute))
	// The second step's Task failed, so the plan is not done.
	if _, err := r.db.Exec(`UPDATE generation_tasks SET state = 'FAILED' WHERE id = 'task-2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SettleBudgetReservations(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateActive {
		t.Fatalf("plan state = %q, want %q", state, budget.StateActive)
	}

	// A Task submitted before the plan was decided did not run its step. It
	// needs a session of its own: a Task of this session that started after the
	// plan was decided is exactly what the plan's step pairs with.
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions
 (id, owner_id, owner_name, title, created_at, updated_at)
 VALUES ('session-old', 'owner-a', 'owner-a', '旧会话', ?, ?)`,
		formatTime(now.Add(-2*time.Hour)), formatTime(now.Add(-2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	old, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-old-task", OwnerID: "owner-a", SessionID: "session-old",
		Plan: budget.Plan{Title: "旧任务计划", Steps: []budget.PlanStep{
			{Capability: "text-to-3d", Purpose: "主体", Estimate: budget.Legacy(7)}}},
		Limits: []budget.Amount{budget.Legacy(20)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", old.ID, "human-a", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	insertPlanTask(t, r, "task-old", "owner-a", "session-old", "text-to-3d", "offering-a", now.Add(-time.Hour))
	if _, err := r.SettleBudgetReservations(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, old.ID); state != budget.StateActive {
		t.Fatalf("a plan completed on a Task older than its decision: %q", state)
	}
}

func TestSettleCompletesAPlanWhoseOnlyUnrunStepIsOptional(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	now := time.Now().UTC()
	steps := []budget.PlanStep{
		{Capability: "text-to-3d", Purpose: "主体", Estimate: budget.Legacy(7)},
		{Capability: "text-to-image", Purpose: "可选的第四个视角", Estimate: budget.Legacy(7), Optional: true},
	}
	plan, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-optional", OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "带可选步骤的计划", Steps: steps}, Limits: []budget.Amount{budget.Legacy(20)},
		State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", plan.ID, "human-a", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reservePlanCredits(t, r, plan.ID, "eg-optional")
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	if _, err := r.SettleBudgetReservations(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateCompleted {
		t.Fatalf("plan state = %q, want %q", state, budget.StateCompleted)
	}
}

// A finished plan must not spend: the person approved a plan for the work it
// lists, and after the last step nothing of it is left to approve by budget.
func TestCompletedPlanNoLongerApprovesAStepByBudget(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-spent-plan", 20, "text-to-3d")
	reservePlanCredits(t, r, plan.ID, "eg-spent-plan")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	if _, err := r.SettleBudgetReservations(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The plan allows the capability, but it is no longer active: a new step
	// must wait for a person, exactly as it does under a closed plan.
	i, g := prepareBudgetGrant(t, r, "turn-a", "after-completion", "text-to-3d", 7)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID,
		"budget:"+plan.ID, budget.Legacy(7)); err == nil {
		t.Fatal("a completed plan approved a new step")
	}
}

// A new plan is proposed while a completed one is the current plan, and its
// approval closes the finished one the way it closes an exhausted one.
func TestApprovingANewPlanClosesTheCompletedOne(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	first := budgetTestPlan(t, r, "pb-first", 20, "text-to-3d")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	if _, err := r.SettleBudgetReservations(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	second, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-second", OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "新一轮计划", Steps: []budget.PlanStep{
			{Capability: "retexture", Purpose: "重做贴图", Estimate: budget.Legacy(7)}}},
		Limits: []budget.Amount{budget.Legacy(20)}, State: budget.StatePending, CreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", second.ID, "human-a", true, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, first.ID); state != budget.StateClosed {
		t.Fatalf("the completed plan state = %q, want %q", state, budget.StateClosed)
	}
	current, found, err := r.CurrentProductionBudget(ctx, "owner-a", "session-a")
	if err != nil || !found || current.ID != second.ID {
		t.Fatalf("current plan = %+v found=%v err=%v", current, found, err)
	}
}

// The migration rebuild keeps every row and widens the state CHECK: a stored
// plan can be completed without a rewrite.
func TestProductionBudgetMigrationKeepsRowsAndAcceptsCompleted(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	plan := budgetTestPlan(t, r, "pb-migrate", 20, "text-to-3d")
	if _, err := r.db.Exec(`UPDATE production_budgets SET state = 'completed' WHERE id = ?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := r.GetProductionBudget(ctx, "owner-a", plan.ID)
	if err != nil || stored.State != budget.StateCompleted {
		t.Fatalf("stored plan = %+v err=%v", stored, err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM production_budgets`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("production_budgets rows = %d err=%v", count, err)
	}
	var states string
	if err := r.db.QueryRow(`SELECT sql FROM sqlite_master
 WHERE type = 'table' AND name = 'production_budgets'`).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(states, budget.StateCompleted) {
		t.Fatalf("the rebuilt table CHECK does not accept %q: %s", budget.StateCompleted, states)
	}
	// SQLite keeps at most one open statement per connection here, so the
	// pragma is read fully before the index definition is looked up.
	rows, err := r.db.Query(`PRAGMA index_list('production_budgets')`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !slices.Contains(names, "idx_production_budgets_current") {
		t.Fatalf("the current-plan index is gone: %v", names)
	}
	var currentWhere string
	if err := r.db.QueryRow(`SELECT COALESCE(sql, '') FROM sqlite_master
 WHERE type = 'index' AND name = 'idx_production_budgets_current'`).Scan(&currentWhere); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{budget.StateActive, budget.StateExhausted, budget.StateCompleted} {
		if !strings.Contains(currentWhere, "'"+state+"'") {
			t.Fatalf("the current-plan index does not cover %q: %s", state, currentWhere)
		}
	}
}

// The projection pairs the plan's steps with the session's Tasks (#410): the
// browser reads the result instead of pairing them itself.
func TestPlanTaskFactsAndTheSessionProjection(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertPlanTurn(t, r, "session-a", "owner-a", "running")
	budgetTestPlan(t, r, "pb-progress", 20, "text-to-3d", "retexture")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-2", "owner-a", "session-a", "retexture", "offering-a", now.Add(time.Minute))
	if _, err := r.db.Exec(`UPDATE generation_tasks SET state = 'RUNNING' WHERE id = 'task-2'`); err != nil {
		t.Fatal(err)
	}
	summaries, err := r.ProductionBudgetSummaries(ctx, "owner-a", []string{"session-a"})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries = %+v err=%v", summaries, err)
	}
	// The store does not project the steps; the chat service does, from these
	// facts and the session's turn states.
	facts, err := r.PlanTaskFacts(ctx, "owner-a", []string{"session-a"}, []string{"running"})
	if err != nil {
		t.Fatal(err)
	}
	entry := facts["session-a"]
	if !entry.TurnRunning || len(entry.Tasks) != 2 {
		t.Fatalf("facts = %+v", entry)
	}
	statuses := budget.PlanStepStatuses(budget.StepStatusInput{
		Plan: summaries["session-a"].Plan, PlanState: summaries["session-a"].State,
		Tasks: entry.Tasks, TurnRunning: entry.TurnRunning,
	})
	if len(statuses) != 2 || statuses[0].State != budget.StepDone || statuses[1].State != budget.StepRunning {
		t.Fatalf("statuses = %+v", statuses)
	}

	// A session without a turn and without a Task has no facts, and the
	// projection then reads every step as not run.
	other, err := r.PlanTaskFacts(ctx, "owner-a", []string{"session-b"}, []string{"running"})
	if err != nil || len(other) != 0 {
		t.Fatalf("empty facts = %+v err=%v", other, err)
	}
}

var _ = sql.ErrNoRows
var _ generation.TaskState

// The sweep runs every two seconds, so a settlement may only reconsider the
// plans whose reservations it actually moved. A plan whose steps all succeeded
// but whose reservations already settled (its steps were paid without a
// reservation, or the pass that would have collected it never touched it) is
// left for the one-shot a deployment runs at start-up, not re-read forever.
func TestSettleLeavesAnUntouchedPlanForTheStartupSweep(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	now := time.Now().UTC()
	// Two sessions: a session holds one current plan, so approving a second
	// plan in the same session would close the first.
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions
 (id, owner_id, owner_name, title, created_at, updated_at)
 VALUES ('session-b', 'owner-a', 'owner-a', '另一个会话', ?, ?)`,
		formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	touched := budgetTestPlan(t, r, "pb-touched", 20, "text-to-3d", "retexture")
	reservePlanCredits(t, r, touched.ID, "eg-touched")
	untouched, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-untouched", OwnerID: "owner-a", SessionID: "session-b",
		Plan: budget.Plan{Title: "未触及的计划", Steps: []budget.PlanStep{
			{Capability: "text-to-3d", Purpose: "主体", Estimate: budget.Legacy(7)}}},
		Limits: []budget.Amount{budget.Legacy(20)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", untouched.ID, "human-a", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	chargePlanCredits(t, r, untouched.ID, "eg-untouched")
	insertPlanTask(t, r, "task-touched-1", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-touched-2", "owner-a", "session-a", "retexture", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-untouched", "owner-a", "session-b", "text-to-3d", "offering-a", now.Add(time.Minute))
	// The touched plan has two steps and two Tasks; the untouched one has one.
	if _, err := r.SettleBudgetReservations(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, touched.ID); state != budget.StateCompleted {
		t.Fatalf("the settled plan state = %q, want %q", state, budget.StateCompleted)
	}
	if state := planState(t, r, untouched.ID); state != budget.StateActive {
		t.Fatalf("the untouched plan was read anyway: %q", state)
	}

	// The one-shot at start-up collects exactly that plan.
	completed, err := r.CompleteFinishedBudgets(ctx, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || completed[0].ID != untouched.ID {
		t.Fatalf("startup collection = %+v", completed)
	}
	if state := planState(t, r, untouched.ID); state != budget.StateCompleted {
		t.Fatalf("after the startup sweep = %q, want %q", state, budget.StateCompleted)
	}
	// A second collection is a no-op.
	again, err := r.CompleteFinishedBudgets(ctx, now.Add(4*time.Minute))
	if err != nil || len(again) != 0 {
		t.Fatalf("repeated collection = %+v err=%v", again, err)
	}
}

// The one-plan scope is what a decision or a continuation turn uses: it reads
// and writes that plan only, and never a plan of another owner.
func TestCompleteFinishedBudgetScopesToOnePlanOfOneOwner(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	mine := budgetTestPlan(t, r, "pb-mine", 20, "text-to-3d")
	now := time.Now().UTC()
	// The other plan belongs to another session, so it stays current too.
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions
 (id, owner_id, owner_name, title, created_at, updated_at)
 VALUES ('session-b', 'owner-a', 'owner-a', '另一个会话', ?, ?)`,
		formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	other, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-other", OwnerID: "owner-a", SessionID: "session-b",
		Plan: budget.Plan{Title: "另一会话的计划", Steps: []budget.PlanStep{
			{Capability: "text-to-3d", Purpose: "主体", Estimate: budget.Legacy(7)}}},
		Limits: []budget.Amount{budget.Legacy(20)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", other.ID, "human-a", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	insertPlanTask(t, r, "task-mine", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))
	insertPlanTask(t, r, "task-other", "owner-a", "session-b", "text-to-3d", "offering-a", now.Add(time.Minute))

	if _, err := r.CompleteFinishedBudget(ctx, "owner-a", "pb-does-not-exist", now); !errors.Is(err, budget.ErrNotFound) {
		t.Fatalf("unknown plan = %v", err)
	}
	if _, err := r.CompleteFinishedBudget(ctx, "owner-b", mine.ID, now); !errors.Is(err, budget.ErrNotFound) {
		t.Fatalf("foreign plan = %v", err)
	}
	completed, err := r.CompleteFinishedBudget(ctx, "owner-a", mine.ID, now)
	if err != nil || len(completed) != 1 || completed[0].ID != mine.ID {
		t.Fatalf("one plan = %+v err=%v", completed, err)
	}
	if state := planState(t, r, mine.ID); state != budget.StateCompleted {
		t.Fatalf("the named plan state = %q", state)
	}
	if state := planState(t, r, other.ID); state != budget.StateActive {
		t.Fatalf("the unnamed plan moved to %q", state)
	}
}

// A SkillRun step the plan covers holds the plan open while it is unfinished:
// the Task of a rejected view succeeded, but the view must be redrawn, and that
// redraw spends the budget the person already approved for the view (#410 with
// #243). An accepted view (or one the person replaced) does not hold it back.
func TestPlanWaitsForAnUnfinishedSkillRunStep(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-views", 20, "text-to-3d")
	now := time.Now().UTC()
	insertPlanTask(t, r, "task-view", "owner-a", "session-a", "text-to-3d", "offering-a", now.Add(time.Minute))

	// The SkillRun of the session has a planned step of that capability.
	insertSkillRun(t, r, "run-1", "owner-a", "session-a", "cover")
	insertSkillRunStep(t, r, "run-1", 0, "cover", "text-to-3d", false, "planned", "unreviewed", "", "", "")

	if _, err := r.CompleteFinishedBudgets(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateActive {
		t.Fatalf("a planned view did not hold the plan: %q", state)
	}

	// The same step, accepted, does not hold it back: its Task delivered a
	// model and the review accepted it.
	if _, err := r.db.Exec(`INSERT INTO generation_attempts
 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
 VALUES ('attempt-view', 'task-view', 0, '{"id":"offering-a"}', 'SUCCEEDED', 'sub-view', ?, ?)`,
		formatTime(now.Add(time.Minute)), formatTime(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_assets
 (id, task_id, attempt_id, role, required, ordinal, storage_key, media_type, size_bytes, sha256, created_at)
 VALUES ('asset-view', 'task-view', 'attempt-view', 'model', 1, 1, 'blobs/view', 'application/octet-stream', 1, 'view', ?)`,
		formatTime(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE skill_run_steps SET phase = 'output_ready', review = 'accepted',
 prepared_input_id = 'prep-view', prepared_input_hash = 'hash-view', reviewed_at = ?,
 task_id = 'task-view', grant_request_id = 'eg-view', output_asset_id = 'asset-view'
 WHERE run_id = 'run-1' AND step_key = 'cover'`, formatTime(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	completed, err := r.CompleteFinishedBudgets(ctx, now.Add(3*time.Minute))
	if err != nil || len(completed) != 1 {
		t.Fatalf("accepted view = %+v err=%v", completed, err)
	}
	if state := planState(t, r, plan.ID); state != budget.StateCompleted {
		t.Fatalf("after the accepted view = %q, want completed", state)
	}
}
