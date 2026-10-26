package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #279: find_history_images reads only the history and selection the server
// put on the command, counts the reply's earlier lookup calls (and no other
// tool's) against the Schema bound, words refusals with the Schema texts,
// and fails closed when the Schema declares no bound.
func TestFindHistoryImagesReadsTheServerHeldHistoryAndCountsEarlierLookups(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	schema.Context.HistoryImageLookupLimit = 1
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}, Context: schema.Context,
		HistoryLookup: chat.NewHistoryLookupPolicy(schema)})
	if err != nil {
		t.Fatal(err)
	}
	history := []chat.Message{
		{Role: schema.Roles.User, Content: "pose", ImageURLs: []string{"/api/v1/input-assets/a"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "a", ImageSlot: 1}}},
		{Role: schema.Roles.User, Content: "outfit", ImageURLs: []string{"/api/v1/input-assets/b"},
			Attachments: []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "b", ImageSlot: 1}}},
	}
	run := func(arguments string, earlier ...chat.ToolCall) chat.ToolResult {
		t.Helper()
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a", History: history, EarlierCalls: earlier,
			Call: agents.ToolCall{ID: "find", Name: ToolFindHistoryImages, Arguments: json.RawMessage(arguments)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	shown := run(`{"handles":["@img1"]}`)
	if len(shown.Pictures) != 1 || shown.Pictures[0].URL != "/api/v1/input-assets/a" || !strings.Contains(shown.AgentContent, `"@img1"`) {
		t.Fatalf("shown = %+v", shown)
	}
	if strings.Contains(shown.AgentContent, "input-assets") {
		t.Fatalf("the Agent must not read URLs: %s", shown.AgentContent)
	}
	// Another tool's call is not a lookup; an earlier lookup is.
	other := chat.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(`{"handles":["@img1"]}`)}
	if free := run(`{"handles":["@img2"]}`, other); len(free.Pictures) != 1 {
		t.Fatalf("another tool used the lookup bound: %+v", free)
	}
	over := run(`{"handles":["@img2"]}`, other, chat.ToolCall{Name: ToolFindHistoryImages, Arguments: json.RawMessage(`{"handles":["@img1"]}`)})
	if len(over.Pictures) != 0 || !strings.Contains(over.AgentContent, `"over_budget":["@img2"]`) {
		t.Fatalf("over the bound = %+v", over)
	}

	unknown := run(`{"handles":["@img9"]}`)
	if unknown.Error != strings.ReplaceAll(schema.Context.UnknownHandleTemplate, "{handle}", "@img9") || len(unknown.Pictures) != 0 {
		t.Fatalf("unknown handle = %+v", unknown)
	}
	if malformed := run(`[1]`); malformed.Error == "" {
		t.Fatalf("malformed arguments = %+v", malformed)
	}

	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := unwired.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", History: history,
		Call: agents.ToolCall{ID: "find", Name: ToolFindHistoryImages, Arguments: json.RawMessage(`{}`)}}, nil)
	if result.Error == "" || len(result.Pictures) != 0 {
		t.Fatalf("a lookup without a Schema bound must fail closed: %+v", result)
	}
}
