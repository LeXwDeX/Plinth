package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #434: the plan store the grant service reads resolves the model-name pins of
// plans stored before #426 against the Offerings the runtime Catalog declares.
// The stored plan_json keeps its bytes (no migration), an unknown or ambiguous
// name stays off-plan, and a plan that already stores Offering IDs reads back
// unchanged.
func TestBudgetPlanStoreResolvesALegacyPlanAgainstTheDefaultCatalog(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "plan-pins.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatalf("load the default catalog: %v", err)
	}
	store := budgetPlanStore(repository, product.List)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)

	stored := activeLegacyPlan(t, repository, now)
	summary, found, err := store.ProductionBudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || summary.ID != stored.ID {
		t.Fatalf("summary found=%v err=%v id=%q", found, err, summary.ID)
	}
	const multiview = "meshy/meshy-7.1-multiview/image-to-3d"
	if !summary.Plan.Allows("image-to-3d", multiview) || !summary.Plan.Covers("image-to-3d", multiview, 1) {
		t.Fatalf("legacy pin not resolved for its Offering: %+v", summary.Plan.Steps)
	}
	if !summary.Plan.Allows("text-to-3d", "meshy/meshy-7.1/text-to-3d") {
		t.Fatalf("legacy contingency pin not resolved: %+v", summary.Plan.Contingencies)
	}
	if summary.Plan.Allows("text-to-image", "openai/gpt-image-2/text-to-image") {
		t.Fatal("a name the Catalog does not declare must stay off-plan")
	}
	// The store reads the plan; it never rewrites the bytes it stored.
	raw, err := repository.GetProductionBudget(ctx, "owner-a", stored.ID)
	if err != nil {
		t.Fatalf("read the stored plan: %v", err)
	}
	if raw.Plan.Steps[0].OfferingID != "meshy-7.1-multiview" ||
		raw.Plan.Contingencies[0].Step.OfferingID != "meshy-7.1" {
		t.Fatalf("the stored plan was rewritten: %+v", raw.Plan)
	}

	// A plan that already stores Offering IDs is unaffected by the read view.
	newPlan := activePlan(t, repository, "session-b", budget.Plan{Title: "新计划", Steps: []budget.PlanStep{
		{Capability: "image-to-3d", OfferingID: multiview, Purpose: "主体",
			Estimate: budget.Legacy(40)},
	}}, now.Add(time.Minute))
	newSummary, found, err := store.ProductionBudgetSummary(ctx, "owner-a", newPlan.SessionID)
	if err != nil || !found {
		t.Fatalf("new plan found=%v err=%v", found, err)
	}
	if newSummary.Plan.Steps[0].OfferingID != multiview || !newSummary.Plan.Allows("image-to-3d", multiview) ||
		!newSummary.Plan.Covers("image-to-3d", multiview, 1) {
		t.Fatalf("a plan storing Offering IDs changed: %+v", newSummary.Plan.Steps)
	}

	// Two Offerings carrying the same model name: the name is not guessed, so
	// the legacy step stays off-plan.
	ambiguous := func(capability generation.Capability) []generation.Offering {
		offerings := product.List(capability)
		if capability != generation.ImageTo3D {
			return offerings
		}
		return append(offerings, generation.Offering{
			ID: "vendor/twin/image-to-3d", ProviderID: "vendor", ModelID: "meshy-7.1-multiview",
			Capability: generation.ImageTo3D, Enabled: true,
		})
	}
	ambiguousPlan := activeLegacyPlanInSession(t, repository, "session-c", now.Add(2*time.Minute))
	ambiguousSummary, found, err := budgetPlanStore(repository, ambiguous).
		ProductionBudgetSummary(ctx, "owner-a", ambiguousPlan.SessionID)
	if err != nil || !found {
		t.Fatalf("ambiguous plan found=%v err=%v", found, err)
	}
	if ambiguousSummary.Plan.Steps[0].OfferingID != "meshy-7.1-multiview" ||
		ambiguousSummary.Plan.Allows("image-to-3d", multiview) {
		t.Fatalf("an ambiguous name was guessed: %+v", ambiguousSummary.Plan.Steps)
	}
}

// legacyPlan is a v1.8.0-shaped plan: the Agent's model name is the step pin.
func legacyPlan() budget.Plan {
	return budget.Plan{
		Title: "三视图主体",
		Steps: []budget.PlanStep{
			{Capability: "image-to-3d", OfferingID: "meshy-7.1-multiview", Purpose: "三视图主体",
				Estimate: budget.Legacy(40)},
			{Capability: "text-to-image", OfferingID: "never-declared", Purpose: "参考图",
				Estimate: budget.Legacy(5)},
		},
		CorrectionCapabilities: []string{"retexture"},
		Contingencies: []budget.PlanContingency{{
			ID: "repair", Trigger: []string{"self_check"}, Step: budget.PlanStep{
				Capability: "text-to-3d", OfferingID: "meshy-7.1", Purpose: "修补", Estimate: budget.Legacy(10)},
		}},
	}
}

func activeLegacyPlan(t *testing.T, repository *sqlite.Repository, now time.Time) budget.ProductionBudget {
	t.Helper()
	return activeLegacyPlanInSession(t, repository, "session-a", now)
}

func activeLegacyPlanInSession(t *testing.T, repository *sqlite.Repository, sessionID string,
	now time.Time,
) budget.ProductionBudget {
	t.Helper()
	return activePlan(t, repository, sessionID, legacyPlan(), now)
}

// activePlan stores and approves one plan for the session.
func activePlan(t *testing.T, repository *sqlite.Repository, sessionID string, plan budget.Plan,
	now time.Time,
) budget.ProductionBudget {
	t.Helper()
	ctx := context.Background()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: sessionID, OwnerID: "owner-a", OwnerName: "A", Title: sessionID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create session %q: %v", sessionID, err)
	}
	id := "pb_" + sessionID
	if _, err := repository.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: sessionID, Plan: plan,
		Limits: []budget.Amount{budget.Legacy(100)}, State: budget.StatePending, CreatedAt: now,
	}); err != nil {
		t.Fatalf("store plan: %v", err)
	}
	active, err := repository.DecideProductionBudget(ctx, "owner-a", id, "human-a", true, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	if active.State != budget.StateActive {
		t.Fatalf("plan state = %q", active.State)
	}
	return active
}
