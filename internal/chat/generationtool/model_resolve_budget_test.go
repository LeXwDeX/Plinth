package generationtool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #380 step 1 / Issue acceptance 2: the production plan and the generation
// tool resolve a step's Offering with one rule (chat.ResolveModel). The plan
// stores the Offering it quoted, the continuation resolves the same Offering,
// and the plan's reservation approves the step without a person.
//
// The unplanned call is checked too, so the test proves the plan's pin changed
// the resolution rather than that both paths happen to agree.
func TestPlanAndExecutionResolveTheSameOfferingWithoutACard(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "model-resolve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "go", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "create_model_3d" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("create_model_3d missing")
	}
	tool.IdempotencyKeyTemplate = "agent:{session_id}:{call_id}"
	const (
		defaultOffering = "vendor/default/text-to-3d"
		plannedOffering = "vendor/planned/text-to-3d"
	)
	offerings := []generation.Offering{
		{ID: defaultOffering, Revision: "rev-a", ModelID: "default-model", ProviderID: "vendor",
			Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true, Default: true,
			Timeout: time.Second, EstimatedCredits: 7,
			Parameters: []generation.Parameter{
				{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
			}},
		{ID: plannedOffering, Revision: "rev-a", ModelID: "planned-model", ProviderID: "vendor",
			Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true,
			Timeout: time.Second, EstimatedCredits: 7,
			Parameters: []generation.Parameter{
				{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
			}},
	}
	gateway := &grantGateway{fakeGateway: &fakeGateway{offerings: offerings}, repo: repo}
	var manual, known []string
	for _, capability := range application.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	grants := &executiongrant.Service{
		Grants: repo, Prepared: repo, Budgets: repo, Modes: sessionMode{mode: "budget"},
		Policy: approvalpolicy.New(application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes, manual, known),
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Operations: repo, Grants: grants,
		Chat: application.Chat, TaskStates: application.TaskStates,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Agent's plan quotes the non-default Offering the step names.
	planner := &chat.Service{}
	planner.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: application.Chat.ApprovalPolicy.Budget,
		Offerings: func() []generation.Offering {
			return offerings
		},
		OfferingAvailable: func(generation.OfferingID) bool { return true },
		Store:             grants,
		Usage:             application.Chat.Model3DUsage,
		Production:        application.Chat.ModelReview.Inspect.Production,
	})
	proposal := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		FrozenIdempotencyKey: "idem-a",
		Call: agents.ToolCall{ID: "plan-call", Name: "propose_production_plan", Arguments: []byte(`{
			"title":"一张图做模型",
			"steps":[{"capability":"text-to-3d","model":"` + plannedOffering + `","purpose":"主体"}],
			"budgets":[{"unit":"legacy.credits","amount":7}]
		}`)},
	}
	proposed, err := planner.ProposeProductionPlan(ctx, proposal)
	if err != nil || proposed.Error != "" || proposed.NoSubmission != true {
		t.Fatalf("proposal = %+v err=%v", proposed, err)
	}
	plan := storedPlan(t, repo, proposed, "owner-a")
	if len(plan.Plan.Steps) != 1 {
		t.Fatalf("stored plan = %+v", plan)
	}
	if plan.Plan.Steps[0].OfferingID != plannedOffering {
		t.Fatalf("planned offering = %q, want the model the step names", plan.Plan.Steps[0].OfferingID)
	}
	if _, err := repo.DecideProductionBudget(ctx, "owner-a", plan.ID, "human-a", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	// The plan's continuation runs the same step without naming a model: the
	// plan's pin resolves the Offering, and the reservation approves it.
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", Tool: tool,
		Options: chat.TurnOptions{PlanContinuation: plan.ID},
		Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"go"}`)},
	}
	planned := executeBudgetCall(t, executor, command, "call-a")
	if planned.Suspend || planned.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("the planned step waited: %+v submits=%d", planned, gateway.submitCount)
	}
	if executed := executedOffering(t, repo, "call-a"); executed != plannedOffering {
		t.Fatalf("executed offering = %q, want the plan's %q", executed, plannedOffering)
	}
	content := budgetContent(t, planned)
	if content[tool.ResultFields.Approval] != "policy" {
		t.Fatalf("approval = %v, want policy", content[tool.ResultFields.Approval])
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if grant.DecidedBy != executiongrant.BudgetActorPrefix+plan.ID || !grant.BudgetEligible {
		t.Fatalf("grant = %+v, want the plan's reservation", grant)
	}

	// Outside a plan continuation the same call resolves the default Offering,
	// which the approved plan does not cover: the plan's pin, not an accident,
	// made the planned call run without a card.
	unplanned := command
	unplanned.Options.PlanContinuation = ""
	unplannedResult := executeBudgetCall(t, executor, unplanned, "call-b")
	unplannedContent := budgetContent(t, unplannedResult)
	if !unplannedResult.NoSubmission || unplannedContent[tool.ResultFields.Reason] != budget.ReasonOffPlan ||
		gateway.submitCount != 1 {
		t.Fatalf("unplanned step = %+v reasons=%v submits=%d", unplannedResult, unplannedContent, gateway.submitCount)
	}
}

// The plan's step for a policy capability still pins the Offering it quoted, so
// budget.Plan.Allows compares like with like: the plan stores an Offering ID,
// never the model name the Agent typed.
func TestThePlanStoresTheOfferingIDItQuoted(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "model-resolve-pin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	offerings := []generation.Offering{
		{ID: "vendor/default/text-to-3d", Revision: "rev-a", ModelID: "default-model", ProviderID: "vendor",
			Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true, Default: true,
			Timeout: time.Second, EstimatedCredits: 7},
		{ID: "vendor/named/text-to-3d", Revision: "rev-a", ModelID: "named-model", ProviderID: "vendor",
			Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true,
			Timeout: time.Second, EstimatedCredits: 7},
	}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Budgets: repo}
	planner := &chat.Service{}
	planner.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: application.Chat.ApprovalPolicy.Budget,
		Offerings: func() []generation.Offering {
			return offerings
		},
		Store: grants, Usage: application.Chat.Model3DUsage,
	})
	proposal := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		FrozenIdempotencyKey: "idem-a",
		Call: agents.ToolCall{ID: "plan-call", Name: "propose_production_plan", Arguments: []byte(`{
			"title":"按模型名","steps":[{"capability":"text-to-3d","model":"named-model","purpose":"主体"}],
			"budgets":[{"unit":"legacy.credits","amount":7}]
		}`)},
	}
	proposed, err := planner.ProposeProductionPlan(ctx, proposal)
	if err != nil || proposed.Error != "" {
		t.Fatalf("proposal = %+v err=%v", proposed, err)
	}
	plan := storedPlan(t, repo, proposed, "owner-a")
	if plan.Plan.Steps[0].OfferingID != "vendor/named/text-to-3d" {
		t.Fatalf("step = %+v, want the Offering ID the model name selects", plan.Plan.Steps[0])
	}
	if !plan.Plan.Allows("text-to-3d", "vendor/named/text-to-3d") {
		t.Fatal("the plan must allow the Offering it quoted")
	}
	if plan.Plan.Allows("text-to-3d", "vendor/default/text-to-3d") {
		t.Fatal("the plan must not allow another Offering of the pinned capability")
	}
	// The card lists the Offering the plan quoted.
	var content map[string]any
	if err := json.Unmarshal([]byte(proposed.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	steps, _ := content["steps"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["offering_id"] != "vendor/named/text-to-3d" {
		t.Fatalf("card steps = %v", content["steps"])
	}
}

// storedPlan reads the plan a proposal stored, from the budget_id the tool
// result carries.
func storedPlan(t *testing.T, repo *sqlite.Repository, proposed chat.ToolResult, ownerID string) budget.ProductionBudget {
	t.Helper()
	var content map[string]any
	if err := json.Unmarshal([]byte(proposed.AgentContent), &content); err != nil {
		t.Fatalf("proposal content %q: %v", proposed.AgentContent, err)
	}
	budgetID, _ := content["budget_id"].(string)
	if budgetID == "" {
		t.Fatalf("proposal carried no budget id: %v", content)
	}
	plan, err := repo.GetProductionBudget(context.Background(), ownerID, budgetID)
	if err != nil {
		t.Fatalf("stored plan %q: %v", budgetID, err)
	}
	return plan
}

// executedOffering is the Offering the tool recorded for a call: the frozen
// step's own Offering, whatever path approved it.
func executedOffering(t *testing.T, repo *sqlite.Repository, callID string) generation.OfferingID {
	t.Helper()
	op, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", callID)
	if err != nil {
		t.Fatalf("operation %s: %v", callID, err)
	}
	return op.OfferingID
}
