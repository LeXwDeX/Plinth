package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// sessionLookupRepository fails the session read of the approval mode on
// demand; everything else is the in-memory repository.
type sessionLookupRepository struct {
	*memoryChatRepository
	fail bool
}

func (repository *sessionLookupRepository) Session(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (chat.Session, bool, error) {
	if repository.fail {
		return chat.Session{}, false, errors.New("session store unavailable")
	}
	return repository.memoryChatRepository.Session(ctx, ownerID, sessionID)
}

// Texts of the shipped Schema (application.chat.approval_policy.prompt) that
// tell the three modes apart.
const (
	manualModeLabel   = "批准方式是「严格（逐步批准）」"
	manualModeText    = "严格（逐步批准）方式下，文生 3D、图生 3D、Gaussian Splat 生成、换贴图这几类步骤会先出批准卡"
	autoModeLabel     = "批准方式是「自动」"
	autoModeText      = "自动方式下，所有付费步骤都由系统按会话批准方式自动批准，不出批准卡"
	budgetModeLabel   = "批准方式是「预算（制作计划）」"
	budgetModeText    = "预算方式下，Agent 先用 propose_production_plan 提交一份制作计划和按积分单位的预算"
	budgetBlockHead   = "【制作预算】"
	noPlanStateText   = "尚未提交计划"
	approvalBlockHead = "【批准方式】"
)

// promptBudgetStore gives the Agent's budget context one frozen plan summary.
type promptBudgetStore struct {
	summary budget.Summary
	found   bool
}

func (store promptBudgetStore) CreateProductionPlan(context.Context, string, string, string, string, string,
	budget.Plan, []budget.Amount) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, errors.New("unused")
}

func (store promptBudgetStore) BudgetSummary(context.Context, string, string) (budget.Summary, bool, error) {
	return store.summary, store.found, nil
}

func (store promptBudgetStore) PendingBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (store promptBudgetStore) GetBudget(context.Context, string, string) (budget.ProductionBudget, error) {
	return budget.ProductionBudget{}, budget.ErrNotFound
}

type approvalPromptCase struct {
	// mode is written to the session as stored ("" keeps the new-session
	// default); it may be a value the Schema does not offer.
	mode  string
	scope string
	// budget is the plan summary the Agent's context reads; nil wires no plan
	// store, so a budget session reads the Schema's no-plan state.
	budget *budget.Summary
	// failLookup makes the session read fail from the turn on.
	failLookup bool
}

// approvalPromptSystem runs one turn of a session set up by c, with the
// shipped Schema texts, and returns the system context the model read on its
// first request.
func approvalPromptSystem(t *testing.T, c approvalPromptCase) string {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shipped := taskCatalog.Application().Chat
	schema := configuredChatSchema(t)
	schema.Agent.SystemPrompt = shipped.Agent.SystemPrompt
	schema.AgentScope = shipped.AgentScope
	schema.ApprovalPolicy = shipped.ApprovalPolicy
	schema.Tools = shipped.Tools
	repository := &sessionLookupRepository{memoryChatRepository: newMemoryChatRepository()}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(schema.AgentScope)
		// A new session takes its default from the same shipped policy the
		// Orchestrator reads its texts from.
		config.ApprovalPolicy = approvalpolicy.New(shipped.ApprovalPolicy.DefaultMode,
			shipped.ApprovalPolicy.Modes, []string{"text-to-3d"},
			[]string{"text-to-3d", "text-to-image"})
	})
	if c.budget != nil {
		service.SetProductionPlans(chat.ProductionPlanPolicy{
			Budget:    schema.ApprovalPolicy.Budget,
			Offerings: func() []generation.Offering { return nil },
			Store:     promptBudgetStore{summary: *c.budget, found: true},
		})
	}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if c.mode != "" {
		if _, _, err := repository.SetSessionApprovalMode(ctx, "owner-a", session.ID, c.mode); err != nil {
			t.Fatal(err)
		}
	}
	if c.scope != "" {
		if _, found, err := repository.SetSessionAgentScope(ctx, "owner-a", session.ID, c.scope); err != nil || !found {
			t.Fatalf("seed scope: found=%v err=%v", found, err)
		}
	}
	options := chat.TurnOptions{CreativeDomain: "image"}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "hello", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	repository.fail = c.failLookup
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: assistant.ID, Options: options}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("the Agent was never called")
	}
	return agent.requests[0].Messages[0].Content
}

