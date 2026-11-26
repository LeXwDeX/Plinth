package chat_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #549: one option of a direction card does one thing. The real case: the
// person picked "keep the silhouette" (remesh) and the Agent went on to
// retexture and resize in the same turn under auto approval, 21 credits the
// person never chose.

func oneStepNextStepKind() catalog.ChatChoiceKindSchema {
	kind := nextStepChoiceKind()
	kind.Step = &catalog.ChatChoiceStepSchema{
		OneStepRefusal:  "选项 {option} 声明了不止一个步骤：一个选项只能做一件事，请拆开。",
		UnchosenRefusal: "没有提交 {tool}：用户只选了「{option}」。请先汇报当前结果，再用新的 ask_choice 卡问下一步。",
	}
	return kind
}

// oneStepTools are the paid tools an option may name and their capabilities.
func oneStepTools() map[string][]string {
	return map[string][]string{
		"process_model":     {"remesh", "resize"},
		"retexture_model":   {"retexture"},
		"create_three_view": {},
	}
}

func oneStepOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "tripo/remesh", Revision: "v1", ProviderID: "tripo", ModelID: "tripo-remesh", Capability: "remesh",
			Domain: generation.DomainModel3D, Enabled: true, Default: true, CreditsMetered: true, EstimatedCredits: 5},
		{ID: "tripo/retexture", Revision: "v1", ProviderID: "tripo", ModelID: "tripo-retexture", Capability: "retexture",
			Domain: generation.DomainModel3D, Enabled: true, Default: true, CreditsMetered: true, EstimatedCredits: 15},
		{ID: "tripo/resize", Revision: "v1", ProviderID: "tripo", ModelID: "tripo-resize", Capability: "resize",
			Domain: generation.DomainModel3D, Enabled: true, Default: true, CreditsMetered: true, EstimatedCredits: 1},
	}
}

func openOneStepService(t *testing.T) (*chat.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	seedReplies(t, repository, "owner-a", "session-a", "turn-1", "turn-2")
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{directionChoiceKind(), oneStepNextStepKind()}
		config.ChoiceMaxOptions = 8
		config.HarnessOperations = nextStepOperations()
		config.ChoiceStepTools = oneStepTools()
	})
	service.SetChoices(repository, 20)
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:    shippedBudgetSchema(t),
		Offerings: func() []generation.Offering { return oneStepOfferings() },
		Store:     promptBudgetStore{},
	})
	return service, ctx
}

func oneStepCommand(turn string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: turn,
		Call: agents.ToolCall{ID: "call-" + turn, Name: "ask_choice"},
	}
}

// An option that names two steps is refused with the Schema's words, and no
// card is stored: the Agent must split it.
func TestDirectionOptionNamingTwoStepsIsRefused_549(t *testing.T) {
	service, ctx := openOneStepService(t)
	refused, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), chat.AskChoiceArguments{
		Kind: "next_step", Options: []chat.ChoiceOptionInput{
			{ID: "balanced", Label: "保轮廓：约 5,000 三角面", Steps: []chat.ChoiceStep{
				{Tool: "process_model", Capability: "remesh"}, {Tool: "retexture_model", Capability: "retexture"}}},
			{ID: "bake", Label: "保留现在这套木纹"},
		}})
	if err != nil || !refused.NoSubmission || !strings.Contains(refused.Error, "一个选项只能做一件事") ||
		!strings.Contains(refused.Error, "balanced") {
		t.Fatalf("two-step option = %+v %v", refused, err)
	}
	if choices, err := service.ListChoices(ctx, "owner-a", "session-a"); err != nil || len(choices) != 0 {
		t.Fatalf("a refused card was stored: %+v %v", choices, err)
	}
}

