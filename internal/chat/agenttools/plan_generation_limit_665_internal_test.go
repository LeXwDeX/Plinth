package agenttools

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: a processing plan counts each of its paid steps against
// max_generation_calls_per_turn when it is created, and a revision that adds a
// paid attempt (insert, retry) counts one more; replace, skip and waive do not.
func TestProcessingPlanAndRevisionsCountAgainstTheTurnLimit665(t *testing.T) {
	planTool := catalog.ChatToolSchema{Processing: &catalog.ChatToolProcessingSchema{
		Plan: &catalog.ChatToolProcessingPlanSchema{StepsArgument: "steps"}}}
	plan := chat.ToolCommand{Tool: planTool, Call: agents.ToolCall{ID: "plan",
		Arguments: []byte(`{"steps":[{"capability":"remesh"},{"capability":"rigging"},{"capability":"resize"}]}`)}}
	if got := processingPlanWeight(plan); got != 3 {
		t.Fatalf("plan weight = %d, want its three steps", got)
	}
	if got := processingPlanWeight(chat.ToolCommand{Call: agents.ToolCall{Arguments: []byte(`{"steps":[1,2]}`)}}); got != 1 {
		t.Fatalf("a non-plan call weighs %d, want 1", got)
	}
	executor := &Executor{maxGeneration: 4, usage: map[string]generationUsage{}}
	if executor.generationLimitReachedForCallWeighted("s", "turn", "plan", 0, 3) {
		t.Fatal("a three-step plan did not fit a limit of four")
	}
	if executor.generationLimitReachedForCallWeighted("s", "turn", "plan-2", 0, 2) {
		// 3 + 2 > 4: correctly refused below.
	} else {
		t.Fatal("a second plan past the limit was accepted")
	}
	if executor.generationLimitReachedForCallWeighted("s", "turn", "plan", 0, 3) {
		t.Fatal("the same plan call was counted twice")
	}

	called := 0
	executor = &Executor{maxGeneration: 1, usage: map[string]generationUsage{}, limitContent: "limit reached",
		reviseProcessingPlan: func(context.Context, chat.ToolCommand) (chat.ToolResult, error) {
			called++
			return chat.ToolResult{AgentContent: "revised"}, nil
		}}
	revise := func(id, operation string) chat.ToolResult {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "o", SessionID: "s",
			AssistantMessageID: "turn", Call: agents.ToolCall{ID: id, Name: ToolReviseProcessingPlan,
				Arguments: []byte(`{"operation":"` + operation + `"}`)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := revise("r1", "retry"); got.AgentContent != "revised" {
		t.Fatalf("first retry = %+v", got)
	}
	if got := revise("r2", "insert"); got.AgentContent != "limit reached" || !got.NoSubmission {
		t.Fatalf("insert past the limit = %+v", got)
	}
	for _, operation := range []string{"replace", "skip", "waive_checkpoint"} {
		if got := revise("free-"+operation, operation); got.AgentContent != "revised" {
			t.Fatalf("%s was counted: %+v", operation, got)
		}
	}
	if called != 4 {
		t.Fatalf("revisions run = %d, want 4", called)
	}
}