// #7: the Agent is told the session's real approval mode, whatever the
// conversation says, and the two modes never leak into each other's text.
func TestAgentSystemContextCarriesTheSessionApprovalMode(t *testing.T) {
	for name, testCase := range map[string]struct {
		session          approvalPromptCase
		want, wantAbsent []string
	}{
		"manual session": {approvalPromptCase{mode: "manual"}, []string{manualModeLabel, manualModeText},
			[]string{autoModeLabel, autoModeText, budgetModeLabel, budgetModeText}},
		"auto session": {approvalPromptCase{mode: "auto"}, []string{autoModeLabel, autoModeText},
			[]string{manualModeLabel, manualModeText, budgetModeLabel, budgetModeText}},
		"new session defaults to budget": {approvalPromptCase{}, []string{budgetModeLabel, budgetModeText, budgetBlockHead, noPlanStateText},
			[]string{autoModeLabel, autoModeText, manualModeLabel, manualModeText}},
		"a stored unknown mode is manual": {approvalPromptCase{mode: "yolo"}, []string{manualModeLabel, manualModeText},
			[]string{autoModeLabel, autoModeText, budgetModeLabel, budgetModeText}},
		"the Agent scope does not change it": {approvalPromptCase{mode: "auto", scope: "2d"},
			[]string{autoModeLabel, autoModeText}, []string{manualModeLabel, manualModeText, budgetModeLabel, budgetModeText}},
	} {
		system := approvalPromptSystem(t, testCase.session)
		for _, want := range testCase.want {
			if !strings.Contains(system, want) {
				t.Errorf("%s: the system context omits %q", name, want)
			}
		}
		for _, absent := range testCase.wantAbsent {
			if strings.Contains(system, absent) {
				t.Errorf("%s: the system context carries %q", name, absent)
			}
		}
	}
}

// The block states that the person alone sets the mode, so the Agent neither
// switches it nor sells a switch as the way to get approval cards (#150, #7).
// The prompt no longer explains the Agent scope, and the approval block still
// follows the scope block.
func TestApprovalModePromptKeepsTheAgentOutOfTheModeAndFollowsTheScope(t *testing.T) {
	system := approvalPromptSystem(t, approvalPromptCase{mode: "manual"})
	for _, want := range []string{"只由用户在顶栏切换", "你不能切换它", "不要主动建议用户切换它", "不要猜测"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	if strings.Contains(system, "它与 Agent 范围") {
		t.Error("the approval block still explains the Agent scope")
	}
	scope, approval := strings.Index(system, "【Agent 范围】"), strings.Index(system, approvalBlockHead)
	if scope < 0 || approval < scope {
		t.Errorf("the approval block (%d) does not follow the scope block (%d)", approval, scope)
	}
	if strings.Count(system, approvalBlockHead) != 1 {
		t.Errorf("the approval block appears %d times", strings.Count(system, approvalBlockHead))
	}
}

// A person who approved a plan is told its limits and what is left, so the
// Agent runs inside the approved budget instead of opening another card or
// proposing the same plan again (#103). Every amount is given per credit unit
// with its label, and different units are never added (#120).
func TestBudgetModePromptCarriesTheActivePlanAndWhatIsLeft(t *testing.T) {
	tripo := budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 200000000}
	hyper3D := budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 3000000}
	summary := budget.Summary{
		ID: "pb_1", State: budget.StateActive,
		Units: []budget.UnitBudget{
			budget.NewUnitBudget(hyper3D, 500000, 0),
			budget.NewUnitBudget(budget.Legacy(40), 0, 0),
			budget.NewUnitBudget(tripo, 120000000, 30000000),
		},
		Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
			{Capability: "text-to-3d", Purpose: "主体模型", Estimate: budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 80000000}},
			{Capability: "mesh-segmentation", Purpose: "拆件", Estimate: budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 500000}},
		}},
	}
	system := approvalPromptSystem(t, approvalPromptCase{mode: "budget", budget: &summary})
	for _, want := range []string{
		budgetBlockHead, budgetModeLabel,
		"当前制作计划：执行中",
		"不同供应商的单位不相加",
		"总预算 3 Hyper3D credits · 40 历史积分估算 · 200 Tripo credits",
		"已扣费 0.5 Hyper3D credits · 0 历史积分估算 · 120 Tripo credits",
		"执行中预留 0 Hyper3D credits · 0 历史积分估算 · 30 Tripo credits",
		"剩余 2.5 Hyper3D credits · 40 历史积分估算 · 50 Tripo credits",
		"计划：骑士；text-to-3d：主体模型，约 80 Tripo credits；mesh-segmentation：拆件，约 0.5 Hyper3D credits",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the budget context omits %q", want)
		}
	}
	if strings.Contains(system, noPlanStateText) || strings.Contains(system, "{limit}") {
		t.Errorf("an approved plan still reads as unplanned: %q", system)
	}
}

// A mode that cannot be read is left out: the Agent is never given a guess
// that could contradict the session.
func TestApprovalModePromptIsOmittedWhenTheSessionCannotBeRead(t *testing.T) {
	system := approvalPromptSystem(t, approvalPromptCase{mode: "manual", failLookup: true})
	if strings.Contains(system, approvalBlockHead) || strings.Contains(system, manualModeLabel) ||
		strings.Contains(system, autoModeLabel) {
		t.Errorf("a failed lookup still states a mode: %q", system)
	}
}
