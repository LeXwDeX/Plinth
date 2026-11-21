package executiongrant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// legacyPlanJSON is a production plan as v1.8.0 stored it (#434): the Agent's
// model name in each step's offering_id, for the main steps and the declared
// contingency step, with the plan's own per-unit estimates.
const legacyPlanJSON = `{
  "title": "三视图主体",
  "steps": [
    {"capability": "image-to-3d", "offering_id": "meshy-7.1-multiview", "purpose": "三视图主体",
     "estimate": {"unit": "legacy.credits", "unit_label": "legacy.credits", "amount": "40"}},
    {"capability": "text-to-3d", "offering_id": "meshy-7.1", "purpose": "主体",
     "estimate": {"unit": "legacy.credits", "unit_label": "legacy.credits", "amount": "20"}}
  ],
  "correction_capabilities": ["retexture"],
  "contingencies": [
    {"id": "repair", "trigger": ["self_check"],
     "step": {"capability": "retexture", "offering_id": "meshy-7.1", "purpose": "修补",
              "estimate": {"unit": "legacy.credits", "unit_label": "legacy.credits", "amount": "10"}}}
  ]
}`

// legacyPlanPins resolves the names of legacyPlanJSON the way the runtime
// Catalog does: the same model name of a capability maps to its one Offering.
var legacyPlanPins = map[string]string{
	"image-to-3d/meshy-7.1-multiview": "meshy/meshy-7.1-multiview/image-to-3d",
	"text-to-3d/meshy-7.1":            "meshy/meshy-7.1/text-to-3d",
	"retexture/meshy-7.1":             "meshy/meshy-7.1/retexture",
}

func legacyPlanPin(capability, pinned string) (string, bool) {
	offeringID, found := legacyPlanPins[capability+"/"+pinned]
	return offeringID, found
}

// legacyPlan decodes the stored plan_json shape, not a hand-built plan, so the
// test reads what a v1.8.0 server wrote.
func legacyPlan(t *testing.T) budget.Plan {
	t.Helper()
	var plan budget.Plan
	if err := json.Unmarshal([]byte(legacyPlanJSON), &plan); err != nil {
		t.Fatalf("decode the stored plan: %v", err)
	}
	return plan
}

// legacyPlanBudgets is one active plan stored before #426, returned by every
// plan read the grant service makes. It also implements the optional plan
// storage interfaces, so the wrapper's delegation is exercised.
type legacyPlanBudgets struct {
	*fakeProductionBudgets
	production   budget.ProductionBudget
	notices      []string
	released     []string
	summaryCalls int
}

func newLegacyPlanBudgets(t *testing.T) *legacyPlanBudgets {
	t.Helper()
	plan := legacyPlan(t)
	production := budget.ProductionBudget{
		ID: "pb_legacy", OwnerID: "owner-a", SessionID: "session-a", Plan: plan,
		Limits: []budget.Amount{budget.Legacy(100)}, State: budget.StateActive,
		CreatedAt: time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC),
	}
	return &legacyPlanBudgets{
		fakeProductionBudgets: &fakeProductionBudgets{summary: budget.Summary{
			ID: production.ID, State: production.State, Plan: plan,
			Units: legacyUnits(100, 0, 0), CreatedAt: production.CreatedAt,
		}, found: true},
		production: production,
	}
}

func (store *legacyPlanBudgets) GetProductionBudget(context.Context, string, string) (budget.ProductionBudget, error) {
	return store.production, nil
}

func (store *legacyPlanBudgets) PendingProductionBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	pending := store.production
	pending.State = budget.StatePending
	return []budget.ProductionBudget{pending}, nil
}

func (store *legacyPlanBudgets) CurrentProductionBudget(context.Context, string, string) (budget.ProductionBudget, bool, error) {
	return store.production, true, nil
}

func (store *legacyPlanBudgets) BudgetSummaryForGrant(context.Context, string, string) (budget.Summary, bool, error) {
	return store.fakeProductionBudgets.summary, true, nil
}

func (store *legacyPlanBudgets) ProductionBudgetSummaries(context.Context, string, []string) (map[string]budget.Summary, error) {
	store.summaryCalls++
	return map[string]budget.Summary{"session-a": store.fakeProductionBudgets.summary}, nil
}

func (store *legacyPlanBudgets) ProductionBudgetsAwaitingNotice(context.Context, int) ([]budget.ProductionBudget, error) {
	return []budget.ProductionBudget{store.production}, nil
}

