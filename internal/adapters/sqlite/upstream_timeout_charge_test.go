package sqlite

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #136: an accepted job that timed out (in execution or in the upstream
// queue) fails ambiguous, because the upstream may still run and bill it. Its
// budget reservation is then charged, not released, exactly like an unknown
// submission; a certain failure is still released.
func TestTimedOutAcceptedJobKeepsItsBudgetReservationCharged(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		failure   generation.Failure
		wantState string
		wantSpent int
	}{
		{"queue timeout", generation.Failure{Class: generation.FailureUpstreamQueueTimeout, Ambiguous: true}, "charged", 7},
		{"job timeout", generation.Failure{Class: generation.FailureTimedOut, Ambiguous: true}, "charged", 7},
		{"unknown submission", generation.Failure{Class: generation.FailureSubmissionUnknown, Ambiguous: true}, "charged", 7},
		{"certain failure", generation.Failure{Class: generation.FailureRejected}, "released", 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			r := openGrantTestRepo(t)
			insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
			plan := budgetTestPlan(t, r, "pb-timeout", 20, "text-to-3d")
			i, g := prepareBudgetGrant(t, r, "turn-a", "timeout", "text-to-3d", 7)
			if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID,
				executiongrant.BudgetActorPrefix+plan.ID, budget.Legacy(7)); err != nil {
				t.Fatal(err)
			}
			bindBudgetTask(t, r, i, g.RequestID, "task-timeout")
			failure, err := json.Marshal(testCase.failure)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.db.Exec(`UPDATE generation_tasks SET state = ?, failure_json = ?, updated_at = ? WHERE id = ?`,
				generation.TaskFailed, string(failure), time.Now().UTC().Format(time.RFC3339Nano), "task-timeout"); err != nil {
				t.Fatal(err)
			}
			if _, err := r.SettleBudgetReservations(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if state := reservationState(t, r, g.RequestID); state != testCase.wantState {
				t.Fatalf("reservation = %q, want %q", state, testCase.wantState)
			}
			summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
			legacy, ok := summary.Unit(budget.LegacyUnit)
			if err != nil || !found || !ok || legacy.Spent != budget.Legacy(testCase.wantSpent).Micros || legacy.Reserved != 0 {
				t.Fatalf("summary = %+v found=%v err=%v", summary, found, err)
			}
		})
	}
}
