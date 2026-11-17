package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #327: the proposal call reaches the asset-list service through the Agent
// dispatcher; an unwired executor refuses without a submission.
func TestProposeAssetListDispatchesToTheService(t *testing.T) {
	var received chat.ToolCommand
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		ProposeAssetList: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			received = command
			return chat.ToolResult{AgentContent: `{"list_id":"alist_1"}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "list-1", Name: ToolProposeAssetList,
			Arguments: json.RawMessage(`{"title":"清单","items":[]}`)},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != `{"list_id":"alist_1"}` ||
		!result.NoSubmission || received.Call.ID != "list-1" {
		t.Fatalf("dispatch result=%+v received=%+v err=%v", result, received.Call, err)
	}

	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = unwired.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || result.Error != "asset lists unavailable" {
		t.Fatalf("unwired asset list tool = %+v, %v", result, err)
	}
}
