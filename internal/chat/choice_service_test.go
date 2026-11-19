package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #321: a turn can answer an open choice card of its own session, once, with
// one of the card's options; the card stays answerable when the turn fails.
func TestATurnAnswersItsSessionsChoiceCardOnce(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
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

func nextStepCardOptions() []chat.ChoiceOption {
	return []chat.ChoiceOption{
		{ID: "clear_and_draw", Label: "清空功能选择，先出三视图图片", Description: "回到自由模式", Arguments: json.RawMessage(`{}`),
			Action: &chat.ChoiceAction{Type: "clear_operation"}},
		{ID: "switch_retexture", Label: "改为换贴图", Arguments: json.RawMessage(`{}`),
			Action: &chat.ChoiceAction{Type: "switch_operation", OperationID: "retexture"}},
		{ID: "keep", Label: "保持当前功能", Arguments: json.RawMessage(`{}`)},
	}
}

// #374: a card that names no target tool answers with an option only (it has
// no default), once; the answer keeps the option's action for the Agent.
func TestATurnAnswersANextStepCardOnceWithoutADefault(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := newChatService(t, repo)
	service.SetChoices(repo, 20)
	card, err := repo.CreateChoice(ctx, chat.Choice{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-0",
		CallID: "call-0", Kind: "next_step", Capability: "next_step", StepIndex: -1, Options: nextStepCardOptions()})
	if err != nil {
		t.Fatal(err)
	}
	turn := func(answer chat.ChoiceAnswer) error {
		_, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: "session-a", Content: "选择", ChoiceAnswer: &answer})
		return err
	}
	if err := turn(chat.ChoiceAnswer{ChoiceID: card.ID, UseDefault: true}); !errors.Is(err, chat.ErrChoiceRejected) {
		t.Fatalf("a default on a card without one = %v", err)
	}
	if err := turn(chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "not-on-card"}); !errors.Is(err, chat.ErrChoiceRejected) {
		t.Fatalf("an option the card does not offer = %v", err)
	}
	if err := turn(chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "switch_retexture"}); err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(listed) != 1 || listed[0].State != chat.ChoiceAnswered || listed[0].AnswerOptionID != "switch_retexture" ||
		listed[0].AnswerSource != chat.ChoiceAnsweredOnCard || listed[0].AnswerMessageID == "" {
		t.Fatalf("answered card = %+v %v", listed, err)
	}
	if err := turn(chat.ChoiceAnswer{ChoiceID: card.ID, OptionID: "keep"}); !errors.Is(err, chat.ErrChoiceClosed) {
		t.Fatalf("a second answer = %v", err)
	}
}

// #374: the continuation of a card with no target tool names the chosen
// option, its description and its action, never a tool to call; an open card
// lists every option for a typed pick.
func TestDirectionContinuationsCarryTheChosenOptionAndItsAction(t *testing.T) {
	card := chat.Choice{ID: "choice_n", Kind: "next_step", Capability: "next_step", State: chat.ChoicePending, Options: nextStepCardOptions()}
	pending, ok := card.PendingDirection()
	var open struct {
		Kind    string `json:"kind"`
		Tool    *string
		Options []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Action *struct {
				Type        string `json:"type"`
				OperationID string `json:"operation_id"`
			} `json:"action"`
		} `json:"options"`
	}
	if !ok || json.Unmarshal([]byte(pending), &open) != nil || open.Kind != "next_step" || open.Tool != nil ||
		len(open.Options) != 3 || open.Options[1].Action == nil || open.Options[1].Action.OperationID != "retexture" ||
		open.Options[2].Action != nil || open.Options[0].Title != "清空功能选择，先出三视图图片" {
		t.Fatalf("pending direction = %s", pending)
	}
	if _, ok := card.AnsweredDirection(); ok {
		t.Fatal("an open card has no answered continuation")
	}
	card.State, card.AnswerOptionID = chat.ChoiceAnswered, "clear_and_draw"
	answered, ok := card.AnsweredDirection()
	var chosen struct {
		Chosen struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Action      struct {
				Type string `json:"type"`
			} `json:"action"`
		} `json:"chosen"`
		Options []any `json:"options"`
	}
	if !ok || json.Unmarshal([]byte(answered), &chosen) != nil || chosen.Chosen.ID != "clear_and_draw" ||
		chosen.Chosen.Description != "回到自由模式" || chosen.Chosen.Action.Type != "clear_operation" || len(chosen.Options) != 0 {
		t.Fatalf("answered direction = %s", answered)
	}
	if _, ok := card.PendingDirection(); ok {
		t.Fatal("an answered card is not pending")
	}
	card.AnswerOptionID = "gone"
	if _, ok := card.AnsweredDirection(); ok {
		t.Fatal("an answer that names no option has a continuation")
	}
}
