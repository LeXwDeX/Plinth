package chat_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// newDefaultTextRig is newScopeRig with the shipped Agent system prompt, so a
// scripted ("fake LLM") turn sees exactly the words the Schema gives a real one.
func newDefaultTextRig(t *testing.T, responses ...agents.Completion) *scopeRig {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rig := &scopeRig{repository: newMemoryChatRepository(), schema: configuredChatSchema(t), tools: &scopeToolExecutor{}}
	shipped := taskCatalog.Application().Chat
	rig.schema.Agent.SystemPrompt = shipped.Agent.SystemPrompt
	rig.schema.AgentScope = shipped.AgentScope
	rig.schema.ApprovalPolicy = shipped.ApprovalPolicy
	rig.schema.Tools = shipped.Tools
	rig.service = newChatServiceWith(t, rig.repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(rig.schema.AgentScope)
	})
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

// #150 scenario: the person asks for a Remesh -> Rigging plan in a 2D-scope
// session. The scripted model calls the 3D plan tool, the server refuses it by
// scope, and what the model reads (system context and refusal) must keep the
// Agent scope apart from the approval mode.
func TestScopeRefusalOfARemeshRiggingPlanDoesNotPointAtTheApprovalMode(t *testing.T) {
	rig := newDefaultTextRig(t,
		agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-1", Name: "plan_model_processing", Arguments: []byte(`{"steps":[{"capability":"remesh"},{"capability":"rigging"}]}`)}}},
		agents.Completion{Content: "explained"},
	)
	rig.turn(t, "2d")
	if len(rig.tools.calls) != 0 {
		t.Fatalf("an out-of-scope plan ran: %v", rig.tools.calls)
	}
	system := rig.agent.requests[0].Messages[0].Content
	for _, want := range []string{"Agent 范围模式", "批准方式", "与付费步骤是否需要批准无关", "不要说需要切换到手动模式"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	var refusal string
	for _, message := range rig.agent.requests[1].Messages {
		if message.Role == rig.schema.Roles.Tool {
			refusal = message.Content
		}
	}
	if !strings.Contains(refusal, "plan_model_processing") || !strings.Contains(refusal, "not the approval mode") {
		t.Fatalf("refusal = %q", refusal)
	}
}

// In a 3D-scope session the plan tool is offered with a description that
// says approval is the approval mode's business, not the scope's.
func TestPlanToolDescriptionKeepsScopeAndApprovalApart(t *testing.T) {
	rig := newDefaultTextRig(t, agents.Completion{Content: "ok"})
	rig.turn(t, "3d")
	var found bool
	for _, tool := range rig.agent.requests[0].Tools {
		if tool.Name != "plan_model_processing" {
			continue
		}
		found = true
		for _, want := range []string{"approved by policy in both modes", "unrelated to the Agent scope"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("plan_model_processing omits %q", want)
			}
		}
	}
	if !found {
		t.Fatal("plan_model_processing was not offered in 3d scope")
	}
}
