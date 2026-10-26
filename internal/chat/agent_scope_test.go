package chat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// scopeToolExecutor remembers every tool the orchestrator really ran.
type scopeToolExecutor struct {
	mu    sync.Mutex
	calls []string
}

func (executor *scopeToolExecutor) Execute(
	_ context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls = append(executor.calls, command.Call.Name)
	return chat.ToolResult{AgentContent: "ran " + command.Call.Name}, nil
}

type scopeRig struct {
	repository   *memoryChatRepository
	service      *chat.Service
	orchestrator *chat.Orchestrator
	agent        *scriptedAgent
	tools        *scopeToolExecutor
	schema       catalog.ChatSchema
}

func newScopeRig(t *testing.T, responses ...agents.Completion) *scopeRig {
	t.Helper()
	rig := &scopeRig{repository: newMemoryChatRepository(), schema: configuredChatSchema(t), tools: &scopeToolExecutor{}}
	rig.service = newChatServiceWith(t, rig.repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(rig.schema.AgentScope)
	})
	if len(responses) == 0 {
		responses = []agents.Completion{{Content: "ok"}}
	}
	rig.agent = &scriptedAgent{responses: responses}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: rig.schema, Model: "m", Agent: rig.agent, Tools: rig.tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.orchestrator = orchestrator
	return rig
}

// turn creates a session in the scope, starts a turn and runs it. The options
// the "client" sends deliberately name another scope: the server must ignore it.
func (rig *scopeRig) turn(t *testing.T, scope string) string {
	t.Helper()
	ctx := context.Background()
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if scope != "" {
		if _, _, err := rig.service.SetAgentScope(ctx, "owner-a", session.ID, scope); err != nil {
			t.Fatal(err)
		}
	}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello",
		Options: chat.TurnOptions{CreativeDomain: "image", AgentScope: "forged"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{CreativeDomain: "image", AgentScope: "forged"}}); err != nil {
		t.Fatal(err)
	}
	return session.ID
}

func toolNames(request agents.Request) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

var (
	twoDTools      = []string{"create_multiview_images", "create_three_view", "edit_multiview_images", "refine_image", "remove_background"}
	threeDTools    = []string{"build_creative_lab_item", "create_creative_lab_design", "convert_model_format", "create_gaussian_splat", "create_model_3d", "create_model_3d_from_image", "create_motion_clip", "get_processing_plan", "list_session_models", "plan_model_processing", "process_model", "retexture_model"}
	utilityToolSet = []string{"find_history_images", "get_task", "list_offerings", "observe_task", "recover_task_result", "request_consent", "set_params", "update_session_notes"}
)

func union(sets ...[]string) []string {
	var all []string
	for _, set := range sets {
		all = append(all, set...)
	}
	slices.Sort(all)
	return all
}

func TestEachTurnIsOfferedOnlyTheToolsOfItsSessionScope(t *testing.T) {
	for scope, want := range map[string][]string{
		"2d":   union(twoDTools, utilityToolSet),
		"3d":   union(threeDTools, utilityToolSet),
		"auto": union(twoDTools, threeDTools, utilityToolSet),
		// A session that never chose a scope is in the Schema default: auto.
		"": union(twoDTools, threeDTools, utilityToolSet),
	} {
		t.Run("scope "+scope, func(t *testing.T) {
			rig := newScopeRig(t)
			rig.turn(t, scope)
			got := toolNames(rig.agent.requests[0])
			if !slices.Equal(got, want) {
				t.Fatalf("offered tools = %v, want %v", got, want)
			}
			withheld := slices.Clone(rig.agent.requests[0].ScopeWithheld)
			slices.Sort(withheld)
			all := union(twoDTools, threeDTools, utilityToolSet)
			for _, name := range all {
				if slices.Contains(got, name) == slices.Contains(withheld, name) {
					t.Fatalf("tool %q must be either offered or withheld (offered=%v withheld=%v)", name, got, withheld)
				}
			}
		})
	}
}

