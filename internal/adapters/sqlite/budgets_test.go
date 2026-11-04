package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// budgetTestPlan creates an active plan for session-a with one planned
// capability per step.
func budgetTestPlan(t *testing.T, r *Repository, id string, limit int, capabilities ...string) budget.ProductionBudget {
	t.Helper()
	ctx := context.Background()
	steps := make([]budget.PlanStep, 0, len(capabilities))
	for _, capability := range capabilities {
		steps = append(steps, budget.PlanStep{Capability: capability, Purpose: capability, EstimatedCredits: 7})
	}
	now := time.Now().UTC()
	stored, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "制作计划", Steps: steps}, LimitCredits: limit,
		State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := r.DecideProductionBudget(ctx, "owner-a", stored.ID, "human-a", true, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if active.State != budget.StateActive {
		t.Fatalf("plan state = %q", active.State)
	}
	return active
}

// prepareBudgetGrant freezes a prepared input worth credits and creates the
// pending Agent grant an active plan may approve.
func prepareBudgetGrant(t *testing.T, r *Repository, turnID, suffix, capability string, credits int) (executiongrant.Identity, executiongrant.Grant) {
	t.Helper()
	i := grantIdentity()
	i.OfferingID = "offering-" + suffix
	i.PreparedInputID = "prepared-" + suffix
	i.PreparedInputHash = "hash-" + suffix
	i.IdempotencyKey = "key-" + suffix
	i.OperationID = "op-" + suffix
	i.PlanStepID = ""
	err := r.CreatePreparedInput(context.Background(), generation.PreparedInput{
		ID: generation.PreparedInputID(i.PreparedInputID), OwnerID: generation.OwnerID(i.OwnerID),
		SessionID: i.SessionID, Capability: generation.Capability(capability),
		OfferingID: generation.OfferingID(i.OfferingID), OfferingRevision: i.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: i.PreparedInputHash,
		EstimatedCredits: credits, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := r.PreparePendingWithBudget(context.Background(), i, executiongrant.AgentPending, turnID, true)
	if err != nil {
		t.Fatal(err)
	}
	return i, g
}

// bindBudgetTask claims the approved grant and binds it to a queued Task.
func bindBudgetTask(t *testing.T, r *Repository, i executiongrant.Identity, requestID, taskID string) {
	t.Helper()
	ctx := context.Background()
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, requestID, i); err != nil || task != "" {
		t.Fatalf("claim: task=%q err=%v", task, err)
	}
	insertGrantedTask(t, ctx, conn, taskID, i)
	if err := r.BindExecutionGrantOnConn(ctx, conn, requestID, i, taskID); err != nil {
		t.Fatal(err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
}

func finishBudgetTask(t *testing.T, r *Repository, taskID string, state generation.TaskState) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE generation_tasks SET state = ?, updated_at = ? WHERE id = ?`, state, now, taskID); err != nil {
		t.Fatal(err)
	}
}

func reservationState(t *testing.T, r *Repository, requestID string) string {
	t.Helper()
	var state string
	if err := r.db.QueryRow(`SELECT state FROM budget_reservations WHERE grant_request_id = ?`, requestID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// A reservation becomes charged only when the Task might have been charged:
// a succeeded Task is spent, a failed or canceled Task that never reached the
// provider is released back to the plan.
func TestProductionBudgetReservationFollowsTheTaskChargeFact(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		state      generation.TaskState
		wantSpent  int
		wantState  string
		wantRemain int
	}{
		{"succeeded", generation.TaskSucceeded, 7, "charged", 13},
		{"failed", generation.TaskFailed, 0, "released", 20},
		{"canceled", generation.TaskCanceled, 0, "released", 20},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			r := openGrantTestRepo(t)
			insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
			plan := budgetTestPlan(t, r, "pb-"+testCase.name, 20, "text-to-image")
			i, g := prepareBudgetGrant(t, r, "turn-a", testCase.name, "text-to-image", 7)
			if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID,
				executiongrant.BudgetActorPrefix+plan.ID, 7); err != nil {
				t.Fatal(err)
			}
			bindBudgetTask(t, r, i, g.RequestID, "task-"+testCase.name)
			if state := reservationState(t, r, g.RequestID); state != "reserved" {
				t.Fatalf("binding changed the reservation: %s", state)
			}
			finishBudgetTask(t, r, "task-"+testCase.name, testCase.state)
			exhausted, err := r.SettleBudgetReservations(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if len(exhausted) != 0 {
				t.Fatalf("settling %s exhausted a plan: %+v", testCase.name, exhausted)
			}
			if state := reservationState(t, r, g.RequestID); state != testCase.wantState {
				t.Fatalf("reservation state = %q, want %q", state, testCase.wantState)
			}
			summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
			if err != nil || !found {
				t.Fatalf("summary: %+v found=%v err=%v", summary, found, err)
			}
			if summary.SpentCredits != testCase.wantSpent || summary.ReservedCredits != 0 ||
				summary.RemainingCredits != testCase.wantRemain || summary.State != budget.StateActive {
				t.Fatalf("summary after %s = %+v", testCase.name, summary)
			}
		})
	}
}

// Reaching the limit exhausts the plan, reports it once, and refuses further
// steps until a person approves a new plan.
// An Agent-created processing plan has no chat turn: its grants carry the
// plan's browser principal with server-set budget eligibility, and the plan
// approves them like a chat Agent step. A browser plan's grant is not eligible.
func TestProductionBudgetApprovesAnAgentProcessingStepWithoutAChatTurn(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	plan := budgetTestPlan(t, r, "pb-processing", 20, "retexture")
	i := grantIdentity()
	i.PreparedInputID = "prepared-processing"
	i.PreparedInputHash = "hash-processing"
	i.IdempotencyKey = "key-processing"
	i.OperationID = "op-processing"
	i.PlanStepID = ""
	if err := r.CreatePreparedInput(ctx, generation.PreparedInput{
		ID: generation.PreparedInputID(i.PreparedInputID), OwnerID: generation.OwnerID(i.OwnerID),
		SessionID: i.SessionID, Capability: generation.Capability("retexture"),
		OfferingID: generation.OfferingID(i.OfferingID), OfferingRevision: i.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: i.PreparedInputHash,
		EstimatedCredits: 5, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	g, err := r.PreparePendingWithBudget(ctx, i, executiongrant.HumanBrowser, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 5); err != nil {
		t.Fatalf("agent processing step: %v", err)
	}
	stored, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || stored.Status != executiongrant.Approved || !stored.ApprovedByBudget() {
		t.Fatalf("processing grant = %+v err=%v", stored, err)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, i.OwnerID, i.SessionID)
	if err != nil || !found || summary.ReservedCredits != 5 || summary.RemainingCredits != 15 {
		t.Fatalf("budget summary = %+v found=%v err=%v", summary, found, err)
	}

	browserIdentity := grantIdentity()
	browserIdentity.PreparedInputID = "prepared-browser-plan"
	browserIdentity.PreparedInputHash = "hash-browser-plan"
	browserIdentity.IdempotencyKey = "key-browser-plan"
	browserIdentity.OperationID = "op-browser-plan"
	browserIdentity.PlanStepID = ""
	if err := r.CreatePreparedInput(ctx, generation.PreparedInput{
		ID: generation.PreparedInputID(browserIdentity.PreparedInputID), OwnerID: generation.OwnerID(browserIdentity.OwnerID),
		SessionID: browserIdentity.SessionID, Capability: generation.Capability("retexture"),
		OfferingID: generation.OfferingID(browserIdentity.OfferingID), OfferingRevision: browserIdentity.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: browserIdentity.PreparedInputHash,
		EstimatedCredits: 1, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	browser, err := r.PreparePending(ctx, browserIdentity, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	storedBrowser, err := r.GetExecutionGrant(ctx, i.OwnerID, browser.RequestID)
	if err != nil || storedBrowser.BudgetEligible {
		t.Fatalf("browser processing grant = %+v err=%v", storedBrowser, err)
	}
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, browser.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 1); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("ineligible browser grant: %v", err)
	}
}

func TestProductionBudgetExhaustsOnceAndRefusesFurtherSteps(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-exhaust", 7, "text-to-image")
	i, g := prepareBudgetGrant(t, r, "turn-a", "spend", "text-to-image", 7)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 7); err != nil {
		t.Fatal(err)
	}
	bindBudgetTask(t, r, i, g.RequestID, "task-spend")
	finishBudgetTask(t, r, "task-spend", generation.TaskSucceeded)
	exhausted, err := r.SettleBudgetReservations(ctx, time.Now().UTC())
	if err != nil || len(exhausted) != 1 || exhausted[0].ID != plan.ID {
		t.Fatalf("first settle = %+v err=%v", exhausted, err)
	}
	if again, err := r.SettleBudgetReservations(ctx, time.Now().UTC()); err != nil || len(again) != 0 {
		t.Fatalf("second settle = %+v err=%v", again, err)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || summary.State != budget.StateExhausted ||
		summary.SpentCredits != 7 || summary.RemainingCredits != 0 {
		t.Fatalf("exhausted summary = %+v found=%v err=%v", summary, found, err)
	}

	_, next := prepareBudgetGrant(t, r, "turn-a", "after-exhaust", "text-to-image", 1)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-a", next.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 1); !errors.Is(err, budget.ErrInactive) {
		t.Fatalf("step after exhaustion: %v", err)
	}
	var reservations int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM budget_reservations`).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatalf("reservations = %d err=%v", reservations, err)
	}
}