func (store *legacyPlanBudgets) MarkProductionBudgetNoticeSent(_ context.Context, _ string, budgetID string, _ time.Time) error {
	store.notices = append(store.notices, budgetID)
	return nil
}

func (store *legacyPlanBudgets) ReleaseUnboundBudgetReservation(_ context.Context, _, requestID string) error {
	store.released = append(store.released, requestID)
	return nil
}

// #434: every read of a plan stored before #426 resolves its model-name pins,
// so Allows and Covers compare the Offering ID a grant runs. A pin the Catalog
// cannot identify stays as stored and remains off-plan, and the wrapped store's
// bytes are never rewritten.
func TestResolvedPlanPinsCoverALegacyPlanOnEveryRead(t *testing.T) {
	budgets := newLegacyPlanBudgets(t)
	store := WithResolvedPlanPins(budgets, legacyPlanPin)
	service := Service{Budgets: store, Policy: budgetPolicy(), Modes: &modeReader{mode: "budget"}}
	ctx := context.Background()

	for name, plan := range map[string]budget.Plan{
		"get": func() budget.Plan {
			production, err := service.GetBudget(ctx, "owner-a", "pb_legacy")
			if err != nil {
				t.Fatalf("GetBudget: %v", err)
			}
			return production.Plan
		}(),
		"pending": func() budget.Plan {
			pending, err := service.PendingBudgets(ctx, "owner-a", "session-a")
			if err != nil || len(pending) != 1 {
				t.Fatalf("PendingBudgets = %+v err=%v", pending, err)
			}
			return pending[0].Plan
		}(),
		"summary": func() budget.Plan {
			summary, found, err := service.BudgetSummary(ctx, "owner-a", "session-a")
			if err != nil || !found {
				t.Fatalf("BudgetSummary found=%v err=%v", found, err)
			}
			return summary.Plan
		}(),
		"batch": func() budget.Plan {
			summaries, err := service.ProductionBudgetSummaries(ctx, "owner-a", []string{"session-a"})
			if err != nil || len(summaries) != 1 {
				t.Fatalf("ProductionBudgetSummaries = %+v err=%v", summaries, err)
			}
			return summaries["session-a"].Plan
		}(),
		"awaiting notice": func() budget.Plan {
			awaiting, err := service.ProductionBudgetsAwaitingNotice(ctx, 10)
			if err != nil || len(awaiting) != 1 {
				t.Fatalf("ProductionBudgetsAwaitingNotice = %+v err=%v", awaiting, err)
			}
			return awaiting[0].Plan
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if !plan.Allows("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d") ||
				!plan.Covers("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", 1) {
				t.Fatalf("legacy pin not resolved: %+v", plan.Steps)
			}
			if !plan.Allows("retexture", "meshy/meshy-7.1/retexture") {
				t.Fatalf("legacy contingency pin not resolved: %+v", plan.Contingencies)
			}
			if plan.Allows("image-to-3d", "meshy-7.1-multiview") {
				t.Fatal("the plan still compares the stored model name as an Offering ID")
			}
		})
	}
	if budgets.summaryCalls != 1 {
		t.Fatalf("batch reads through the wrapper = %d, want the wrapped store's", budgets.summaryCalls)
	}
	if err := service.MarkProductionBudgetNoticeSent(ctx, "owner-a", "pb_legacy"); err != nil ||
		len(budgets.notices) != 1 {
		t.Fatalf("notice marking = %v, calls=%v", err, budgets.notices)
	}
	if err := service.AbandonBudgetReservation(ctx, "owner-a", "request-a"); err != nil ||
		len(budgets.released) != 1 {
		t.Fatalf("reservation release = %v, calls=%v", err, budgets.released)
	}

	// Reading resolved plans never rewrote the stored plan.
	if budgets.production.Plan.Steps[0].OfferingID != "meshy-7.1-multiview" ||
		budgets.production.Plan.Contingencies[0].Step.OfferingID != "meshy-7.1" {
		t.Fatalf("the store's plan changed: %+v", budgets.production.Plan)
	}
}