func TestSystemContextTellsTheAgentItsScopeEveryTurn(t *testing.T) {
	for scope, label := range map[string]string{"2d": "2D 模式", "3d": "3D 模式", "auto": "自动模式"} {
		t.Run(scope, func(t *testing.T) {
			rig := newScopeRig(t)
			rig.turn(t, scope)
			system := rig.agent.requests[0].Messages[0].Content
			if !strings.Contains(system, "「"+label+"」") {
				t.Fatalf("the system context does not name %q: %q", label, system)
			}
			mode, _ := agentscope.FromSchema(rig.schema.AgentScope).Mode(scope)
			if !strings.Contains(system, mode.Description) {
				t.Fatalf("the system context lacks the Schema description of %s: %q", scope, system)
			}
			// The Agent is told what is on and off the table, by tool name.
			available, blocked, found := strings.Cut(system[strings.Index(system, "本轮你可以使用的工具："):], "本轮不可使用的工具：")
			if !found {
				t.Fatalf("tool lists missing: %q", system)
			}
			switch scope {
			case "2d":
				if !strings.Contains(available, "refine_image") || strings.Contains(available, "create_model_3d") || !strings.Contains(blocked, "create_model_3d") {
					t.Fatalf("2d lists wrong: available=%q blocked=%q", available, blocked)
				}
			case "3d":
				if strings.Contains(available, "refine_image") || !strings.Contains(blocked, "refine_image") || !strings.Contains(available, "create_model_3d") {
					t.Fatalf("3d lists wrong: available=%q blocked=%q", available, blocked)
				}
			case "auto":
				if !strings.Contains(blocked, rig.schema.AgentScope.EmptyListText) {
					t.Fatalf("auto must block nothing: %q", blocked)
				}
			}
		})
	}
	// The line follows the session: switching the scope changes the next turn.
	rig := newScopeRig(t, agents.Completion{Content: "one"}, agents.Completion{Content: "two"})
	sessionID := rig.turn(t, "2d")
	if _, _, err := rig.service.SetAgentScope(context.Background(), "owner-a", sessionID, "3d"); err != nil {
		t.Fatal(err)
	}
	assistant, err := rig.service.StartTurn(context.Background(), chat.StartTurn{OwnerID: "owner-a", SessionID: sessionID, Content: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.orchestrator.Process(context.Background(), chat.ProcessTurn{OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	if first, second := rig.agent.requests[0].Messages[0].Content, rig.agent.requests[1].Messages[0].Content; !strings.Contains(first, "「2D 模式」") || !strings.Contains(second, "「3D 模式」") {
		t.Fatalf("scope line did not follow the switch:\nfirst=%q\nsecond=%q", first, second)
	}
}

func TestServerRefusesAnOutOfScopeToolCallEvenWhenTheModelNamesIt(t *testing.T) {
	cases := []struct {
		scope, tool string
		refused     bool
	}{
		{"2d", "create_model_3d", true},
		{"2d", "convert_model_format", true},
		{"2d", "list_session_models", true},
		{"2d", "plan_model_processing", true},
		{"2d", "get_processing_plan", true},
		{"3d", "refine_image", true},
		{"3d", "remove_background", true},
		{"3d", "create_three_view", true},
		{"2d", "refine_image", false},
		{"3d", "create_model_3d_from_image", false},
		// Utility tools run in every scope.
		{"2d", "update_session_notes", false},
		{"3d", "find_history_images", false},
		{"3d", "get_task", false},
		{"auto", "create_model_3d", false},
		{"auto", "refine_image", false},
	}
	for _, tc := range cases {
		t.Run(tc.scope+"/"+tc.tool, func(t *testing.T) {
			rig := newScopeRig(t,
				agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-1", Name: tc.tool, Arguments: []byte(`{"prompt":"x"}`)}}},
				agents.Completion{Content: "explained"},
			)
			sessionID := rig.turn(t, tc.scope)
			ran := slices.Contains(rig.tools.calls, tc.tool)
			if ran == tc.refused {
				t.Fatalf("tool %s in %s: ran=%v, refused should be %v (calls %v)", tc.tool, tc.scope, ran, tc.refused, rig.tools.calls)
			}
			if len(rig.agent.requests) != 2 {
				t.Fatalf("the turn must go on after a refusal so the Agent can explain: %d requests", len(rig.agent.requests))
			}
			var result string
			for _, message := range rig.agent.requests[1].Messages {
				if message.Role == rig.schema.Roles.Tool {
					result = message.Content
				}
			}
			if tc.refused {
				mode, _ := agentscope.FromSchema(rig.schema.AgentScope).Mode(tc.scope)
				if !strings.Contains(result, tc.tool) || !strings.Contains(result, mode.Label) || strings.HasPrefix(result, "ran ") {
					t.Fatalf("refusal result = %q", result)
				}
			} else if result != "ran "+tc.tool {
				t.Fatalf("allowed call result = %q", result)
			}
			// A refusal is an answer, not a failure: the turn finishes normally.
			messages, err := rig.service.ListMessages(context.Background(), "owner-a", sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if last := messages[len(messages)-1]; last.Content != "explained" {
				t.Fatalf("assistant message = %q", last.Content)
			}
		})
	}
}

func TestTheTurnKeepsTheScopeItStartedWith(t *testing.T) {
	rig := newScopeRig(t)
	ctx := context.Background()
	session, _ := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if _, _, err := rig.service.SetAgentScope(ctx, "owner-a", session.ID, "2d"); err != nil {
		t.Fatal(err)
	}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello",
		Options: chat.TurnOptions{AgentScope: "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	// The user switches while the turn is queued; the turn is unaffected.
	if _, _, err := rig.service.SetAgentScope(ctx, "owner-a", session.ID, "3d"); err != nil {
		t.Fatal(err)
	}
	turn, err := rig.service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.AgentScope != "2d" {
		t.Fatalf("stored turn scope = %q, want the session's scope at start (2d), not the client's", turn.Options.AgentScope)
	}
	if err := rig.orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	if got := toolNames(rig.agent.requests[0]); !slices.Equal(got, union(twoDTools, utilityToolSet)) {
		t.Fatalf("the running turn changed its tools: %v", got)
	}
}

func TestSessionsDefaultToAutoAndRememberTheirScope(t *testing.T) {
	rig := newScopeRig(t)
	ctx := context.Background()
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if session.AgentScope != "auto" {
		t.Fatalf("new session scope = %q, want auto", session.AgentScope)
	}
	updated, previous, err := rig.service.SetAgentScope(ctx, "owner-a", session.ID, "3d")
	if err != nil || updated.AgentScope != "3d" || previous != "auto" {
		t.Fatalf("SetAgentScope = %+v, previous %q, err %v", updated, previous, err)
	}
	stored, found, err := rig.service.Session(ctx, "owner-a", session.ID)
	if err != nil || !found || stored.AgentScope != "3d" {
		t.Fatalf("stored session = %+v found=%v err=%v", stored, found, err)
	}
	if _, _, err := rig.service.SetAgentScope(ctx, "owner-a", session.ID, "4d"); !errors.Is(err, chat.ErrInvalidAgentScope) {
		t.Fatalf("unknown scope accepted: %v", err)
	}
	if _, _, err := rig.service.SetAgentScope(ctx, "owner-b", session.ID, "2d"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("a foreign session was switched: %v", err)
	}
	if after, _, _ := rig.service.Session(ctx, "owner-a", session.ID); after.AgentScope != "3d" {
		t.Fatalf("a rejected switch changed the scope to %q", after.AgentScope)
	}
	if _, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-b", SessionID: session.ID, Content: "hi"}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("a foreign owner started a turn: %v", err)
	}
}
