package executiongrant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
)

// fakeProductionBudgets is the plan store and atomic budget approver of one
// session. It records what the policy asked it to reserve.
type fakeProductionBudgets struct {
	*fakeGrantStore
	summary budget.Summary
	found   bool
	over    bool
	actor   string
	amount  budget.Amount
	approve int
}

// A store that only implements the per-session read keeps the old assembly
// usable: the batch projection returns an empty map and no error, so the list
// still renders without budgets instead of failing (#103 review C6).
func TestProductionBudgetSummariesFallBackWithoutBatchStore(t *testing.T) {
	service := Service{Budgets: &fakeProductionBudgets{
		summary: budget.Summary{ID: "pb-1"}, found: true,
	}}
	summaries, err := service.ProductionBudgetSummaries(context.Background(), "owner-a", []string{"session-a"})
	if err != nil || len(summaries) != 0 {
		t.Fatalf("summaries without a batch store = %+v err=%v", summaries, err)
	}
	if summaries == nil {
		t.Fatal("the batch projection returned a nil map")
	}
}

func (store *fakeProductionBudgets) CreateProductionBudget(context.Context, budget.ProductionBudget) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, ErrInvalid
}

func (store *fakeProductionBudgets) DecideProductionBudget(context.Context, string, string, string, bool, time.Time) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, ErrInvalid
}

func (store *fakeProductionBudgets) GetProductionBudget(context.Context, string, string) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, budget.ErrNotFound
}

func (store *fakeProductionBudgets) PendingProductionBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (store *fakeProductionBudgets) CurrentProductionBudget(context.Context, string, string) (budget.ProductionBudget, bool, error) {
	return budget.ProductionBudget{}, false, nil
}

func (store *fakeProductionBudgets) ProductionBudgetSummary(context.Context, string, string) (budget.Summary, bool, error) {
	return store.summary, store.found, nil
}

func (store *fakeProductionBudgets) BudgetSummaryForGrant(context.Context, string, string) (budget.Summary, bool, error) {
	return budget.Summary{}, false, nil
}

func (store *fakeProductionBudgets) ExpireProductionBudgets(context.Context, time.Time) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (store *fakeProductionBudgets) SettleBudgetReservations(context.Context, time.Time) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (store *fakeProductionBudgets) PreparePendingWithBudget(context.Context, Identity, string, string, bool) (Grant, error) {
	return Grant{}, ErrInvalid
}

func (store *fakeProductionBudgets) ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string, amount budget.Amount) (Grant, error) {
	if store.over {
		return Grant{}, budget.ErrOverLimit
	}
	if budgetID != store.summary.ID {
		return Grant{}, budget.ErrInactive
	}
	grant, err := store.fakeGrantStore.ApproveExecutionGrantByPolicy(ctx, owner, requestID, actor)
	if err != nil {
		return Grant{}, err
	}
	store.actor, store.amount = actor, amount
	store.approve++
	return grant, nil
}

// credits is a whole number of credits in millionths.
func credits(whole int) int64 { return int64(whole) * 1000000 }

// legacyUnits is the one legacy bucket of an integer-credit test plan.
func legacyUnits(limit, spent, reserved int) []budget.UnitBudget {
	return []budget.UnitBudget{budget.NewUnitBudget(budget.Legacy(limit), credits(spent), credits(reserved))}
}

