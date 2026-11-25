package httpapi

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #548: an assistant message lists the Skills it used, read from the
// summaries its calls stored, so a reload shows the same line. A message
// that used none leaves the member out.
func TestChatMessageWireListsTheUsedSkills(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	server := &Server{application: application}
	field := application.Chat.API.ResponseFields.UsedSkills
	if field == "" {
		t.Fatal("the shipped Schema must name the used Skills member")
	}
	wire := server.chatMessageWire("", chat.Message{ID: "m1", Role: "assistant", ToolCalls: []chat.ToolCall{
		{ID: "call-1", Name: "use_skill", Summary: chat.UsedSkillSummary(chat.UsedSkill{ID: "system-three-view", DisplayName: "三视图", Source: "agent", Applied: true})},
		{ID: "call-2", Name: "create_three_view"},
	}})
	used, ok := wire[field].([]chat.UsedSkill)
	if !ok || len(used) != 1 || used[0].ID != "system-three-view" || used[0].DisplayName != "三视图" || used[0].Source != "agent" {
		t.Fatalf("used skills = %#v", wire[field])
	}
	if _, present := server.chatMessageWire("", chat.Message{ID: "m2", Role: "assistant"})[field]; present {
		t.Fatal("a message without used Skills carries the member")
	}
}
