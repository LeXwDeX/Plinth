package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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
		steps = append(steps, budget.PlanStep{Capability: capability, Purpose: capability, Estimate: budget.Legacy(7)})
	}
	now := time.Now().UTC()
	stored, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "制作计划", Steps: steps}, Limits: []budget.Amount{budget.Legacy(limit)},
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

// credits is a whole number of credits in millionths, the unit of a budget
// amount.
func credits(whole int) int64 { return int64(whole) * 1000000 }

// legacyFigure is the summary's legacy bucket: the integer test plans budget
// historical credits only.
func legacyFigure(t *testing.T, summary budget.Summary) budget.UnitBudget {
	t.Helper()
	unit, found := summary.Unit(budget.LegacyUnit)
	if !found {
		t.Fatalf("summary without a legacy bucket: %+v", summary)
	}
	return unit
}

func reservationState(t *testing.T, r *Repository, requestID string) string {
	t.Helper()
	var state string
	if err := r.db.QueryRow(`SELECT state FROM budget_reservations WHERE grant_request_id = ?`, requestID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// The exhausted/expired notice follows the committed state transition. A plan
// whose chat post fails therefore stays in the notice queue until the post is
// recorded, and a recorded notice is never posted again (#103 review B3).
func TestProductionBudgetNoticesAwaitAndAreMarkedOnce(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	now := time.Now().UTC()
	exhausted := budgetTestPlan(t, r, "pb-notice", 20, "text-to-image")
	if _, err := r.db.Exec(`UPDATE production_budgets SET state = 'exhausted', decided_at = ? WHERE id = ?`,
		now.Format(time.RFC3339Nano), exhausted.ID); err != nil {
		t.Fatal(err)
	}
	denied, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-denied-notice", OwnerID: "owner-a", SessionID: "session-a",
		Plan:   budget.Plan{Title: "人工拒绝", Steps: []budget.PlanStep{{Capability: "text-to-image", Estimate: budget.Legacy(1)}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideProductionBudget(ctx, "owner-a", denied.ID, "owner-a", false, now); err != nil {
		t.Fatal(err)
	}
	awaiting, err := r.ProductionBudgetsAwaitingNotice(ctx, 10)
	if err != nil || len(awaiting) != 1 || awaiting[0].ID != exhausted.ID {
		t.Fatalf("awaiting notices = %+v err=%v", awaiting, err)
	}
	if err := r.MarkProductionBudgetNoticeSent(ctx, "owner-a", exhausted.ID, now); err != nil {
		t.Fatal(err)
	}
	if awaiting, err := r.ProductionBudgetsAwaitingNotice(ctx, 10); err != nil || len(awaiting) != 0 {
		t.Fatalf("a recorded notice was listed again = %+v err=%v", awaiting, err)
	}

	pending, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-expired-notice", OwnerID: "owner-a", SessionID: "session-a",
		Plan:   budget.Plan{Title: "过期", Steps: []budget.PlanStep{{Capability: "text-to-image", Estimate: budget.Legacy(1)}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExpireProductionBudgets(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	awaiting, err = r.ProductionBudgetsAwaitingNotice(ctx, 10)
	if err != nil || len(awaiting) != 1 || awaiting[0].ID != pending.ID || awaiting[0].State != budget.StateDenied {
		t.Fatalf("expired notice queue = %+v err=%v", awaiting, err)
	}
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
				executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(7)); err != nil {
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
			if legacyFigure(t, summary).Spent != credits(testCase.wantSpent) || legacyFigure(t, summary).Reserved != credits(0) ||
				legacyFigure(t, summary).Remaining != credits(testCase.wantRemain) || summary.State != budget.StateActive {
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
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(5)); err != nil {
		t.Fatalf("agent processing step: %v", err)
	}
	stored, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || stored.Status != executiongrant.Approved || !stored.ApprovedByBudget() {
		t.Fatalf("processing grant = %+v err=%v", stored, err)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, i.OwnerID, i.SessionID)
	if err != nil || !found || legacyFigure(t, summary).Reserved != credits(5) || legacyFigure(t, summary).Remaining != credits(15) {
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
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(1)); !errors.Is(err, executiongrant.ErrConflict) {
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
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(7)); err != nil {
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
		legacyFigure(t, summary).Spent != credits(7) || legacyFigure(t, summary).Remaining != credits(0) {
		t.Fatalf("exhausted summary = %+v found=%v err=%v", summary, found, err)
	}

	_, next := prepareBudgetGrant(t, r, "turn-a", "after-exhaust", "text-to-image", 1)
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-a", next.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(1)); !errors.Is(err, budget.ErrInactive) {
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
		Plan:   budget.Plan{Title: "不需要", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now,
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
		Plan:   budget.Plan{Title: "过期", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: created,
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
		executiongrant.BudgetActorPrefix+old.ID, budget.Legacy(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id='turn-a'`); err != nil {
		t.Fatal(err)
	}
	pending, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-new", OwnerID: "owner-a", SessionID: "session-a",
		Plan:   budget.Plan{Title: "新计划", Steps: []budget.PlanStep{{Capability: "text-to-image"}}},
		Limits: []budget.Amount{budget.Legacy(30)}, State: budget.StatePending, CreatedAt: time.Now().UTC(),
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
		legacyFigure(t, summary).Reserved != credits(0) || legacyFigure(t, summary).Spent != credits(0) {
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
				executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(7))
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
	var reservedCredits int64
	if err := r.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(amount_micros), 0) FROM budget_reservations WHERE state = 'reserved'`).
		Scan(&reservations, &reservedCredits); err != nil || reservations != 1 || reservedCredits != credits(7) {
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
	if err != nil || !found || legacyFigure(t, summary).Remaining != credits(3) || legacyFigure(t, summary).Reserved != credits(7) {
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
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(11)); !errors.Is(err, budget.ErrOverLimit) {
		t.Fatalf("over-limit step: %v", err)
	}
	if _, err := r.ApproveExecutionGrantWithBudget(ctx, "owner-b", tooBig.RequestID, plan.ID,
		executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(1)); err == nil {
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

func insertBudgetSession(t *testing.T, r *Repository, id string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions
 (id, owner_id, owner_name, title, created_at, updated_at)
 VALUES (?, 'owner-a', 'owner', 'test', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, id); err != nil {
		t.Fatal(err)
	}
}

func budgetPlanForSession(t *testing.T, r *Repository, id, sessionID string, limit int) budget.ProductionBudget {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	stored, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: sessionID,
		Plan: budget.Plan{Title: "批量计划", Steps: []budget.PlanStep{
			{Capability: "text-to-image", Purpose: "text-to-image", Estimate: budget.Legacy(7)},
		}},
		Limits: []budget.Amount{budget.Legacy(limit)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := r.DecideProductionBudget(ctx, "owner-a", stored.ID, "human-a", true, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return active
}

func insertBudgetReservation(t *testing.T, r *Repository, requestID, budgetID string, amount int, state string, createdAt time.Time) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES (?, ?, '', 'legacy.credits', ?, ?, ?)`, requestID, budgetID, credits(amount), state, createdAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

// The list projection reads the same summaries as the per-session method in
// one batch, omits sessions without a current plan and stays owner-scoped.
func TestProductionBudgetSummariesMatchPerSessionReads(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertBudgetSession(t, r, "session-b")
	insertBudgetSession(t, r, "session-c")
	planA := budgetTestPlan(t, r, "pb-batch-a", 20, "text-to-image")
	planB := budgetPlanForSession(t, r, "pb-batch-b", "session-b", 30)
	created := time.Now().UTC()
	insertBudgetReservation(t, r, "reservation-a-charged", planA.ID, 7, budget.ReservationCharged, created)
	insertBudgetReservation(t, r, "reservation-a-reserved", planA.ID, 5, budget.ReservationReserved, created)
	insertBudgetReservation(t, r, "reservation-b-charged", planB.ID, 4, budget.ReservationCharged, created)

	summaries, err := r.ProductionBudgetSummaries(ctx, "owner-a",
		[]string{"session-a", "session-b", "session-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v, want the two sessions with a current plan", summaries)
	}
	if _, found := summaries["session-c"]; found {
		t.Fatal("a session without a plan was not omitted")
	}
	for _, sessionID := range []string{"session-a", "session-b"} {
		single, found, err := r.ProductionBudgetSummary(ctx, "owner-a", sessionID)
		if err != nil || !found {
			t.Fatalf("per-session summary %s: %+v found=%v err=%v", sessionID, single, found, err)
		}
		if batch := summaries[sessionID]; !reflect.DeepEqual(batch, single) {
			t.Fatalf("batch %s = %+v, per-session = %+v", sessionID, batch, single)
		}
	}
	if summary := summaries["session-a"]; summary.ID != planA.ID || legacyFigure(t, summary).Spent != credits(7) ||
		legacyFigure(t, summary).Reserved != credits(5) || legacyFigure(t, summary).Remaining != credits(8) || summary.State != budget.StateActive {
		t.Fatalf("session-a summary = %+v", summary)
	}
	if summary := summaries["session-b"]; summary.ID != planB.ID || legacyFigure(t, summary).Spent != credits(4) ||
		legacyFigure(t, summary).Reserved != credits(0) || legacyFigure(t, summary).Remaining != credits(26) {
		t.Fatalf("session-b summary = %+v", summary)
	}

	empty, err := r.ProductionBudgetSummaries(ctx, "owner-a", nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %+v err=%v", empty, err)
	}
	foreign, err := r.ProductionBudgetSummaries(ctx, "owner-b", []string{"session-a", "session-b"})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("another owner read summaries = %+v err=%v", foreign, err)
	}
}

// A long session list crosses SQLite's bound-parameter limit: both queries
// split into batches of productionBudgetSummaryBatch.
func TestProductionBudgetSummariesBatchBeyondTheParameterLimit(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	const sessions = productionBudgetSummaryBatch + 1
	stamp := formatTime(time.Now().UTC())
	planJSON := `{"title":"批量","steps":[]}`
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	sessionIDs := make([]string, 0, sessions)
	for index := 0; index < sessions; index++ {
		sessionID := "batch-session-" + strconv.Itoa(index)
		sessionIDs = append(sessionIDs, sessionID)
		if _, err := tx.Exec(`INSERT INTO go_chat_sessions
 (id, owner_id, owner_name, title, created_at, updated_at)
 VALUES (?, 'owner-a', 'owner', 'test', ?, ?)`, sessionID, stamp, stamp); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		budgetID := "pb-" + sessionID
		if _, err := tx.Exec(`INSERT INTO production_budgets
 (id, owner_id, session_id, plan_json, limits_json, state, created_at, decided_by)
 VALUES (?, 'owner-a', ?, ?, '[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"10"}]', 'active', ?, 'human-a')`,
			budgetID, sessionID, planJSON, stamp); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if index%2 == 0 {
			if _, err := tx.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES (?, ?, '', 'legacy.credits', 2000000, 'charged', ?)`, "reservation-"+sessionID, budgetID, stamp); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	summaries, err := r.ProductionBudgetSummaries(ctx, "owner-a", sessionIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != sessions {
		t.Fatalf("summaries = %d, want %d", len(summaries), sessions)
	}
	// Even indexes reserved and spent 2 credits; odd indexes have no charges.
	for index, wantSpent := range map[int]int{0: 2, 1: 0, sessions - 1: 2} {
		summary, found := summaries["batch-session-"+strconv.Itoa(index)]
		if !found || legacyFigure(t, summary).Spent != credits(wantSpent) || legacyFigure(t, summary).Limit != credits(10) {
			t.Fatalf("batch-session-%d = %+v found=%v, want spent %d", index, summary, found, wantSpent)
		}
	}
}

// The batch read reports a store failure instead of returning an empty map,
// so the caller can fall back to per-session reads (#103 review C6).
func TestProductionBudgetSummariesReturnsStoreErrors(t *testing.T) {
	r := openGrantTestRepo(t)
	if err := r.db.Close(); err != nil {
		t.Fatal(err)
	}
	summaries, err := r.ProductionBudgetSummaries(context.Background(), "owner-a", []string{"session-a"})
	if err == nil {
		t.Fatal("a closed store returned no error")
	}
	if summaries != nil {
		t.Fatalf("summaries on failure = %+v, want nil", summaries)
	}
}