func budgetPolicy() approvalpolicy.Policy {
	return approvalpolicy.New("budget", []string{"auto", "budget", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-image", "image-to-3d", "text-to-3d", "retexture"})
}

func budgetStep(capability string) (*fakeGrantStore, *fakeProductionBudgets) {
	store := policyFixture(capability)
	store.grant.Source = AgentPending
	store.grant.TriggerTurnID = "turn-a"
	store.grant.BudgetEligible = true
	store.snapshot.EstimatedCredits = 7
	budgets := &fakeProductionBudgets{fakeGrantStore: store}
	return store, budgets
}

func runBudgetPolicy(t *testing.T, capability string, budgets *fakeProductionBudgets, store *fakeGrantStore) (PolicyResult, error) {
	t.Helper()
	service := Service{Grants: budgets, Prepared: budgets, Policy: budgetPolicy(), Budgets: budgets}
	return service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "budget")
}

// #243: only an active plan in budget mode that explicitly reserves the
// requested number of steps at the same Offering continues the series.
func TestBudgetContinuationRequiresAnActivePlanThatCoversEveryView(t *testing.T) {
	summary := budget.Summary{ID: "pb-1", State: budget.StateActive, Plan: budget.Plan{Steps: []budget.PlanStep{
		{Capability: "image-to-image", OfferingID: "image-offering", Estimate: budget.Legacy(4)},
		{Capability: "image-to-image", OfferingID: "image-offering", Estimate: budget.Legacy(4)},
		{Capability: "image-to-image", OfferingID: "image-offering", Estimate: budget.Legacy(4)},
	}}}
	budgets := &fakeProductionBudgets{summary: summary, found: true}
	service := Service{Budgets: budgets, Policy: budgetPolicy(), Modes: &modeReader{mode: "budget"}}
	for _, tc := range []struct {
		name     string
		mode     string
		offering string
		count    int
		found    bool
		state    string
		want     bool
	}{
		{"covered", "budget", "image-offering", 3, true, budget.StateActive, true},
		{"too many views", "budget", "image-offering", 4, true, budget.StateActive, false},
		{"another Offering", "budget", "other", 3, true, budget.StateActive, false},
		{"auto mode", "auto", "image-offering", 3, true, budget.StateActive, false},
		{"strict mode", "manual", "image-offering", 3, true, budget.StateActive, false},
		{"no plan", "budget", "image-offering", 3, false, budget.StateActive, false},
		{"exhausted plan", "budget", "image-offering", 3, true, budget.StateExhausted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budgets.found, budgets.summary.State = tc.found, tc.state
			service.Modes = &modeReader{mode: tc.mode}
			got, err := service.BudgetContinuation(context.Background(), "owner-a", "session-a", "image-to-image", tc.offering, tc.count)
			if err != nil || got != tc.want {
				t.Fatalf("continuation = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	service.Modes = nil
	if got, err := service.BudgetContinuation(context.Background(), "owner-a", "session-a", "image-to-image", "image-offering", 3); err != nil || got {
		t.Fatalf("without a mode reader = %v, %v", got, err)
	}
}

// Branch 2: without an active plan, manual's rules decide. A step manual leaves
// to a person reports no_plan so the Agent proposes a plan first; a step manual
// approves itself still runs, under the budget mode's actor.
func TestBudgetModeWithoutAPlanFallsBackToManual(t *testing.T) {
	store, budgets := budgetStep("image-to-3d")
	result, err := runBudgetPolicy(t, "image-to-3d", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonNoPlan || result.Budget != nil {
		t.Fatalf("manual step without a plan = %+v err=%v", result, err)
	}
	if store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("a step manual would card was approved: %+v", store.grant)
	}

	store, budgets = budgetStep("text-to-image")
	result, err = runBudgetPolicy(t, "text-to-image", budgets, store)
	if err != nil || result.Outcome != PolicyOutcomeApproved || result.Reason != "" ||
		store.grant.DecidedBy != "policy:budget" || store.approved != 1 {
		t.Fatalf("manual auto step without a plan = %+v err=%v grant=%+v", result, err, store.grant)
	}
}

// Branch 3: a capability (or pinned Offering) outside the approved plan's
// steps and correction capabilities waits for a person, with the plan summary.
func TestBudgetModeLeavesOffPlanStepsToThePerson(t *testing.T) {
	store, budgets := budgetStep("image-to-3d")
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(100, 0, 20), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-3d", Estimate: budget.Legacy(20)},
	}}}
	result, err := runBudgetPolicy(t, "image-to-3d", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonOffPlan || result.Budget == nil || result.Budget.ID != "pb_1" {
		t.Fatalf("off-plan step = %+v err=%v", result, err)
	}
	if store.approved != 0 || budgets.approve != 0 {
		t.Fatalf("off-plan step spent the budget: store=%d budget=%d", store.approved, budgets.approve)
	}
}

// Branch 4: a planned step whose estimate does not fit what remains waits for a
// person and is told why and what is left.
func TestBudgetModeLeavesStepsBeyondTheLimitToThePerson(t *testing.T) {
	store, budgets := budgetStep("text-to-image")
	budgets.found = true
	budgets.over = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(20, 12, 7), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(7)},
	}}}
	result, err := runBudgetPolicy(t, "text-to-image", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonOver || result.Budget == nil || result.Budget.Units[0].Remaining != credits(1) {
		t.Fatalf("over-budget step = %+v err=%v", result, err)
	}
	if store.approved != 0 || budgets.approve != 0 {
		t.Fatalf("over-budget step spent the budget: store=%d budget=%d", store.approved, budgets.approve)
	}
}

// An exhausted plan no longer approves anything, including a capability that
// manual mode would approve on its own: the person approved a total, and a
// further step needs a new decision.
func TestBudgetModeLeavesEveryStepOfAnExhaustedPlanToThePerson(t *testing.T) {
	store, budgets := budgetStep("text-to-image")
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateExhausted, Units: legacyUnits(20, 20, 0), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(7)},
	}}}
	result, err := runBudgetPolicy(t, "text-to-image", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonOver || result.Budget == nil || result.Budget.State != budget.StateExhausted {
		t.Fatalf("exhausted plan step = %+v err=%v", result, err)
	}
	if store.approved != 0 || budgets.approve != 0 {
		t.Fatalf("an exhausted plan approved a step: store=%d budget=%d", store.approved, budgets.approve)
	}
}