// A pin the Catalog does not resolve keeps the returned plan off-plan: the
// stored name must not start matching an Offering's price.
func TestResolvedPlanPinsLeaveAnUnknownPinOffPlan(t *testing.T) {
	budgets := newLegacyPlanBudgets(t)
	plan := budgets.summary.Plan
	plan.Steps[0].OfferingID = "never-declared"
	budgets.summary.Plan = plan
	budgets.production.Plan = plan
	service := Service{Budgets: WithResolvedPlanPins(budgets, legacyPlanPin)}

	summary, found, err := service.BudgetSummary(context.Background(), "owner-a", "session-a")
	if err != nil || !found {
		t.Fatalf("BudgetSummary found=%v err=%v", found, err)
	}
	if summary.Plan.Steps[0].OfferingID != "never-declared" {
		t.Fatalf("unknown pin changed to %q", summary.Plan.Steps[0].OfferingID)
	}
	if summary.Plan.Allows("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d") ||
		summary.Plan.Covers("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", 1) {
		t.Fatal("an unresolved pin covered the Offering it might have named")
	}
}

// The budget policy approves a legacy plan's pinned step in its own unit: the
// resolved pin is the Offering the grant runs, so the step is on-plan and the
// reservation goes through.
func TestBudgetModeApprovesALegacyPlansPinnedStep(t *testing.T) {
	store, budgets := budgetStep("image-to-3d")
	const offeringID = "meshy/meshy-7.1-multiview/image-to-3d"
	store.grant.OfferingID, store.snapshot.OfferingID = offeringID, offeringID
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_legacy", State: budget.StateActive, Units: legacyUnits(100, 0, 0),
		Plan: legacyPlan(t)}
	service := Service{Grants: budgets, Prepared: budgets, Policy: budgetPolicy(),
		Budgets: WithResolvedPlanPins(budgets, legacyPlanPin)}

	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "budget")
	if err != nil || result.Outcome != PolicyOutcomeApproved {
		t.Fatalf("legacy pinned step = %+v err=%v", result, err)
	}
	if budgets.approve != 1 || budgets.amount != budget.Legacy(7) || budgets.actor != BudgetActorPrefix+"pb_legacy" {
		t.Fatalf("reservation = approvals=%d amount=%+v actor=%q",
			budgets.approve, budgets.amount, budgets.actor)
	}
}

// An old plan whose pin the Catalog does not identify still leaves the step to
// a person with off_plan.
func TestBudgetModeLeavesALegacyPlansUnresolvedPinOffPlan(t *testing.T) {
	store, budgets := budgetStep("image-to-3d")
	store.grant.OfferingID = "meshy/meshy-7.1-multiview/image-to-3d"
	store.snapshot.OfferingID = store.grant.OfferingID
	plan := legacyPlan(t)
	plan.Steps[0].OfferingID = "never-declared"
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_legacy", State: budget.StateActive, Units: legacyUnits(100, 0, 0), Plan: plan}
	service := Service{Grants: budgets, Prepared: budgets, Policy: budgetPolicy(),
		Budgets: WithResolvedPlanPins(budgets, legacyPlanPin)}

	result, err := service.ApproveByPolicyDecision(context.Background(), "owner-a", "request-a", "budget")
	if !errors.Is(err, ErrHumanRequired) || result.Outcome != PolicyOutcomeHuman ||
		result.Reason != BudgetReasonOffPlan {
		t.Fatalf("unresolved legacy pin = %+v err=%v", result, err)
	}
	if budgets.approve != 0 {
		t.Fatal("an off-plan step spent the plan")
	}
}

// A wrapper without a resolver is the store itself, and a wrapped store with no
// optional interfaces reports no batch plans and no notices, as the grant
// service does without them.
func TestResolvedPlanPinsKeepTheStoresOptionalBehavior(t *testing.T) {
	budgets := &fakeProductionBudgets{summary: budget.Summary{ID: "pb_1"}, found: true}
	if WithResolvedPlanPins(budgets, nil) != budgets {
		t.Fatal("a nil resolver must not wrap the store")
	}
	service := Service{Budgets: WithResolvedPlanPins(budgets, legacyPlanPin)}
	summaries, err := service.ProductionBudgetSummaries(context.Background(), "owner-a", []string{"session-a"})
	if err != nil || len(summaries) != 0 || summaries == nil {
		t.Fatalf("batch reads without the wrapped interface = %+v err=%v", summaries, err)
	}
	if awaiting, err := service.ProductionBudgetsAwaitingNotice(context.Background(), 10); err != nil || awaiting != nil {
		t.Fatalf("notices without the wrapped interface = %+v err=%v", awaiting, err)
	}
	if err := service.MarkProductionBudgetNoticeSent(context.Background(), "owner-a", "pb_1"); err != nil {
		t.Fatalf("marking without the wrapped interface: %v", err)
	}
}
