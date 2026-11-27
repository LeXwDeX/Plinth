package chat_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// checkpointToolExecutor refuses every call after the first at a generation
// checkpoint of 1, like agenttools does at the approval mode's count.
type checkpointToolExecutor struct {
	mu    sync.Mutex
	calls int
	modes []string
}

func (executor *checkpointToolExecutor) Execute(_ context.Context, command chat.ToolCommand,
	_ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	executor.modes = append(executor.modes, command.ApprovalMode)
	if executor.calls > 1 {
		return chat.ToolResult{AgentContent: "limit reached", NoSubmission: true, GenerationCheckpoint: 1}, nil
	}
	return chat.ToolResult{AgentContent: "done"}, nil
}

// #694: a round whose call reached the approval mode's generation checkpoint
// ends the reply with the Schema note naming the count; the model is not asked
// again, so the stop does not depend on it remembering to ask. Every call is
// told the session's approval mode.
func TestReplyEndsAtTheGenerationCheckpointWithTheSchemaNote(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.GenerationCheckpointReached = "configured-checkpoint {count}"
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: "first", ToolCalls: []agents.ToolCall{budgetCall("r0", tool, "r0")}},
		{Content: "second", ToolCalls: []agents.ToolCall{budgetCall("r1", tool, "r1")}},
		{Content: "summary that must not be asked for"},
	}}
	tools := &checkpointToolExecutor{}
	command, err := processOnce(t, service, schema, agent, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("agent requests = %d, want 2 (no follow-up after the checkpoint)", len(agent.requests))
	}
	reply := storedReply(t, service, command)
	if !strings.HasSuffix(reply.Content, "configured-checkpoint 1") || strings.Contains(reply.Content, "summary") {
		t.Fatalf("reply = %q", reply.Content)
	}
	mode, err := service.SessionApprovalMode(context.Background(), command.OwnerID, command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, seen := range tools.modes {
		if seen != mode || seen == "" {
			t.Fatalf("tool saw approval mode %q, session has %q", seen, mode)
		}
	}
}
