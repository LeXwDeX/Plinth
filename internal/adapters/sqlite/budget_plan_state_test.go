package sqlite

import (
	"context"
	"database/sql"
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
