package chat_test

import (
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #615: one Agent reply opened two choice cards asking the same thing; the
// second create silently superseded the first and the person saw both. A reply
// opens at most one card: while its card is pending, a second ask_choice call
// of the same reply is refused and stores nothing. A replay of the same call
// still returns its stored card, and a later reply may open a new card.
func TestAskChoiceOpensAtMostOneCardPerReply_615(t *testing.T) {
	service, ctx := openOneStepService(t)
	arguments := chat.AskChoiceArguments{Kind: "next_step", Options: []chat.ChoiceOptionInput{
		{ID: "keep", Label: "保留 GPT Image"}, {ID: "switch", Label: "改用 Nano Banana 2"},
	}}
	first, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), arguments)
	if err != nil || first.Error != "" {
		t.Fatalf("first card = %+v %v", first, err)
	}

	second := oneStepCommand("turn-1")
	second.Call = agents.ToolCall{ID: "call-turn-1-again", Name: "ask_choice"}
	refused, err := service.AskChoice(ctx, second, oneStepNextStepKind(), arguments)
	if !errors.Is(err, chat.ErrChoiceOpenInReply) {
		t.Fatalf("second card of the same reply = %+v %v; want ErrChoiceOpenInReply", refused, err)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || choices[0].State != chat.ChoicePending || choices[0].CallID != "call-turn-1" {
		t.Fatalf("cards after the refused second = %+v %v; want only the first, still pending", choices, err)
	}

	// The same call replayed (a resumed reply) returns its stored card.
	replayed, err := service.AskChoice(ctx, oneStepCommand("turn-1"), oneStepNextStepKind(), arguments)
	if err != nil || replayed.Error != "" || replayed.AgentContent != first.AgentContent {
		t.Fatalf("replayed call = %+v %v", replayed, err)
	}

	// A later reply may open its own card; it supersedes the earlier one.
	later, err := service.AskChoice(ctx, oneStepCommand("turn-2"), oneStepNextStepKind(), arguments)
	if err != nil || later.Error != "" {
		t.Fatalf("card of a later reply = %+v %v", later, err)
	}
}
