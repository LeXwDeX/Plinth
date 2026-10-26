package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #329 E': the Agent is told, in the description of a tool, what the turn's
// own model picks mean for it (how many reference pictures the 3D model takes),
// so it does not promise what the picked model does not do. The note comes
// from the tool executor for the frozen turn options; tools it says nothing
// about keep their declared description.
func TestAToolDescriptionCarriesTheTurnsNote(t *testing.T) {
	rig := &scopeRig{repository: newMemoryChatRepository(), schema: configuredChatSchema(t), tools: &scopeToolExecutor{}}
	rig.service = newChatServiceWith(t, rig.repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(rig.schema.AgentScope)
	})
	rig.agent = &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	var seen []chat.TurnOptions
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: rig.service, Schema: rig.schema, Model: "m", Agent: rig.agent, Tools: rig.tools,
		ToolNote: func(tool catalog.ChatToolSchema, options chat.TurnOptions) string {
			if tool.Name != "create_model_3d_from_image" {
				return ""
			}
			seen = append(seen, options)
			return "NOTE for " + options.DomainModels["model-3d"]
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := rig.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	options := chat.TurnOptions{CreativeDomain: "image", DomainModels: map[string]string{"model-3d": "meshy-7.1"}}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID, Options: options}); err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{}
	for _, tool := range rig.schema.Tools {
		declared[tool.Name] = tool.Description
	}
	found := false
	for _, tool := range rig.agent.requests[0].Tools {
		switch {
		case tool.Name == "create_model_3d_from_image":
			found = true
			if !strings.HasPrefix(tool.Description, declared[tool.Name]) || !strings.HasSuffix(tool.Description, " NOTE for meshy-7.1") {
				t.Fatalf("description = %q", tool.Description)
			}
		case declared[tool.Name] != "" && tool.Description != declared[tool.Name]:
			t.Fatalf("%s description changed: %q", tool.Name, tool.Description)
		}
	}
	if !found || len(seen) == 0 {
		t.Fatalf("the image-to-3d tool was not offered with its note (asked %d times)", len(seen))
	}
}
