package processing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// budgetMemoryGrants adds the atomic budget authorization to the in-memory
// grant store used by the plan policy tests.
type budgetMemoryGrants struct {
	*memoryGrants
	summary  budget.Summary
	reserved int
	approve  int
}

func (m *budgetMemoryGrants) GetExecutionPreparedSnapshot(ctx context.Context, owner, id string) (executiongrant.PreparedSnapshot, error) {
	snapshot, err := m.memoryGrants.GetExecutionPreparedSnapshot(ctx, owner, id)
	if err == nil {
		snapshot.EstimatedCredits = 5
	}
	return snapshot, err
}

func (m *budgetMemoryGrants) PreparePendingWithBudget(ctx context.Context, identity executiongrant.Identity,
	source, turn string, budgetEligible bool) (executiongrant.Grant, error) {
	if !budgetEligible {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	grant, err := m.memoryGrants.PreparePending(ctx, identity, source, turn)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	grant.BudgetEligible = true
	m.grants[grant.RequestID] = grant
	return grant, nil
}

func (m *budgetMemoryGrants) ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string, credits int) (executiongrant.Grant, error) {
	if budgetID != m.summary.ID || m.summary.State != budget.StateActive {
		return executiongrant.Grant{}, budget.ErrInactive
	}
	if credits > m.summary.RemainingCredits {
		return executiongrant.Grant{}, budget.ErrOverLimit
	}
	grant, err := m.memoryGrants.ApproveExecutionGrantByPolicy(ctx, owner, requestID, actor)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	m.approve++
	m.reserved += credits
	m.summary.ReservedCredits += credits
	m.summary.RemainingCredits -= credits
	return grant, nil
}

func (m *budgetMemoryGrants) CreateProductionBudget(context.Context, budget.ProductionBudget) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, executiongrant.ErrInvalid
}

func (m *budgetMemoryGrants) DecideProductionBudget(context.Context, string, string, string, bool, time.Time) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, executiongrant.ErrInvalid
}

func (m *budgetMemoryGrants) GetProductionBudget(context.Context, string, string) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, budget.ErrNotFound
}

func (m *budgetMemoryGrants) PendingProductionBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (m *budgetMemoryGrants) CurrentProductionBudget(context.Context, string, string) (budget.ProductionBudget, bool, error) {
	return budget.ProductionBudget{}, false, nil
}

func (m *budgetMemoryGrants) ProductionBudgetSummary(context.Context, string, string) (budget.Summary, bool, error) {
	return m.summary, true, nil
}

func (m *budgetMemoryGrants) BudgetSummaryForGrant(context.Context, string, string) (budget.Summary, bool, error) {
	return budget.Summary{}, false, nil
}

func (m *budgetMemoryGrants) ExpireProductionBudgets(context.Context, time.Time) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (m *budgetMemoryGrants) SettleBudgetReservations(context.Context, time.Time) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func budgetProcessingFixture(t *testing.T, origin string) (*Service, *gatewayFake, *budgetMemoryGrants, Plan) {
	t.Helper()
	product := memoryCatalog{"step": {ID: "step", Revision: "v1", ProviderID: "meshy",
		Capability: "retexture", Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 5,
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d",
		State: generation.TaskSucceeded, Offering: generation.Offering{ProviderID: "meshy"},
		Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, product, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	grants := &budgetMemoryGrants{
		memoryGrants: &memoryGrants{grants: map[string]executiongrant.Grant{}, gateway: gateway},
		summary: budget.Summary{ID: "pb-processing", State: budget.StateActive,
			LimitCredits: 20, RemainingCredits: 20,
			Plan: budget.Plan{Title: "加工计划", Steps: []budget.PlanStep{
				{Capability: "retexture", Purpose: "换贴图", EstimatedCredits: 5},
			}}},
	}
	policy := approvalpolicy.New("budget", []string{"auto", "budget", "manual"},
		[]string{"retexture"}, []string{"retexture"})
	service.WithGrants(&executiongrant.Service{Grants: grants, Prepared: grants, Budgets: grants,
		Policy: policy, Modes: fixedMode("budget")})
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session",
		SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "budget-processing-plan-0001",
		Origin: origin, Steps: []StepRequest{{OfferingID: "step", ExpectedOfferingRevision: "v1",
			Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, gateway, grants, plan
}

// An Agent-created processing plan's steps are approved by the session budget:
// the plan runs without a per-step card and reserves the server's price.
func TestProcessingPlanFromTheAgentRunsUnderTheBudget(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, plan := budgetProcessingFixture(t, OriginAgent)
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State == Paused || got.Steps[0].AuthorizationRef == "" || len(gateway.commands) != 1 {
		t.Fatalf("agent processing plan = %+v commands=%d err=%v", got, len(gateway.commands), err)
	}
	grant, err := grants.GetExecutionGrant(ctx, "owner", got.Steps[0].AuthorizationRef)
	if err != nil || !grant.ApprovedByBudget() || grant.DecidedBy != executiongrant.BudgetActorPrefix+"pb-processing" {
		t.Fatalf("step grant = %+v err=%v", grant, err)
	}
	if grants.approve != 1 || grants.reserved != 5 || grants.summary.RemainingCredits != 15 {
		t.Fatalf("budget reservations = approvals=%d reserved=%d summary=%+v", grants.approve, grants.reserved, grants.summary)
	}
}

// A budget-approved step the kernel rejects permanently (for instance because
// the Offering vanished) keeps no authorization reference: the saved plan then
// releases the reservation instead of holding credits for a step that can
// never spend them (#103 review B2).
func TestRejectedBudgetStepDropsItsAuthorizationReference(t *testing.T) {
	ctx := context.Background()
	service, gateway, _, plan := budgetProcessingFixture(t, OriginAgent)
	gateway.submitError = &generation.Error{Code: generation.ErrUnknownOffering, Operation: "submit_prepared"}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != Failed || got.Steps[0].Failure != ReasonSubmitRejected || got.Steps[0].AuthorizationRef != "" {
		t.Fatalf("rejected budget step = %+v", got)
	}
}

// A browser-created processing plan keeps the per-step human approval: the
// same capability pauses and spends no budget.
func TestProcessingPlanFromTheBrowserKeepsPerStepApproval(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, plan := budgetProcessingFixture(t, OriginBrowser)
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State != Paused || got.Failure != ReasonConsentRequired ||
		got.Steps[0].AuthorizationRef != "" || len(gateway.commands) != 0 {
		t.Fatalf("browser processing plan = %+v commands=%d err=%v", got, len(gateway.commands), err)
	}
	if grants.approve != 0 || grants.reserved != 0 || grants.summary.RemainingCredits != 20 {
		t.Fatalf("browser plan spent the budget: approvals=%d reserved=%d summary=%+v",
			grants.approve, grants.reserved, grants.summary)
	}
}
