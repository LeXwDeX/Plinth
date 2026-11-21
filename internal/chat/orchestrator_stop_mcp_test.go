package chat_test

// #453: a turn that called an MCP preset tool (a read-only tool the Schema
// alone does not declare) could not be stopped. The stop reconciliation read
// the calls in the Orchestrator's base registry, did not know the MCP tool and
// took it for a call with no recoverable task identity, so the turn stayed
// running; the restart recovery failed the same way. The stop paths read the
// turn's own registry (its frozen Agent configuration, MCP presets included).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// mcpReadOnlyTool is an MCP preset tool as the application freezes it into a
// turn's Agent configuration snapshot: internal/mcp admits only mcp/read with
// the READ effect, session scope and no consent (internal/mcp/service.go).
func mcpReadOnlyTool(name string) catalog.ChatAgentToolSchema {
	return catalog.ChatAgentToolSchema{
		Name:        name,
		Description: "configured MCP read-only tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Execution: catalog.ChatToolExecutionSchema{
			ImplementationKey: "mcp/read",
			Effects:           []string{"READ"},
			Scope:             "session",
			Consent:           "none",
			Idempotency:       "none",
			Cancellation:      "context",
			AgentScope:        "utility",
		},
	}
}

// stopProbeExecutor plays a turn whose read-only calls answer at once and whose
// generation call reports a detached approval card. onGenerate runs while the
// turn is still running: the test stops there, or leaves the durable stop for
// the restart recovery. It also answers the stop reconciliation's card question
// and keeps what it was asked.
type stopProbeExecutor struct {
	generated  string
	detached   bool
	suspend    bool
	onGenerate func(context.Context, chat.ToolCommand)
	mu         sync.Mutex
	asked      []chat.ToolCommand
	calls      []string
}

func (executor *stopProbeExecutor) Execute(
	ctx context.Context, command chat.ToolCommand, _ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	executor.mu.Lock()
	executor.calls = append(executor.calls, command.Call.Name)
	suspend, hook := executor.suspend, executor.onGenerate
	executor.mu.Unlock()
	if command.Call.Name != executor.generated {
		return chat.ToolResult{AgentContent: `{"result":"configured ok"}`}, nil
	}
	executor.mu.Lock()
	executor.detached = true
	executor.mu.Unlock()
	if hook != nil {
		hook(ctx, command)
	}
	if suspend {
		return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
	}
	return chat.ToolResult{AgentContent: `{"result":"configured waiting"}`, NoSubmission: true}, nil
}

func (executor *stopProbeExecutor) DetachedOperationForCall(_ context.Context, command chat.ToolCommand) (bool, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.asked = append(executor.asked, command)
	return command.Call.Name == executor.generated && executor.detached, nil
}

func (executor *stopProbeExecutor) questions() []chat.ToolCommand {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]chat.ToolCommand(nil), executor.asked...)
}

func (executor *stopProbeExecutor) executed() []string {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]string(nil), executor.calls...)
}

// mcpStopTurn is the turn shape of the incident: update_production_brief and
// the MCP read-only call in the first round, then the generation call whose
// approval card was detached. The fake Agent writes no further call, so the
// turn ends on the card.
func mcpStopTurn(t *testing.T, service *chat.Service, generated, sessionID string) (*scriptedAgent, chat.ProcessTurn) {
	t.Helper()
	agent := &scriptedAgent{
		completions: []agents.Completion{{Content: "configured title"}},
		responses: []agents.Completion{
			{ToolCalls: []agents.ToolCall{
				{Name: "update_production_brief", Arguments: json.RawMessage(`{"brief":"a plain cube"}`)},
				{Name: "mcp_list_offerings", Arguments: json.RawMessage(`{}`)},
			}},
			{ToolCalls: []agents.ToolCall{
				{Name: generated, Arguments: json.RawMessage(`{"prompt":"a plain cube"}`)},
			}},
			{Content: "configured summary"},
		},
	}
	assistantID := startTurnFor(t, service, "owner-a", sessionID, "make a cube")
	return agent, chat.ProcessTurn{OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: assistantID}
}

// mcpStopConfiguration freezes the turn's Agent configuration the way the
// application's Configuration callback does: the Schema's enabled tools plus
// the MCP preset tools. The MCP tool is the one the Schema alone cannot declare.
func mcpStopConfiguration(schema catalog.ChatSchema) func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
	snapshot := chat.AgentConfigurationSnapshot{Revision: "mcp-1"}
	snapshot.Tools = append([]catalog.ChatToolSchema(nil), schema.Tools...)
	snapshot.AgentTools = append([]catalog.ChatAgentToolSchema(nil), schema.AgentTools...)
	snapshot.AgentTools = append(snapshot.AgentTools, mcpReadOnlyTool("mcp_list_offerings"))
	return func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
		return snapshot, nil
	}
}

