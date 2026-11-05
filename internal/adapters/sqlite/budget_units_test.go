package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func tripoCredits(whole int) budget.Amount {
	return budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: credits(whole)}
}

func hyper3DAmount(micros int64) budget.Amount {
	return budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: micros}
}

// unitPlan creates and approves a plan with per-unit limits for session-a.
func unitPlan(t *testing.T, r *Repository, id string, limits ...budget.Amount) budget.ProductionBudget {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	steps := make([]budget.PlanStep, 0, len(limits))
	for _, limit := range limits {
		steps = append(steps, budget.PlanStep{Capability: "text-to-3d", Purpose: limit.Unit, Estimate: limit})
	}
	stored, err := r.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: "session-a",
		Plan:   budget.Plan{Title: "分单位", Steps: steps},
		Limits: limits, State: budget.StatePending, CreatedAt: now,
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

// #120: a plan limits, reserves and compares each credit unit on its own. A
// step only fits in what its own unit has left; another unit's room never
// covers it, and a unit the plan does not budget has no room at all.
func TestProductionBudgetReservesEachUnitSeparately(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := unitPlan(t, r, "pb-units", tripoCredits(30), budget.Legacy(10))
	if len(plan.Limits) != 2 || plan.Limits[0].Unit != budget.LegacyUnit || plan.Limits[1].Unit != "tripo.credit" {
		t.Fatalf("stored limits = %+v", plan.Limits)
	}
	actor := executiongrant.BudgetActorPrefix + plan.ID
	approve := func(suffix string, amount budget.Amount) error {
		t.Helper()
		i, g := prepareBudgetGrant(t, r, "turn-a", suffix, "text-to-3d", 0)
		_, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID, actor, amount)
		return err
	}
	if err := approve("tripo-1", tripoCredits(20)); err != nil {
		t.Fatalf("first Tripo step: %v", err)
	}
	// 10 Tripo credits are left; the 10 legacy credits never make up the rest.
	if err := approve("tripo-2", tripoCredits(20)); !errors.Is(err, budget.ErrOverLimit) {
		t.Fatalf("second Tripo step beyond its unit: %v", err)
	}
	if err := approve("legacy", budget.Legacy(7)); err != nil {
		t.Fatalf("legacy step: %v", err)
	}
	if err := approve("hyper3d", hyper3DAmount(500000)); !errors.Is(err, budget.ErrOverLimit) {
		t.Fatalf("a step in an unbudgeted unit: %v", err)
	}
	// A step that costs nothing fits any plan, whatever its unit.
	if err := approve("free", hyper3DAmount(0)); err != nil {
		t.Fatalf("free step: %v", err)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || len(summary.Units) != 2 {
		t.Fatalf("summary = %+v found=%v err=%v", summary, found, err)
	}
	tripo, _ := summary.Unit("tripo.credit")
	legacy, _ := summary.Unit(budget.LegacyUnit)
	if tripo.Limit != credits(30) || tripo.Reserved != credits(20) || tripo.Remaining != credits(10) || tripo.UnitLabel != "Tripo credits" ||
		legacy.Limit != credits(10) || legacy.Reserved != credits(7) || legacy.Remaining != credits(3) {
		t.Fatalf("per-unit summary = %+v", summary.Units)
	}
}

// A plan is used up only when every unit is: a unit with room left keeps it
// active for the steps of that unit.
func TestProductionBudgetExhaustsOnlyWhenEveryUnitIsUsedUp(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	plan := unitPlan(t, r, "pb-units-exhaust", tripoCredits(20), hyper3DAmount(500000))
	actor := executiongrant.BudgetActorPrefix + plan.ID
	spend := func(suffix string, amount budget.Amount) {
		t.Helper()
		i, g := prepareBudgetGrant(t, r, "turn-a", suffix, "text-to-3d", 0)
		if _, err := r.ApproveExecutionGrantWithBudget(ctx, i.OwnerID, g.RequestID, plan.ID, actor, amount); err != nil {
			t.Fatal(err)
		}
		bindBudgetTask(t, r, i, g.RequestID, "task-"+suffix)
		finishBudgetTask(t, r, "task-"+suffix, generation.TaskSucceeded)
	}
	spend("tripo", tripoCredits(20))
	if exhausted, err := r.SettleBudgetReservations(ctx, time.Now().UTC()); err != nil || len(exhausted) != 0 {
		t.Fatalf("one used-up unit exhausted the plan: %+v err=%v", exhausted, err)
	}
	spend("hyper3d", hyper3DAmount(500000))
	exhausted, err := r.SettleBudgetReservations(ctx, time.Now().UTC())
	if err != nil || len(exhausted) != 1 || exhausted[0].ID != plan.ID {
		t.Fatalf("every unit used up = %+v err=%v", exhausted, err)
	}
	summary, _, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	hyper3D, _ := summary.Unit("hyper3d.credit")
	if err != nil || summary.State != budget.StateExhausted || hyper3D.Spent != 500000 || hyper3D.Remaining != 0 {
		t.Fatalf("exhausted summary = %+v err=%v", summary, err)
	}
}

// #120 migration 0078: a plan stored with one integer limit_credits moves into
// the legacy.credits bucket with every number unchanged: the limit, the step
// quotes and the reserved and charged totals.
func TestMigration0078MovesLegacyPlansIntoTheLegacyBucket(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy-77.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE go_schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	r := &Repository{db: db}
	all := migrations()
	cut := -1
	for index, migration := range all {
		if migration.version == 78 {
			cut = index
		}
	}
	if cut < 0 {
		t.Fatal("missing migration 0078")
	}
	if err = r.runMigrations(ctx, all[:cut]); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
		 VALUES ('session-a','owner-a','owner','legacy','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO production_budgets (id,owner_id,session_id,plan_json,limit_credits,state,created_at,decided_at,decided_by)
		 VALUES ('pb-legacy','owner-a','session-a','{"title":"旧计划","steps":[{"capability":"text-to-3d","purpose":"主体","estimated_credits":25}]}',
		 50,'active','2026-01-01T00:00:00Z','2026-01-01T00:01:00Z','human-a')`,
		`INSERT INTO budget_reservations (grant_request_id,budget_id,task_id,credits,state,created_at)
		 VALUES ('grant-charged','pb-legacy','task-a',25,'charged','2026-01-01T00:02:00Z'),
		        ('grant-reserved','pb-legacy','',5,'reserved','2026-01-01T00:03:00Z')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.runMigrations(ctx, all[cut:]); err != nil {
		t.Fatal(err)
	}
	stored, err := r.GetProductionBudget(ctx, "owner-a", "pb-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Limits) != 1 || stored.Limits[0] != budget.Legacy(50) {
		t.Fatalf("migrated limits = %+v", stored.Limits)
	}
	if len(stored.Plan.Steps) != 1 || stored.Plan.Steps[0].Estimate != budget.Legacy(25) {
		t.Fatalf("migrated step quote = %+v", stored.Plan.Steps)
	}
	summary, found, err := r.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || len(summary.Units) != 1 {
		t.Fatalf("migrated summary = %+v found=%v err=%v", summary, found, err)
	}
	legacy := legacyFigure(t, summary)
	if legacy.Limit != credits(50) || legacy.Spent != credits(25) || legacy.Reserved != credits(5) || legacy.Remaining != credits(20) {
		t.Fatalf("migrated legacy bucket = %+v", legacy)
	}
}