// A metered step the server could not price has no number to reserve; it must
// wait for a person instead of spending a zero-sized reservation from the
// approved total. An unmetered zero-cost step stays genuinely free.
func TestBudgetModeLeavesUnpricedMeteredStepsToThePerson(t *testing.T) {
	store, budgets := budgetStep("text-to-image")
	store.snapshot.CreditsMetered = true
	store.snapshot.EstimatedCredits = 0
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(100, 0, 0), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(5)},
	}}}
	result, err := runBudgetPolicy(t, "text-to-image", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonUnpriced || result.Budget == nil {
		t.Fatalf("unpriced metered step = %+v err=%v", result, err)
	}
	if store.approved != 0 || budgets.approve != 0 {
		t.Fatalf("an unpriced step spent the budget: store=%d budget=%d", store.approved, budgets.approve)
	}

	store, budgets = budgetStep("text-to-image")
	store.snapshot.CreditsMetered = false
	store.snapshot.EstimatedCredits = 0
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(100, 0, 0), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(0)},
	}}}
	result, err = runBudgetPolicy(t, "text-to-image", budgets, store)
	if err != nil || result.Outcome != PolicyOutcomeApproved || budgets.approve != 1 || budgets.amount != budget.Legacy(0) {
		t.Fatalf("free step = %+v err=%v approvals=%d amount=%+v", result, err, budgets.approve, budgets.amount)
	}
}

// Branch 5: a planned step inside the remaining budget is approved by the plan
// in the same transaction that reserves its server-side estimate, and the
// result carries the plan's remaining credits.
func TestBudgetModeApprovesAPlannedStepAndReportsWhatIsLeft(t *testing.T) {
	store, budgets := budgetStep("text-to-image")
	// The estimate includes extra credits and is the frozen server quote.
	store.snapshot.ExtraCredits = 3
	store.snapshot.EstimatedCredits = 10
	budgets.found = true
	before := budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(100, 20, 10), Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(10)},
	}}}
	budgets.summary = before
	service := Service{Grants: budgets, Prepared: budgets, Policy: budgetPolicy(), Budgets: budgets}
	// After approval the plan reports the step reserved.
	budgets.summary.Units = legacyUnits(100, 20, 20)
	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "budget")
	if err != nil || result.Outcome != PolicyOutcomeApproved {
		t.Fatalf("planned step = %+v err=%v", result, err)
	}
	if budgets.approve != 1 || budgets.amount != budget.Legacy(10) ||
		budgets.actor != BudgetActorPrefix+"pb_1" || store.grant.DecidedBy != BudgetActorPrefix+"pb_1" {
		t.Fatalf("reservation = approvals=%d amount=%+v actor=%q grant=%+v",
			budgets.approve, budgets.amount, budgets.actor, store.grant)
	}
	if result.Budget == nil || result.Budget.Units[0].Remaining != credits(60) || result.Budget.Units[0].Reserved != credits(20) {
		t.Fatalf("result budget = %+v", result.Budget)
	}
}

// A deployment without plan storage behaves like manual in budget mode rather
// than failing: no plan can authorize a step.
func TestBudgetModeWithoutPlanStorageFallsBackToManual(t *testing.T) {
	store, _ := budgetStep("image-to-3d")
	service := Service{Grants: store, Prepared: store, Policy: budgetPolicy()}
	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "budget")
	if !errors.Is(err, ErrHumanRequired) || result.Reason != BudgetReasonNoPlan || store.approved != 0 {
		t.Fatalf("no plan storage = %+v err=%v approved=%d", result, err, store.approved)
	}
}

// Manual and auto keep today's rules for the same Agent step: budget
// eligibility alone never changes them.
func TestBudgetEligibilityDoesNotChangeManualOrAuto(t *testing.T) {
	manualStore, _ := budgetStep("image-to-3d")
	manual := Service{Grants: manualStore, Prepared: manualStore, Policy: budgetPolicy()}
	if _, err := manual.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual image-to-3d: %v", err)
	}
	if manualStore.approved != 0 {
		t.Fatal("manual mode approved a step the budget store could have covered")
	}

	autoStore, _ := budgetStep("image-to-3d")
	auto := Service{Grants: autoStore, Prepared: autoStore, Policy: budgetPolicy()}
	result, err := auto.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "auto")
	if err != nil || result.Outcome != PolicyOutcomeApproved || autoStore.approved != 1 ||
		autoStore.grant.DecidedBy != PolicyActorPrefix+"auto" {
		t.Fatalf("auto image-to-3d = %+v err=%v grant=%+v", result, err, autoStore.grant)
	}
}
