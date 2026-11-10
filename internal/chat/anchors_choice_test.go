package chat_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func visualAnchorChoiceKind() catalog.ChatChoiceKindSchema {
	return catalog.ChatChoiceKindSchema{
		ID: "visual_anchor", OptionSource: "agent_proposal",
		ToolName: "set_visual_anchor", Argument: "action",
		Question: "要把这张图设为视觉锚吗？", Message: "确认后才会写入。",
		AcceptLabel: "设为视觉锚", DeclineLabel: "不用了",
	}
}

// #250: an agent_proposal card offers the Agent's exact arguments under
// accept and a decline that writes nothing. Opening the card itself writes no
// anchor.
func TestAskChoiceBuildsAnAgentProposalCardWithoutWriting(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{visualAnchorChoiceKind()}
	})
	service.SetChoices(repository, 20)
	proposal := json.RawMessage(`{"action":"create","name":"hero","kind":"identity","references":["asset-a"]}`)
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-anchor", Name: "ask_choice"},
	}
	result, err := service.AskChoice(ctx, command, visualAnchorChoiceKind(), chat.AskChoiceArguments{
		Kind: "visual_anchor", Proposal: proposal, Recommended: "accept",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("a confirmation card must not submit anything: %+v", result)
	}
	var opened struct {
		ChoiceRequired bool   `json:"choice_required"`
		ChoiceID       string `json:"choice_id"`
		Kind           string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil ||
		!opened.ChoiceRequired || opened.ChoiceID == "" || opened.Kind != "visual_anchor" {
		t.Fatalf("card = %+v, %v (%s)", opened, err, result.AgentContent)
	}
	cards, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(cards) != 1 || len(cards[0].Options) != 2 {
		t.Fatalf("cards = %+v, %v", cards, err)
	}
	accept, decline := cards[0].Options[0], cards[0].Options[1]
	if accept.ID != "accept" || accept.Label != "设为视觉锚" || !accept.Recommended ||
		string(accept.Arguments) != string(proposal) {
		t.Fatalf("accept option = %+v", accept)
	}
	if decline.ID != "decline" || decline.Label != "不用了" || string(decline.Arguments) != `{"action":"decline"}` {
		t.Fatalf("decline option = %+v", decline)
	}
	anchors, err := service.VisualAnchors(ctx, "owner-a", "session-a")
	if err != nil || len(anchors) != 0 {
		t.Fatalf("opening a card wrote anchors: %+v, %v", anchors, err)
	}

	// Without the proposal arguments there is no card to answer.
	if _, err := service.AskChoice(ctx, command, visualAnchorChoiceKind(), chat.AskChoiceArguments{Kind: "visual_anchor"}); err != nil {
		t.Fatal(err)
	}
	cards, _ = service.ListChoices(ctx, "owner-a", "session-a")
	if len(cards) != 1 {
		t.Fatalf("an empty proposal created a card: %+v", cards)
	}
}
