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

// #328: the completion call reaches the chat service through the dispatcher;
// an unwired executor refuses without a submission.
func TestSubmitAssetListItemDispatchesToTheService(t *testing.T) {
	var received chat.ToolCommand
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		SubmitAssetListItem: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			received = command
			return chat.ToolResult{AgentContent: `{"item_id":"aitem_1"}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "submit-1", Name: ToolSubmitAssetListItem,
			Arguments: json.RawMessage(`{"status":"done"}`)},
		Options: chat.TurnOptions{AssetListItemID: "aitem_1"},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != `{"item_id":"aitem_1"}` ||
		!result.NoSubmission || received.Call.ID != "submit-1" ||
		received.Options.AssetListItemID != "aitem_1" {
		t.Fatalf("dispatch result=%+v received=%+v err=%v", result, received, err)
	}

	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = unwired.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || result.Error != "asset list item reports unavailable" {
		t.Fatalf("unwired asset list item tool = %+v, %v", result, err)
	}
}

// #494: a turn frozen while the production-list tools were still offered can
// name one after the Catalog stopped injecting them. The executor answers the
// Schema's tool-unavailable copy, names the called tool and runs nothing.
func TestAssetListToolsRefuseWithSchemaCopyWithoutExecution(t *testing.T) {
	called := ""
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		AssetListUnavailable: "工具 {tool} 本回合不可用，没有执行。",
		ProposeAssetList: func(context.Context, chat.ToolCommand) (chat.ToolResult, error) {
			called = ToolProposeAssetList
			return chat.ToolResult{AgentContent: "{}"}, nil
		},
		SubmitAssetListItem: func(context.Context, chat.ToolCommand) (chat.ToolResult, error) {
			called = ToolSubmitAssetListItem
			return chat.ToolResult{AgentContent: "{}"}, nil
		},
		ReadAssetList: func(context.Context, chat.ToolCommand) (chat.ToolResult, error) {
			called = ToolGetAssetList
			return chat.ToolResult{AgentContent: "{}"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ToolProposeAssetList, ToolSubmitAssetListItem, ToolGetAssetList} {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
			Call: agents.ToolCall{ID: "call-" + name, Name: name, Arguments: json.RawMessage(`{}`)},
		}, nil)
		if err != nil || !result.NoSubmission ||
			result.Error != "工具 "+name+" 本回合不可用，没有执行。" {
			t.Fatalf("%s = %+v, %v", name, result, err)
		}
	}
	if called != "" {
		t.Fatalf("a refused asset list tool still ran %s", called)
	}
}
