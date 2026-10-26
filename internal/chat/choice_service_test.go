package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #321: a turn can answer an open choice card of its own session, once, with
// one of the card's options; the card stays answerable when the turn fails.
func TestATurnAnswersItsSessionsChoiceCardOnce(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-b", OwnerName: "B", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repo.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	service := newChatService(t, repo)
	service.SetChoices(repo, 20)
	card, err := repo.CreateChoice(ctx, chat.Choice{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-0",
		CallID: "call-0", ToolName: "process_model", Capability: "mesh-segmentation", StepIndex: -1,
		Options: []chat.ChoiceOption{
			{ID: "first", ProviderName: "P", ModelName: "One", Arguments: json.RawMessage(`{"offering_id":"first"}`)},
			{ID: "second", ProviderName: "P", ModelName: "Two", Arguments: json.RawMessage(`{"offering_id":"second"}`)},
		}, DefaultOptionID: "first", AllowDefault: true, DefaultArguments: json.RawMessage(`{"use_default_offering":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	turn := func(owner, session string, answer chat.ChoiceAnswer) (chat.Message, error) {
		return service.StartTurn(ctx, chat.StartTurn{OwnerID: owner, SessionID: session, Content: "选择：P · Two", ChoiceAnswer: &answer})
	}
	if _, err := turn("owner-b", "session-b", chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "second"}); !errors.Is(err, chat.ErrChoiceNotFound) {
		t.Fatalf("another owner's session answered the card: %v", err)
	}
	if _, err := turn("owner-a", "session-a", chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "not-on-card"}); !errors.Is(err, chat.ErrChoiceRejected) {
		t.Fatalf("an option not on the card = %v", err)
	}
	if _, err := turn("owner-a", "session-a", chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "second", UseDefault: true}); !errors.Is(err, chat.ErrChoiceRejected) {
		t.Fatalf("an option with the default = %v", err)
	}
	if _, err := turn("owner-a", "session-a", chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "second"}); err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(listed) != 1 || listed[0].State != chat.ChoiceAnswered || listed[0].AnswerOptionID != "second" ||
		listed[0].AnswerSource != chat.ChoiceAnsweredOnCard || listed[0].AnswerMessageID == "" {
		t.Fatalf("answered card = %+v %v", listed, err)
	}
	if _, err := turn("owner-a", "session-a", chat.ChoiceAnswer{ChoiceID: card.ID, UseDefault: true}); !errors.Is(err, chat.ErrChoiceClosed) {
		t.Fatalf("a second answer = %v", err)
	}
	if _, err := service.ListChoices(ctx, "owner-b", "session-a"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("listing another owner's session = %v", err)
	}
}

func TestChoiceContinuationsCarryExactCalls(t *testing.T) {
	card := chat.Choice{ID: "choice_1", ToolName: "process_model", Capability: "mesh-segmentation", State: chat.ChoicePending,
		Options: []chat.ChoiceOption{
			{ID: "first", ProviderName: "P", ModelName: "One", Arguments: json.RawMessage(`{"offering_id":"first"}`)},
			{ID: "second", ProviderName: "P", ModelName: "Two", Arguments: json.RawMessage(`{"offering_id":"second"}`)},
		}, DefaultOptionID: "first", AllowDefault: true, DefaultArguments: json.RawMessage(`{"use_default_offering":true}`)}
	pending, ok := card.PendingContinuation()
	var open struct {
		Tool    string `json:"tool"`
		Options []struct {
			ID        string         `json:"id"`
			Arguments map[string]any `json:"arguments"`
		} `json:"options"`
		Default struct {
			Arguments map[string]any `json:"arguments"`
		} `json:"default"`
	}
	if !ok || json.Unmarshal([]byte(pending), &open) != nil || open.Tool != "process_model" || len(open.Options) != 2 ||
		open.Options[1].Arguments["offering_id"] != "second" || open.Default.Arguments["use_default_offering"] != true {
		t.Fatalf("pending continuation = %s", pending)
	}
	if _, ok := card.AnsweredContinuation(); ok {
		t.Fatal("an open card has no answered continuation")
	}
	card.State, card.AnswerOptionID, card.AnswerDefault = chat.ChoiceAnswered, "first", true
	answered, ok := card.AnsweredContinuation()
	var chosen struct {
		Chosen struct {
			Arguments map[string]any `json:"arguments"`
		} `json:"chosen"`
	}
	if !ok || json.Unmarshal([]byte(answered), &chosen) != nil || chosen.Chosen.Arguments["use_default_offering"] != true {
		t.Fatalf("answered default continuation = %s", answered)
	}
	if _, ok := card.PendingContinuation(); ok {
		t.Fatal("an answered card is not pending")
	}
}
