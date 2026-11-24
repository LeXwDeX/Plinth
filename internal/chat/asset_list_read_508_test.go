package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #508 asset-list-07: get_asset_list without list_id resolves the list in a
// fixed order, and the tool description states that order. A session that
// proposed a list reads its own newest proposal (here still a draft) even
// though the project has a confirmed list; a session that proposed nothing
// reads the project's confirmed list. The Agent checks the returned status.
func TestReadAssetListWithoutListIDFollowsTheDescribedOrder508(t *testing.T) {
	repository := newAssetListRepo()
	repository.sessions[scopedKey("owner-a", "session-b")] = "project-a"
	schema := defaultAssetListSchema(t)
	service := newAssetListProposalService(t, repository, schema, nil)
	repository.lists = append(repository.lists, assetlist.List{
		ID: "alist_confirmed", OwnerID: "owner-a", ProjectID: "project-a",
		SourceSessionID: "session-other", Title: "已确认", Status: assetlist.StatusConfirmed,
	})
	if _, err := service.ProposeAssetList(context.Background(), assetListCommand(`{
		"title":"草稿","items":[{"kind":"prop","name":"Sword","description":"blade"}]}`)); err != nil {
		t.Fatal(err)
	}
	read := func(sessionID string) map[string]any {
		t.Helper()
		result, err := service.ReadAssetList(context.Background(), chat.ToolCommand{
			OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: "turn-r",
			Call:    agents.ToolCall{ID: "call-r", Name: "get_asset_list"},
			Options: chat.TurnOptions{ProjectID: "project-a"},
		})
		if err != nil || result.Error != "" {
			t.Fatalf("read from %s = %+v err=%v", sessionID, result, err)
		}
		var content map[string]any
		if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
			t.Fatal(err)
		}
		return content
	}
	if own := read("session-a"); own["title"] != "草稿" || own["status"] != assetlist.StatusDraft {
		t.Fatalf("proposing session read = %v; want its own draft", own)
	}
	if other := read("session-b"); other["list_id"] != "alist_confirmed" ||
		other["status"] != assetlist.StatusConfirmed {
		t.Fatalf("other session read = %v; want the project's confirmed list", other)
	}

	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range product.Application().Chat.AgentTools {
		if tool.Name != "get_asset_list" {
			continue
		}
		if !strings.Contains(tool.Description, "the newest list this session proposed") ||
			!strings.Contains(tool.Description, "the project's confirmed list") {
			t.Fatalf("get_asset_list description does not state the read order: %q", tool.Description)
		}
		return
	}
	t.Fatal("the Catalog declares no get_asset_list tool")
}