// A turn that ran a read-only MCP tool and whose generation call left an
// approval card stops cleanly: the turn ends as stopped, the card is still
// waiting for the person and the stop reports no error.
func TestStoppedTurnWithAnMCPReadCallFinalizes(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	executor := &stopProbeExecutor{generated: "create_model_3d"}
	agent, command := mcpStopTurn(t, service, executor.generated, session.ID)
	var orchestrator *chat.Orchestrator
	executor.onGenerate = func(ctx context.Context, _ chat.ToolCommand) {
		if _, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil {
			t.Errorf("stop the turn: %v", err)
		}
	}
	var canceled int
	orchestrator, err = chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: executor,
		Configuration: mcpStopConfiguration(schema),
		TaskCanceler: func(context.Context, string, string) error {
			canceled++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, command); err != nil {
		t.Fatalf("stop reconciliation: %v", err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" || turn.EndReason != configuredEndReasons.Stopped {
		t.Fatalf("stopped turn: state=%q reason=%q err=%v", turn.State, turn.EndReason, err)
	}
	message := lastAssistant(t, service, command.OwnerID, command.SessionID)
	if !strings.Contains(message.Content, "configured stopped") {
		t.Fatalf("the stop text is missing: %q", message.Content)
	}
	if len(message.ToolCalls) != 3 {
		t.Fatalf("stored calls = %#v", message.ToolCalls)
	}
	names := map[string]bool{}
	for _, call := range message.ToolCalls {
		names[call.Name] = true
		if call.Name == executor.generated && (call.IdempotencyKey == "" || call.TaskID != "") {
			t.Fatalf("the generation call was not left to its card: %#v", call)
		}
	}
	if !names["mcp_list_offerings"] || !names["update_production_brief"] || !names[executor.generated] {
		t.Fatalf("the turn's calls changed: %#v", message.ToolCalls)
	}
	// The detached card is still waiting for the person: nothing was canceled
	// or settled by the stop, and every question carried the frozen snapshot.
	if canceled != 0 {
		t.Fatalf("the stop canceled %d Tasks of a detached card", canceled)
	}
	questions := executor.questions()
	if len(questions) == 0 {
		t.Fatal("the stop never asked about the approval card")
	}
	for _, question := range questions {
		if question.Configuration == nil || question.Configuration.Revision != "mcp-1" {
			t.Fatalf("the stop question lost the turn's snapshot: %#v", question.Configuration)
		}
	}
	if len(executor.executed()) != 3 {
		t.Fatalf("executed calls = %#v", executor.executed())
	}
	if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
		t.Fatalf("stopped turn remained active: %#v err=%v", turns, err)
	}
}

// The same turn shape whose stop could not be finalized in the running process
// (the process ended first) is finished by the restart recovery: RecoverTurns
// hands the turn to a fresh Orchestrator, which reconciles the calls with the
// frozen snapshot the turn saved.
func TestStoppedTurnWithAnMCPReadCallFinalizesAfterRestart(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	executor := &stopProbeExecutor{generated: "create_model_3d", suspend: true}
	agent, command := mcpStopTurn(t, service, executor.generated, session.ID)
	executor.onGenerate = func(ctx context.Context, _ chat.ToolCommand) {
		// The durable stop intent, as CancelTurn persists it; the turn is left
		// running for the restart instead of being finalized here.
		_, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID)
		if err != nil || !active {
			t.Errorf("request the durable stop: active=%v err=%v", active, err)
		}
	}
	var canceled int
	newOrchestrator := func() *chat.Orchestrator {
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: executor,
			Configuration: mcpStopConfiguration(schema),
			TaskCanceler: func(context.Context, string, string) error {
				canceled++
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return orchestrator
	}
	if err := newOrchestrator().Process(ctx, command); err != nil {
		t.Fatalf("the turn did not park for the restart: %v", err)
	}
	turns, err := service.RecoverTurns(ctx)
	if err != nil || len(turns) != 1 || !turns[0].StopRequested {
		t.Fatalf("recoverable stopped turn: %#v err=%v", turns, err)
	}
	if err := newOrchestrator().Process(ctx, command); err != nil {
		t.Fatalf("restart reconciliation: %v", err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" || turn.EndReason != configuredEndReasons.Stopped {
		t.Fatalf("recovered turn: state=%q reason=%q err=%v", turn.State, turn.EndReason, err)
	}
	if canceled != 0 {
		t.Fatalf("the recovery canceled %d Tasks of a detached card", canceled)
	}
	if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
		t.Fatalf("recovered turn remained active: %#v err=%v", turns, err)
	}
}

// A call the turn's registry does not know either still takes the protective
// branch: the stop stays recoverable rather than finalizing a turn that may
// have committed a paid Task.
func TestStoppedTurnWithAnUnknownCallStaysRecoverable(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	executor := &stopProbeExecutor{generated: "create_model_3d"}
	agent := &scriptedAgent{
		completions: []agents.Completion{{Content: "configured title"}},
		responses: []agents.Completion{
			{ToolCalls: []agents.ToolCall{{Name: "no-such-tool", Arguments: json.RawMessage(`{}`)}}},
			{ToolCalls: []agents.ToolCall{{Name: executor.generated, Arguments: json.RawMessage(`{"prompt":"a plain cube"}`)}}},
			{Content: "configured summary"},
		},
	}
	assistant := startTurnFor(t, service, "owner-a", session.ID, "make a cube")
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant}
	var orchestrator *chat.Orchestrator
	executor.onGenerate = func(ctx context.Context, _ chat.ToolCommand) {
		if _, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil {
			t.Errorf("stop the turn: %v", err)
		}
	}
	orchestrator, err = chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: executor,
		Configuration: mcpStopConfiguration(schema),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = orchestrator.Process(ctx, command)
	if err == nil || !strings.Contains(err.Error(), "no recoverable task identity") {
		t.Fatalf("unknown call reconciliation error = %v", err)
	}
	turns, err := service.RecoverTurns(ctx)
	if err != nil || len(turns) != 1 || !turns[0].StopRequested {
		t.Fatalf("unknown call must keep the stop recoverable: %#v err=%v", turns, err)
	}
}
