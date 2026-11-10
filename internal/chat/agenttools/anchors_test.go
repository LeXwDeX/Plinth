package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #250: the confirmed anchor write reaches the service through the Agent tool
// dispatcher; an unwired executor refuses without a submission.
func TestSetVisualAnchorDispatchesToTheService(t *testing.T) {
	var received chat.ToolCommand
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		WriteVisualAnchor: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			received = command
			return chat.ToolResult{AgentContent: `{"success":true}`}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "anchor-1", Name: ToolSetVisualAnchor,
			Arguments: json.RawMessage(`{"action":"create","name":"hero","kind":"identity"}`)},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != `{"success":true}` || received.Call.ID != "anchor-1" {
		t.Fatalf("dispatch result=%+v received=%+v err=%v", result, received.Call, err)
	}

	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = unwired.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || result.Error == "" {
		t.Fatalf("unwired anchor tool = %+v, %v", result, err)
	}
}
