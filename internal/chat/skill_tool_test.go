package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// Choosing the Schema's three-view Skill tells the Agent, in the system
// context, to use the bound tool; without the Skill, or when the session scope
// withholds the tool, the block is absent.
func TestToolBindingSkillDirectsTheAgentToItsTool(t *testing.T) {
	run := func(scope, skillID string) string {
		schema := configuredChatSchema(t)
		repository := newMemoryChatRepository()
		service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
			config.AgentScope = agentscope.FromSchema(schema.AgentScope)
		})
		agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
			Configuration: func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
				return chat.AgentConfigurationSnapshot{
					Revision: "r", SystemPrompt: schema.Agent.SystemPrompt,
					Skills: schema.Skills.SystemSkills, Tools: schema.Tools, AgentTools: schema.AgentTools,
				}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.SetAgentScope(ctx, "owner-a", session.ID, scope); err != nil {
			t.Fatal(err)
		}
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "go",
			Options: chat.TurnOptions{CreativeDomain: "image", SkillID: skillID}})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
			Options: chat.TurnOptions{CreativeDomain: "image", SkillID: skillID}}); err != nil {
			t.Fatal(err)
		}
		return agent.requests[0].Messages[0].Content
	}
	skill := configuredChatSchema(t).Skills.SystemSkills[0]
	directive := "本轮必须用工具 " + skill.Tool
	if system := run("auto", skill.ID); !strings.Contains(system, directive) || !strings.Contains(system, skill.Prompt) {
		t.Fatalf("auto scope with the Skill: no directive in %q", system)
	}
	if system := run("2d", skill.ID); !strings.Contains(system, directive) {
		t.Fatalf("2d scope allows the tool but no directive in %q", system)
	}
	if system := run("3d", skill.ID); strings.Contains(system, directive) {
		t.Fatalf("3d scope withholds the tool but the Agent was told to use it: %q", system)
	}
	if system := run("auto", ""); strings.Contains(system, directive) {
		t.Fatalf("no Skill chosen but a directive was added: %q", system)
	}
}
