package chat_test

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"testing"
	"time"
)

type configurationSuspendExecutor struct {
	calls     int
	revisions []string
}

func (e *configurationSuspendExecutor) Execute(_ context.Context, c chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	e.calls++
	if c.Configuration != nil {
		e.revisions = append(e.revisions, c.Configuration.Revision)
	}
	if e.calls == 1 {
		return chat.ToolResult{Suspend: true, WaitUntil: time.Now().Add(time.Minute)}, nil
	}
	return chat.ToolResult{AgentContent: "resumed"}, nil
}
func TestSuspendedTurnKeepsConfigurationAcrossNewOrchestrator(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	session, err := service.CreateSession(ctx, "owner-config", "C", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant := startTurnFor(t, service, "owner-config", session.ID, "draw")
	tool := &configurationSuspendExecutor{}
	calls := 0
	revision := "1"
	style := "original personal style"
	configured := func(_ context.Context, owner string) (chat.AgentConfigurationSnapshot, error) {
		if owner != "owner-config" {
			t.Fatalf("configuration owner = %q", owner)
		}
		calls++
		return chat.AgentConfigurationSnapshot{Revision: revision, SystemPrompt: "prompt-" + revision, DomainContext: map[string]string{}, Skills: []catalog.SkillSchema{{ID: "personal-style", Prompt: style}}, AgentTools: []catalog.ChatAgentToolSchema{schema.AgentTools[0]}}, nil
	}
	name := schema.AgentTools[0].Name
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{ID: "frozen-call", Name: name, Arguments: json.RawMessage(`{}`)}}}, resume: []agents.Completion{{Content: "finished"}}}
	makeOrchestrator := func() *chat.Orchestrator {
		o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tool, Configuration: configured})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	command := chat.ProcessTurn{OwnerID: "owner-config", SessionID: session.ID, AssistantMessageID: assistant, Options: chat.TurnOptions{CreativeDomain: "image"}}
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, assistant)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Checkpoint.Configuration == nil || turn.Checkpoint.Configuration.Revision != "1" ||
		turn.Checkpoint.Configuration.Skills[0].Prompt != "original personal style" {
		t.Fatal("missing durable snapshot")
	}
	revision = "2"
	style = "changed personal style"
	command.ConsentWaitDeadline = time.Now().Add(time.Minute)
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(tool.revisions) != 2 || tool.revisions[1] != "1" {
		t.Fatalf("resume replaced config: reads=%d tools=%v", calls, tool.revisions)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, assistant)
	if err != nil || turn.Checkpoint.Configuration.Skills[0].Prompt != "original personal style" {
		t.Fatalf("resumed turn changed frozen Skill: %#v %v", turn.Checkpoint.Configuration, err)
	}
}
