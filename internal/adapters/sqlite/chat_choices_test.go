package sqlite_test

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

func choiceRepository(t *testing.T) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "choices.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-a", OwnerName: "A", Title: "b", CreatedAt: now, UpdatedAt: now},
		{ID: "session-x", OwnerID: "owner-b", OwnerName: "B", Title: "x", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repo.CreateSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
	return repo, path
}

func sampleChoice(call string) chat.Choice {
	return chat.Choice{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1", CallID: call,
		ToolName: "process_model", Capability: "mesh-segmentation", StepIndex: -1,
		Options: []chat.ChoiceOption{
			{ID: "hyper3d/bang/local-model", ProviderName: "Hyper3D", ModelName: "Bang（本地模型）", Arguments: json.RawMessage(`{"offering_id":"hyper3d/bang/local-model"}`)},
			{ID: "hyper3d/bang/rodin-task", ProviderName: "Hyper3D", ModelName: "Bang（Rodin 来源）", Arguments: json.RawMessage(`{"offering_id":"hyper3d/bang/rodin-task"}`)},
		},
		DefaultOptionID: "hyper3d/bang/local-model", AllowDefault: true, DefaultArguments: json.RawMessage(`{"use_default_offering":true}`),
		CreatedAt: time.Now().UTC()}
}

func TestChoiceCardsPersistPerOwnerAndSessionAndAnswerOnce(t *testing.T) {
	ctx := context.Background()
	repo, path := choiceRepository(t)
	created, err := repo.CreateChoice(ctx, sampleChoice("call-1"))
	if err != nil || created.ID == "" || created.State != chat.ChoicePending {
		t.Fatalf("create = %+v %v", created, err)
	}
	// A replayed tool call returns the stored card instead of a second one.
	again, err := repo.CreateChoice(ctx, sampleChoice("call-1"))
	if err != nil || again.ID != created.ID {
		t.Fatalf("replay = %+v %v", again, err)
	}
	// Another owner, or another session of the same owner, cannot see or answer it.
	for _, scope := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "session-b"}, {"owner-b", "session-x"}} {
		if _, err := repo.GetChoice(ctx, scope[0], scope[1], created.ID); !errors.Is(err, chat.ErrChoiceNotFound) {
			t.Fatalf("%v read = %v", scope, err)
		}
		if listed, err := repo.ListChoices(ctx, scope[0], scope[1], 10); err != nil || len(listed) != 0 {
			t.Fatalf("%v list = %v %v", scope, listed, err)
		}
		if _, err := repo.AnswerChoice(ctx, scope[0], scope[1], created.ID, chat.ChoiceRecord{OptionID: "hyper3d/bang/rodin-task"}); !errors.Is(err, chat.ErrChoiceNotFound) {
			t.Fatalf("%v answer = %v", scope, err)
		}
	}
	if _, err := repo.CreateChoice(ctx, chat.Choice{OwnerID: "owner-b", SessionID: "session-a", AssistantMessageID: "t", CallID: "c",
		Options: sampleChoice("x").Options}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("a card in another owner's session = %v", err)
	}
	if _, err := repo.AnswerChoice(ctx, "owner-a", "session-a", created.ID, chat.ChoiceRecord{OptionID: "not-offered"}); !errors.Is(err, chat.ErrChoiceRejected) {
		t.Fatalf("an option not on the card = %v", err)
	}
	answered, err := repo.AnswerChoice(ctx, "owner-a", "session-a", created.ID, chat.ChoiceRecord{OptionID: "hyper3d/bang/rodin-task",
		Source: chat.ChoiceAnsweredOnCard, MessageID: "user-2"})
	if err != nil || answered.State != chat.ChoiceAnswered || answered.AnswerOptionID != "hyper3d/bang/rodin-task" {
		t.Fatalf("answer = %+v %v", answered, err)
	}
	if _, err := repo.AnswerChoice(ctx, "owner-a", "session-a", created.ID, chat.ChoiceRecord{OptionID: "hyper3d/bang/local-model"}); !errors.Is(err, chat.ErrChoiceClosed) {
		t.Fatalf("a second answer = %v", err)
	}
	// Reopening is bound to the message that answered it.
	if err := repo.ReopenChoice(ctx, "owner-a", "session-a", created.ID, "other-message"); err != nil {
		t.Fatal(err)
	}
	if stored, _ := repo.GetChoice(ctx, "owner-a", "session-a", created.ID); stored.State != chat.ChoiceAnswered {
		t.Fatalf("an unrelated reopen changed the card: %+v", stored)
	}
	// The answer survives a restart of the store.
	_ = repo.Close()
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.GetChoice(ctx, "owner-a", "session-a", created.ID)
	if err != nil || stored.State != chat.ChoiceAnswered || stored.AnswerMessageID != "user-2" || len(stored.Options) != 2 ||
		string(stored.DefaultArguments) != `{"use_default_offering":true}` {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	if err := reopened.ReopenChoice(ctx, "owner-a", "session-a", created.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if stored, _ := reopened.GetChoice(ctx, "owner-a", "session-a", created.ID); stored.State != chat.ChoicePending || stored.AnswerOptionID != "" {
		t.Fatalf("reopen = %+v", stored)
	}
}

func TestANewChoiceCardSupersedesTheOpenOne(t *testing.T) {
	ctx := context.Background()
	repo, _ := choiceRepository(t)
	first, _ := repo.CreateChoice(ctx, sampleChoice("call-1"))
	second, err := repo.CreateChoice(ctx, sampleChoice("call-2"))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListChoices(ctx, "owner-a", "session-a", 10)
	if err != nil || len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("list = %+v %v", listed, err)
	}
	if listed[0].State != chat.ChoicePending || listed[1].State != chat.ChoiceSuperseded {
		t.Fatalf("states = %s %s", listed[0].State, listed[1].State)
	}
	if _, err := repo.AnswerChoice(ctx, "owner-a", "session-a", first.ID, chat.ChoiceRecord{OptionID: "hyper3d/bang/rodin-task"}); !errors.Is(err, chat.ErrChoiceClosed) {
		t.Fatalf("answering a superseded card = %v", err)
	}
	if deleted, err := repo.DeleteSession(ctx, "owner-a", "session-a"); err != nil || !deleted {
		t.Fatalf("delete session = %v %v", deleted, err)
	}
	if listed, _ := repo.ListChoices(ctx, "owner-a", "session-a", 10); len(listed) != 0 {
		t.Fatalf("cards outlived their session: %+v", listed)
	}
}

func TestChoiceCardsListNewestFirstAcrossFractionalSeconds(t *testing.T) {
	ctx := context.Background()
	repo, _ := choiceRepository(t)
	// RFC3339Nano drops trailing zeros, so "…:00Z" sorts after "…:00.1Z" as
	// text; the stored instant must keep time order.
	whole := time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)
	for index, created := range []time.Time{whole, whole.Add(100 * time.Millisecond)} {
		choice := sampleChoice("call-" + string(rune('a'+index)))
		choice.CreatedAt = created
		if _, err := repo.CreateChoice(ctx, choice); err != nil {
			t.Fatal(err)
		}
	}
	choices, err := repo.ListChoices(ctx, "owner-a", "session-a", 20)
	if err != nil || len(choices) != 2 {
		t.Fatalf("list = %+v %v", choices, err)
	}
	if choices[0].CallID != "call-b" || !choices[0].CreatedAt.Equal(whole.Add(100*time.Millisecond)) {
		t.Fatalf("newest first = %s (%s), want call-b", choices[0].CallID, choices[0].CreatedAt)
	}
}