// A pending plan that a person denies or that expires approves nothing and has
// no reservation to release.
func TestProductionBudgetDenialAndExpiryApproveNothing(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	now := time.Now().UTC()
	denied, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-denied", OwnerID: "owner-a", SessionID: "session-a",
		Plan:         budget.Plan{Title: "不需要", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		LimitCredits: 10, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.DecideProductionBudget(ctx, "owner-a", denied.ID, "human-a", false, now.Add(time.Second))
	if err != nil || got.State != budget.StateDenied {
		t.Fatalf("denied plan = %+v err=%v", got, err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", denied.ID, "human-a", true, now.Add(2*time.Second)); !errors.Is(err, budget.ErrState) {
		t.Fatalf("deciding a denied plan again: %v", err)
	}

	created := now.Add(-2 * time.Hour)
	expiring, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-expired", OwnerID: "owner-a", SessionID: "session-a",
		Plan:         budget.Plan{Title: "过期", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		LimitCredits: 10, State: budget.StatePending, CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := r.ExpireProductionBudgets(ctx, now.Add(-time.Hour))
	if err != nil || len(expired) != 1 || expired[0].ID != expiring.ID {
		t.Fatalf("expired = %+v err=%v", expired, err)
	}
	if again, err := r.ExpireProductionBudgets(ctx, now); err != nil || len(again) != 0 {
		t.Fatalf("second expiry = %+v err=%v", again, err)
	}
	stored, err := r.GetProductionBudget(ctx, "owner-a", expiring.ID)
	if err != nil || stored.State != budget.StateDenied {
		t.Fatalf("expired plan = %+v err=%v", stored, err)
	}
	var reservations int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM budget_reservations`).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("a pending plan reserved credits: %d err=%v", reservations, err)
	}
}

// Approving a new plan closes the old active plan. The old plan's reservation
// is released when its trigger turn was stopped before the step could bind,
// since that grant can never become a Task.
func TestProductionBudgetNewPlanClosesTheOldOneAndReleasesUnclaimableReservations(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	old := budgetTestPlan(t, r, "pb-old", 20, "text-to-image")
	i, g := prepareBudgetGrant(t, r, "turn-a", "old", "text-to-image", 7)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, old.ID,
		executiongrant.BudgetActorPrefix+old.ID, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id='turn-a'`); err != nil {
		t.Fatal(err)
	}
	pending, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-new", OwnerID: "owner-a", SessionID: "session-a",
		Plan:         budget.Plan{Title: "新计划", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		LimitCredits: 30, State: budget.StatePending, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", pending.ID, "human-a", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	closed, err := r.GetProductionBudget(ctx, "owner-a", old.ID)
	if err != nil || closed.State != budget.StateClosed {
		t.Fatalf("old plan = %+v err=%v", closed, err)
	}
	if state := reservationState(t, r, g.RequestID); state != "released" {
		t.Fatalf("unclaimable reservation = %q", state)
	}
	summary, found, err := r.BudgetSummaryForGrant(ctx, "owner-a", g.RequestID)
	if err != nil || !found || summary.State != budget.StateClosed ||
		summary.ReservedCredits != 0 || summary.SpentCredits != 0 {
		t.Fatalf("old plan summary = %+v found=%v err=%v", summary, found, err)
	}
	current, found, err := r.CurrentProductionBudget(ctx, "owner-a", "session-a")
	if err != nil || !found || current.ID != pending.ID || current.State != budget.StateActive {
		t.Fatalf("new current plan = %+v found=%v err=%v", current, found, err)
	}
}

// Two concurrent approvals cannot both take the last credits: the BEGIN
// IMMEDIATE transaction settles and re-checks the remaining limit before it
// inserts the reservation, so one wins and the other is told over_budget.
func TestProductionBudgetConcurrentApprovalsCannotBothTakeTheLastCredits(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-race", 10, "text-to-image")
	_, gA := prepareBudgetGrant(t, r, "turn-a", "race-a", "text-to-image", 7)
	_, gB := prepareBudgetGrant(t, r, "turn-a", "race-b", "text-to-image", 7)

	type outcome struct {
		request string
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, request := range []string{gA.RequestID, gB.RequestID} {
		wg.Add(1)
		go func(requestID string) {
			defer wg.Done()
			<-start
			_, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-a", requestID, plan.ID,
				executiongrant.BudgetActorPrefix+plan.ID, 7)
			results <- outcome{request: requestID, err: err}
		}(request)
	}
	close(start)
	wg.Wait()
	close(results)

	var won []string
	var over int
	for result := range results {
		switch {
		case result.err == nil:
			won = append(won, result.request)
		case errors.Is(result.err, budget.ErrOverLimit):
			over++
		default:
			t.Fatalf("unexpected concurrent result for %s: %v", result.request, result.err)
		}
	}
	if len(won) != 1 || over != 1 {
		t.Fatalf("winners=%v over_budget=%d", won, over)
	}
	var reservations int
	var reservedCredits int
	if err := r.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(credits), 0) FROM budget_reservations WHERE state = 'reserved'`).
		Scan(&reservations, &reservedCredits); err != nil || reservations != 1 || reservedCredits != 7 {
		t.Fatalf("reservations = %d credits=%d err=%v", reservations, reservedCredits, err)
	}
	approved, pending := 0, 0
	for _, request := range []string{gA.RequestID, gB.RequestID} {
		grant, err := r.GetExecutionGrant(ctx, "owner-a", request)
		if err != nil {
			t.Fatal(err)
		}
		if grant.Status == executiongrant.Approved {
			approved++
		} else if grant.Status == executiongrant.Pending {
			pending++
		}
	}
	if approved != 1 || pending != 1 {
		t.Fatalf("grants approved=%d pending=%d", approved, pending)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || summary.RemainingCredits != 3 || summary.ReservedCredits != 7 {
		t.Fatalf("summary = %+v found=%v err=%v", summary, found, err)
	}
}

// A step larger than what remains, a browser-source grant, and another owner's
// request never reserve or approve anything.
func TestProductionBudgetApprovalRefusalsLeaveThePlanUntouched(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := budgetTestPlan(t, r, "pb-refuse", 10, "text-to-image")
	_, tooBig := prepareBudgetGrant(t, r, "turn-a", "big", "text-to-image", 11)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-a", tooBig.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 11); !errors.Is(err, budget.ErrOverLimit) {
		t.Fatalf("over-limit step: %v", err)
	}
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-b", tooBig.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, 1); err == nil {
		t.Fatal("another owner approved the grant")
	}
	stored, err := r.GetExecutionGrant(ctx, "owner-a", tooBig.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("refused grant changed: %+v err=%v", stored, err)
	}
	var reservations int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM budget_reservations`).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("refusals reserved credits: %d err=%v", reservations, err)
	}
}
