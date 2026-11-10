package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #103 review B4: the plan tool's Agent-facing message for each state comes
// from chat.approval_policy.budget.tool_messages, so a Schema can say it in
// another language and the execution layer keeps no product copy.
func TestPlanProposedContentUsesTheSchemasStateMessages(t *testing.T) {
	messages := catalog.ChatApprovalBudgetToolMessages{
		Proposed: "schema-proposed", Active: "schema-active",
		Exhausted: "schema-exhausted", Closed: "schema-closed",
	}
	for state, want := range map[string]string{
		budget.StatePending:   "schema-proposed",
		budget.StateActive:    "schema-active",
		budget.StateExhausted: "schema-exhausted",
		// A denied plan is closed for the Agent: it may not be proposed again.
		budget.StateDenied: "schema-closed",
		budget.StateClosed: "schema-closed",
	} {
		content := planProposedContent(budget.ProductionBudget{ID: "pb", State: state}, messages, "")
		if content["message"] != want {
			t.Fatalf("%s message = %v, want %q", state, content["message"], want)
		}
	}

	// A historical Catalog without tool_messages still gets an English line
	// the Agent can act on, one per state.
	for state, want := range map[string]string{
		budget.StatePending:   "wait for the person",
		budget.StateActive:    "is approved",
		budget.StateExhausted: "used up",
		budget.StateDenied:    "no longer pending",
		budget.StateClosed:    "no longer pending",
	} {
		content := planProposedContent(budget.ProductionBudget{ID: "pb", State: state},
			catalog.ChatApprovalBudgetToolMessages{}, "")
		message, _ := content["message"].(string)
		if !strings.Contains(message, want) {
			t.Fatalf("%s fallback = %q, want %q", state, message, want)
		}
	}
}
