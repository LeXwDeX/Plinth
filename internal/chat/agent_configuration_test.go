package chat_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
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

// A turn that was submitted but not yet processed freezes the Skill content as it
// is at its first Process; a turn that already froze keeps its old content.
func TestPersonalSkillEditAfterSubmitAppliesAtFirstFreeze(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent-edit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	size := product.Application().Chat.Skills.Creation.RecommendedSize
	created, err := configuration.CreatePersonalSkill(ctx, "owner-config", agentconfiguration.CreatePersonalSkillInput{Name: "Style", Prompt: "old prompt", RecommendedSize: size})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	session, err := service.CreateSession(ctx, "owner-config", "C", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant := startTurnFor(t, service, "owner-config", session.ID, "draw")
	// Submitted, not yet processed: the edit arrives before the first freeze.
	if _, err := configuration.UpdatePersonalSkill(ctx, "owner-config", created.ID, agentconfiguration.CreatePersonalSkillInput{Name: "Style", Prompt: "new prompt", RecommendedSize: size}); err != nil {
		t.Fatal(err)
	}
	frozen := func(ctx context.Context, owner string) (chat.AgentConfigurationSnapshot, error) {
		snapshot, err := configuration.SnapshotForOwner(ctx, owner)
		if err != nil {
			return chat.AgentConfigurationSnapshot{}, err
		}
		return chat.AgentConfigurationSnapshot{Revision: snapshot.Revision, SystemPrompt: snapshot.Configuration.SystemPrompt, DomainContext: snapshot.Configuration.DomainContext, Skills: snapshot.Configuration.Skills, AgentTools: []catalog.ChatAgentToolSchema{schema.AgentTools[0]}}, nil
	}
	agent := &scriptedSuspendAgent{initial: agents.Completion{Content: "done"}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: &configurationSuspendExecutor{}, Configuration: frozen})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-config", SessionID: session.ID, AssistantMessageID: assistant, Options: chat.TurnOptions{CreativeDomain: "image"}}
	if err := orchestrator.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, assistant)
	if err != nil || turn.Checkpoint.Configuration == nil {
		t.Fatalf("missing frozen configuration: %#v %v", turn.Checkpoint.Configuration, err)
	}
	found := false
	for _, skill := range turn.Checkpoint.Configuration.Skills {
		if skill.ID != created.ID {
			continue
		}
		found = true
		if skill.Prompt != "new prompt" {
			t.Fatalf("first freeze used %q, want the edited content", skill.Prompt)
		}
	}
	if !found {
		t.Fatalf("personal Skill missing from the frozen configuration: %#v", turn.Checkpoint.Configuration.Skills)
	}
	// A later edit cannot rewrite the frozen turn.
	if _, err := configuration.UpdatePersonalSkill(ctx, "owner-config", created.ID, agentconfiguration.CreatePersonalSkillInput{Name: "Style", Prompt: "newer prompt", RecommendedSize: size}); err != nil {
		t.Fatal(err)
	}
	again, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, assistant)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range again.Checkpoint.Configuration.Skills {
		if skill.ID == created.ID && skill.Prompt != "new prompt" {
			t.Fatalf("a later edit rewrote the frozen turn: %q", skill.Prompt)
		}
	}
}
