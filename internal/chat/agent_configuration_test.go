package chat_test

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"path/filepath"
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
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "personal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	config, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	skill, err := config.CreatePersonalSkill(ctx, "owner-config", agentconfiguration.CreatePersonalSkillInput{Name: "Style", Prompt: "original personal style", RecommendedSize: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	configured := func(_ context.Context, owner string) (chat.AgentConfigurationSnapshot, error) {
		if owner != "owner-config" {
			t.Fatalf("configuration owner = %q", owner)
		}
		calls++
		snapshot, err := config.SnapshotForOwner(ctx, owner)
		if err != nil {
			return chat.AgentConfigurationSnapshot{}, err
		}
		return chat.AgentConfigurationSnapshot{Revision: snapshot.Revision, SystemPrompt: "prompt", DomainContext: map[string]string{}, Skills: snapshot.Configuration.Skills, AgentTools: []catalog.ChatAgentToolSchema{schema.AgentTools[0]}}, nil
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
	if turn.Checkpoint.Configuration == nil || turn.Checkpoint.Configuration.Skills[len(turn.Checkpoint.Configuration.Skills)-1].Prompt != "original personal style" {
		t.Fatal("missing durable snapshot")
	}
	frozenRevision := turn.Checkpoint.Configuration.Revision
	queuedSession, err := service.CreateSession(ctx, "owner-config", "C", "queued")
	if err != nil {
		t.Fatal(err)
	}
	queued := startTurnFor(t, service, "owner-config", queuedSession.ID, "draw later")
	_, err = config.UpdatePersonalSkill(ctx, "owner-config", skill.ID, agentconfiguration.CreatePersonalSkillInput{Name: "Style", Prompt: "changed personal style", RecommendedSize: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	command.ConsentWaitDeadline = time.Now().Add(time.Minute)
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(tool.revisions) != 2 || tool.revisions[1] != frozenRevision {
		t.Fatalf("resume replaced config: reads=%d tools=%v", calls, tool.revisions)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, assistant)
	if err != nil || turn.Checkpoint.Configuration.Skills[len(turn.Checkpoint.Configuration.Skills)-1].Prompt != "original personal style" {
		t.Fatalf("resumed turn changed frozen Skill: %#v %v", turn.Checkpoint.Configuration, err)
	}
	queuedCommand := chat.ProcessTurn{OwnerID: "owner-config", SessionID: queuedSession.ID, AssistantMessageID: queued, Options: chat.TurnOptions{CreativeDomain: "image"}}
	if err := makeOrchestrator().Process(ctx, queuedCommand); err != nil {
		t.Fatal(err)
	}
	queuedTurn, err := service.GetTurn(ctx, queuedCommand.OwnerID, queuedCommand.SessionID, queued)
	if err != nil || queuedTurn.Checkpoint.Configuration == nil || queuedTurn.Checkpoint.Configuration.Revision == frozenRevision || queuedTurn.Checkpoint.Configuration.Skills[len(queuedTurn.Checkpoint.Configuration.Skills)-1].Prompt != "changed personal style" {
		t.Fatalf("queued turn did not freeze updated Skill: %#v %v", queuedTurn.Checkpoint.Configuration, err)
	}
}
