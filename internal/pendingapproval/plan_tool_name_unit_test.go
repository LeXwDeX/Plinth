package pendingapproval

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #103 review B4: the plan card names the tool the Schema marks with the
// proposal implementation key, so renaming that tool needs no code change.
func TestPlanCardToolNameComesFromTheProposalImplementationKey(t *testing.T) {
	service := &Service{schema: catalog.ChatSchema{
		AgentTools: []catalog.ChatAgentToolSchema{
			{Name: "other_agent_tool", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "other"}},
			{Name: "renamed_proposal", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "plan.propose"}},
		},
		Tools: []catalog.ChatToolSchema{
			{Name: "ordinary_tool", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "other"}},
		},
	}}
	if got := service.planToolName(); got != "renamed_proposal" {
		t.Fatalf("plan tool name = %q, want the declared proposal tool", got)
	}

	// The proposal may also be declared among the ordinary chat tools.
	service.schema.AgentTools = nil
	service.schema.Tools = []catalog.ChatToolSchema{
		{Name: "renamed_proposal", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "plan.propose"}},
	}
	if got := service.planToolName(); got != "renamed_proposal" {
		t.Fatalf("ordinary-tool plan name = %q", got)
	}
}