// One step per option passes; the server quotes it from the Offering it would
// run, never from the Agent, and stores the step with the option.
func TestDirectionOptionWithOneStepCarriesTheServerQuote_549(t *testing.T) {
	service, ctx := openOneStepService(t)
	result, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), chat.AskChoiceArguments{
		Kind: "next_step", Recommended: "balanced", Options: []chat.ChoiceOptionInput{
			{ID: "balanced", Label: "保轮廓：约 5,000 三角面", Description: "用现有模型重拓扑到约 5,000 三角面",
				Steps: []chat.ChoiceStep{{Tool: "process_model", Capability: "remesh"}}},
			// A single-capability tool fills its capability itself.
			{ID: "texture", Label: "换一套木纹", Steps: []chat.ChoiceStep{{Tool: "retexture_model"}}},
			{ID: "bake", Label: "保留现在这套木纹", Description: "我给你 Blender 的简化与烘焙步骤"},
		}})
	if err != nil || result.Error != "" {
		t.Fatalf("one-step card = %+v %v", result, err)
	}
	var opened struct {
		Options []struct {
			ID               string           `json:"id"`
			Step             *chat.ChoiceStep `json:"step"`
			EstimatedCredits int              `json:"estimated_credits"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil || len(opened.Options) != 3 ||
		opened.Options[0].Step == nil || opened.Options[0].EstimatedCredits != 5 || opened.Options[2].Step != nil {
		t.Fatalf("agent content = %s %v", result.AgentContent, err)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 {
		t.Fatalf("stored = %+v %v", choices, err)
	}
	options := choices[0].Options
	if step := options[0].Step; step == nil || step.Tool != "process_model" || step.Capability != "remesh" ||
		!options[0].CreditsMetered || options[0].EstimatedCredits != 5 {
		t.Fatalf("stored remesh option = %+v step=%+v", options[0], options[0].Step)
	}
	if step := options[1].Step; step == nil || step.Tool != "retexture_model" || step.Capability != "retexture" ||
		options[1].EstimatedCredits != 15 {
		t.Fatalf("stored retexture option = %+v step=%+v", options[1], options[1].Step)
	}
	if options[2].Step != nil || options[2].CreditsMetered || options[2].EstimatedCredits != 0 {
		t.Fatalf("an explanation option carries a step or a quote: %+v", options[2])
	}
}

// A step the server cannot run as one paid step is refused before any card.
func TestDirectionOptionStepMustBeOneRunnablePaidStep_549(t *testing.T) {
	cases := []struct {
		name string
		step chat.ChoiceStep
	}{
		{"unknown tool", chat.ChoiceStep{Tool: "delete_session"}},
		{"free tool", chat.ChoiceStep{Tool: "inspect_model"}},
		{"multi-capability tool without capability", chat.ChoiceStep{Tool: "process_model"}},
		{"capability the tool does not declare", chat.ChoiceStep{Tool: "process_model", Capability: "retexture"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, ctx := openOneStepService(t)
			refused, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), chat.AskChoiceArguments{
				Kind: "next_step", Options: []chat.ChoiceOptionInput{
					{ID: "a", Label: "A", Steps: []chat.ChoiceStep{tc.step}}, {ID: "b", Label: "B"}}})
			if err != nil || !refused.NoSubmission || !strings.HasPrefix(refused.Error, "choice options invalid") {
				t.Fatalf("step %+v = %+v %v", tc.step, refused, err)
			}
		})
	}
	// A tool this turn cannot run is refused too.
	service, ctx := openOneStepService(t)
	command := oneStepCommand("turn-2")
	command.ToolAvailable = func(name string) bool { return name != "retexture_model" }
	refused, err := service.AskChoice(ctx, command, oneStepNextStepKind(), chat.AskChoiceArguments{
		Kind: "next_step", Options: []chat.ChoiceOptionInput{
			{ID: "a", Label: "A", Steps: []chat.ChoiceStep{{Tool: "retexture_model"}}}, {ID: "b", Label: "B"}}})
	if err != nil || !strings.HasPrefix(refused.Error, "choice options invalid") {
		t.Fatalf("unavailable step tool = %+v %v", refused, err)
	}
}

// Switching to a function is that function's one step: the server derives and
// quotes it, and a different step on the same option is a second thing.
func TestSwitchOptionRunsTheSwitchedFunctionAsItsOneStep_549(t *testing.T) {
	service, ctx := openOneStepService(t)
	result, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), chat.AskChoiceArguments{
		Kind: "next_step", Options: []chat.ChoiceOptionInput{
			{ID: "switch", Label: "改为换贴图", Action: &chat.ChoiceAction{Type: "switch_operation", OperationID: "retexture"}},
			{ID: "clear", Label: "回到自由模式", Action: &chat.ChoiceAction{Type: "clear_operation"}},
		}})
	if err != nil || result.Error != "" {
		t.Fatalf("switch card = %+v %v", result, err)
	}
	choices, _ := service.ListChoices(ctx, "owner-a", "session-a")
	if step := choices[0].Options[0].Step; step == nil || step.Tool != "retexture_model" || step.Capability != "retexture" ||
		choices[0].Options[0].EstimatedCredits != 15 {
		t.Fatalf("switch option step = %+v", choices[0].Options[0])
	}
	if choices[0].Options[1].Step != nil {
		t.Fatalf("clear option got a step: %+v", choices[0].Options[1].Step)
	}
	refused, err := service.AskChoice(ctx, oneStepCommand("turn-2"), oneStepNextStepKind(), chat.AskChoiceArguments{
		Kind: "next_step", Options: []chat.ChoiceOptionInput{
			{ID: "switch", Label: "改为换贴图再缩放", Action: &chat.ChoiceAction{Type: "switch_operation", OperationID: "retexture"},
				Steps: []chat.ChoiceStep{{Tool: "process_model", Capability: "resize"}}},
			{ID: "b", Label: "B"},
		}})
	if err != nil || !strings.Contains(refused.Error, "一个选项只能做一件事") {
		t.Fatalf("switch plus another step = %+v %v", refused, err)
	}
}

// answeredChoices is the session's stored cards for the turn guard: the
// orchestrator reads them, nothing else.
type answeredChoices struct {
	chat.ChoiceStore
	mu      sync.Mutex
	choices []chat.Choice
}

func (store *answeredChoices) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]chat.Choice(nil), store.choices...), nil
}

// SupersedeChoicesBefore is the end of every person's turn (#645); these
// cards are answered on the card, so it changes nothing.
func (store *answeredChoices) SupersedeChoicesBefore(context.Context, string, string, string, time.Time) error {
	return nil
}

// stepToolExecutor records every tool really run; paid calls get a Task.
type stepToolExecutor struct {
	mu    sync.Mutex
	calls []string
}

func (executor *stepToolExecutor) Execute(_ context.Context, command chat.ToolCommand,
	_ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls = append(executor.calls, command.Call.Name)
	return chat.ToolResult{AgentContent: "ran " + command.Call.Name, TaskID: "task-" + command.Call.ID}, nil
}

type oneStepRig struct {
	repository *sessionLookupRepository
	service    *chat.Service
	store      *answeredChoices
	schema     catalog.ChatSchema
	tools      *stepToolExecutor
	agent      *scriptedAgent
}

func newOneStepRig(t *testing.T, plan *budget.Summary, responses ...agents.Completion) *oneStepRig {
	t.Helper()
	rig := &oneStepRig{repository: &sessionLookupRepository{memoryChatRepository: newMemoryChatRepository()},
		schema: configuredChatSchema(t), store: &answeredChoices{}, tools: &stepToolExecutor{}}
	rig.service = newChatServiceWith(t, rig.repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(rig.schema.AgentScope)
		config.ApprovalPolicy = approvalpolicy.New(rig.schema.ApprovalPolicy.DefaultMode,
			rig.schema.ApprovalPolicy.Modes, nil, nil)
	})
	rig.service.SetChoices(rig.store, 20)
	if plan != nil {
		rig.service.SetProductionPlans(chat.ProductionPlanPolicy{
			Budget:    rig.schema.ApprovalPolicy.Budget,
			Offerings: func() []generation.Offering { return nil },
			Store:     promptBudgetStore{summary: *plan, found: true},
		})
	}
	rig.agent = &scriptedAgent{responses: responses}
	return rig
}

// run starts a turn whose user message is content and processes it with a
// new orchestrator (so nothing but stored data can carry the card's answer).
// answer, when not empty, is the next_step option the person picked on the
// card with this message.
func (rig *oneStepRig) run(t *testing.T, mode, content, answer string, step *chat.ChoiceStep) string {
	t.Helper()
	ctx := context.Background()
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "" {
		if _, _, err := rig.repository.SetSessionApprovalMode(ctx, "owner-a", session.ID, mode); err != nil {
			t.Fatal(err)
		}
	}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: content,
		Options: chat.TurnOptions{CreativeDomain: "model-3d"}})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "" {
		messages, err := rig.service.ListMessages(ctx, "owner-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		userID := ""
		for _, message := range messages {
			if message.Role == rig.schema.Roles.User {
				userID = message.ID
			}
		}
		rig.store.mu.Lock()
		rig.store.choices = []chat.Choice{{
			ID: "choice-1", OwnerID: "owner-a", SessionID: session.ID, Kind: "next_step", Capability: "next_step", StepIndex: -1,
			Options: []chat.ChoiceOption{
				{ID: "balanced", Label: "保轮廓：约 5,000 三角面", Step: step, Arguments: json.RawMessage(`{}`)},
				{ID: "bake", Label: "保留现在这套木纹", Arguments: json.RawMessage(`{}`)},
			},
			State: chat.ChoiceAnswered, AnswerOptionID: answer, AnswerSource: chat.ChoiceAnsweredOnCard, AnswerMessageID: userID,
		}}
		rig.store.mu.Unlock()
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: rig.schema, Model: "m", Agent: rig.agent, Tools: rig.tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{CreativeDomain: "model-3d"}}); err != nil {
		t.Fatal(err)
	}
	return session.ID
}

// toolResults are the tool messages the Agent read in its last request.
func (rig *oneStepRig) toolResults() []string {
	var results []string
	last := rig.agent.requests[len(rig.agent.requests)-1]
	for _, message := range last.Messages {
		if message.Role == rig.schema.Roles.Tool {
			results = append(results, message.Content)
		}
	}
	return results
}

func remeshStep() *chat.ChoiceStep {
	return &chat.ChoiceStep{Tool: "process_model", Capability: "remesh"}
}

func remeshThenRetexture() []agents.Completion {
	return []agents.Completion{
		{ToolCalls: []agents.ToolCall{{ID: "call-remesh", Name: "process_model", Arguments: []byte(`{"step":"remesh"}`)}}},
		{ToolCalls: []agents.ToolCall{{ID: "call-retexture", Name: "retexture_model", Arguments: []byte(`{"prompt":"wood"}`)}}},
		{ToolCalls: []agents.ToolCall{{ID: "call-remesh-again", Name: "process_model", Arguments: []byte(`{"step":"resize"}`)}}},
		{Content: "已完成重拓扑，接下来要换贴图吗？"},
	}
}

// The reported turn: the person picked remesh on the card; the Agent's
// retexture and its second processing step are refused and never run.
func TestCardAnswerTurnRunsOnlyTheChosenStep_549(t *testing.T) {
	rig := newOneStepRig(t, nil, remeshThenRetexture()...)
	rig.run(t, "auto", "选择：保轮廓", "balanced", remeshStep())
	if !slices.Equal(rig.tools.calls, []string{"process_model"}) {
		t.Fatalf("ran %v, want only the chosen process_model", rig.tools.calls)
	}
	results := rig.toolResults()
	if len(results) != 3 || results[0] != "ran process_model" {
		t.Fatalf("tool results = %q", results)
	}
	for _, refused := range results[1:] {
		if !strings.Contains(refused, "保轮廓") || !strings.Contains(refused, "ask_choice") {
			t.Fatalf("refusal = %q", refused)
		}
	}
	if !strings.Contains(results[1], "retexture_model") || !strings.Contains(results[2], "process_model") {
		t.Fatalf("refusals do not name the refused tool: %q", results)
	}
}

// An option that names no step (an explanation, a hand-off) authorizes no paid
// step at all in its answer turn.
func TestCardAnswerWithoutAStepAuthorizesNoPaidStep_549(t *testing.T) {
	rig := newOneStepRig(t, nil, remeshThenRetexture()...)
	rig.run(t, "auto", "选择：保留现在这套木纹", "bake", remeshStep())
	if len(rig.tools.calls) != 0 {
		t.Fatalf("ran %v after an option without a step", rig.tools.calls)
	}
}

// A turn the person started with their own words is not scoped by a card.
func TestTypedInstructionTurnIsNotScopedByACard_549(t *testing.T) {
	rig := newOneStepRig(t, nil, remeshThenRetexture()...)
	rig.run(t, "auto", "重拓扑后换贴图再缩放", "", nil)
	if !slices.Equal(rig.tools.calls, []string{"process_model", "retexture_model", "process_model"}) {
		t.Fatalf("ran %v, want every step of a typed instruction", rig.tools.calls)
	}
}

// A step the person approved as part of a production plan still runs in a
// card-answer turn of a budget-mode session.
func TestPlannedStepIsNotScopedByACard_549(t *testing.T) {
	plan := &budget.Summary{ID: "pb-1", State: budget.StateActive,
		Plan: budget.Plan{Title: "计划", Steps: []budget.PlanStep{{Capability: "retexture", Estimate: budget.Legacy(15)}}}}
	rig := newOneStepRig(t, plan, remeshThenRetexture()...)
	rig.run(t, "budget", "选择：保轮廓", "balanced", remeshStep())
	if !slices.Equal(rig.tools.calls, []string{"process_model", "retexture_model"}) {
		t.Fatalf("ran %v, want the chosen remesh and the planned retexture", rig.tools.calls)
	}
	// The same plan decides nothing in auto mode.
	rig = newOneStepRig(t, plan, remeshThenRetexture()...)
	rig.run(t, "auto", "选择：保轮廓", "balanced", remeshStep())
	if !slices.Equal(rig.tools.calls, []string{"process_model"}) {
		t.Fatalf("auto mode ran %v", rig.tools.calls)
	}
}
