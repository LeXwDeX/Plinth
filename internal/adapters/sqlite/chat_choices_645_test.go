package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #645: the end of a person's turn supersedes the pending card an earlier reply
// left, created no later than the turn started. The turn's own card, a card
// created after the turn started and an answered card stay as they are.
func TestSupersedeChoicesBeforeEndsOnlyAnEarlierRepliesOpenCard_645(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		created time.Time
		keep    string
		answer  bool
		want    chat.ChoiceState
	}{
		{name: "earlier reply", created: start.Add(-time.Minute), keep: "turn-2", want: chat.ChoiceSuperseded},
		{name: "same instant", created: start, keep: "turn-2", want: chat.ChoiceSuperseded},
		{name: "the turn's own reply", created: start.Add(-time.Minute), keep: "turn-1", want: chat.ChoicePending},
		{name: "created after the turn started", created: start.Add(time.Second), keep: "turn-2", want: chat.ChoicePending},
		{name: "answered by the Agent", created: start.Add(-time.Minute), keep: "turn-2", answer: true, want: chat.ChoiceAnswered},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, _ := choiceRepository(t)
			sample := sampleChoice("call-1")
			sample.CreatedAt = test.created
			card, err := repo.CreateChoice(ctx, sample)
			if err != nil {
				t.Fatal(err)
			}
			if test.answer {
				if _, err := repo.AnswerChoice(ctx, "owner-a", "session-a", card.ID, chat.ChoiceRecord{
					OptionID: "hyper3d/bang/rodin-task", Source: chat.ChoiceAnsweredByAgent, AnsweredAt: start}); err != nil {
					t.Fatal(err)
				}
			}
			// Another session's open card is never touched.
			insertReply(t, repo, "owner-a", "session-b", "turn-b", start)
			elsewhere := sampleChoice("call-b")
			elsewhere.SessionID, elsewhere.AssistantMessageID, elsewhere.CreatedAt = "session-b", "turn-b", start.Add(-time.Hour)
			other, err := repo.CreateChoice(ctx, elsewhere)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SupersedeChoicesBefore(ctx, "owner-a", "session-a", test.keep, start); err != nil {
				t.Fatal(err)
			}
			if got, err := repo.GetChoice(ctx, "owner-a", "session-a", card.ID); err != nil || got.State != test.want {
				t.Fatalf("card = %s %v, want %s", got.State, err, test.want)
			}
			if got, _ := repo.GetChoice(ctx, "owner-a", "session-b", other.ID); got.State != chat.ChoicePending {
				t.Fatalf("another session's card = %s", got.State)
			}
		})
	}
}

// #645: deleting a reply deletes the cards it asked, and a card whose reply no
// longer exists (left by a deletion before this fix) is not read.
func TestDeletingAReplyDropsItsChoiceCards_645(t *testing.T) {
	ctx := context.Background()
	repo, _ := choiceRepository(t)
	created, err := repo.CreateChoice(ctx, sampleChoice("call-1"))
	if err != nil {
		t.Fatal(err)
	}
	orphan := sampleChoice("call-orphan")
	orphan.AssistantMessageID = "never-stored"
	orphaned, err := repo.CreateChoice(ctx, orphan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetChoice(ctx, "owner-a", "session-a", orphaned.ID); !errors.Is(err, chat.ErrChoiceNotFound) {
		t.Fatalf("a card without its reply = %v", err)
	}
	if listed, err := repo.ListChoices(ctx, "owner-a", "session-a", 10); err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list before the deletion = %+v %v", listed, err)
	}
	if found, deleted, err := repo.DeleteMessage(ctx, "owner-a", "session-a", "turn-1"); err != nil || !found || !deleted {
		t.Fatalf("delete = %v %v %v", found, deleted, err)
	}
	if listed, err := repo.ListChoices(ctx, "owner-a", "session-a", 10); err != nil || len(listed) != 0 {
		t.Fatalf("cards outlived their reply: %+v %v", listed, err)
	}
	// The card row went with the reply: storing the same reply ID again does
	// not bring it back.
	insertReply(t, repo, "owner-a", "session-a", "turn-1", time.Now().UTC())
	if _, err := repo.GetChoice(ctx, "owner-a", "session-a", created.ID); !errors.Is(err, chat.ErrChoiceNotFound) {
		t.Fatalf("the deleted reply's card = %v", err)
	}
}
