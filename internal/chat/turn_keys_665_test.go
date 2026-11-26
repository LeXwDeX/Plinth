package chat_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: a keyed send that already started a turn answers that turn. The
// in-memory test repository keeps the same rules as SQLite, so every case
// runs against both.

type turnKeyClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *turnKeyClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *turnKeyClock) advance(by time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(by)
}

const turnKeyRetention = time.Hour

func turnKeyRepositories(t *testing.T) map[string]func() chat.Repository {
	return map[string]func() chat.Repository{
		"memory": func() chat.Repository { return newMemoryChatRepository() },
		"sqlite": func() chat.Repository {
			repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repository.Close() })
			return repository
		},
	}
}

func newTurnKeyService(t *testing.T, repository chat.Repository) (*chat.Service, *turnKeyClock, string) {
	t.Helper()
	clock := &turnKeyClock{now: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	sequence := 0
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Clock = clock.Now
		config.TurnKeyRetention = turnKeyRetention
		config.NewID = func(kind string) string {
			mu.Lock()
			defer mu.Unlock()
			sequence++
			return kind + "-" + time.Duration(sequence).String()
		}
	})
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "keyed")
	if err != nil {
		t.Fatal(err)
	}
	return service, clock, session.ID
}

func keyedTurn(session, key, fingerprint string) chat.StartTurn {
	return chat.StartTurn{OwnerID: "owner-a", SessionID: session, Content: "draw a cat",
		IdempotencyKey: key, RequestFingerprint: fingerprint}
}

// finishTurn ends the session's running turn, so the next send is not refused
// as a busy session and a repeat is told apart from a new turn.
func finishTurn(t *testing.T, service *chat.Service, session, assistant string) {
	t.Helper()
	if err := service.FinishStream(context.Background(), session, assistant, false); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTurn(context.Background(), assistant, false); err != nil {
		t.Fatal(err)
	}
}

func messageCount(t *testing.T, service *chat.Service, session string) int {
	t.Helper()
	messages, err := service.ListMessages(context.Background(), "owner-a", session)
	if err != nil {
		t.Fatal(err)
	}
	return len(messages)
}

func TestKeyedSendRepeatAnswersItsTurn665(t *testing.T) {
	for name, open := range turnKeyRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			service, _, session := newTurnKeyService(t, open())
			first, replayed, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
			if err != nil || replayed || first.ID == "" {
				t.Fatalf("first send = %+v replayed=%v err=%v", first, replayed, err)
			}
			finishTurn(t, service, session, first.ID)
			repeat, replayed, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
			if err != nil || !replayed || repeat.ID != first.ID || repeat.SessionID != session {
				t.Fatalf("repeat = %+v replayed=%v err=%v; want the first turn %q", repeat, replayed, err, first.ID)
			}
			if count := messageCount(t, service, session); count != 2 {
				t.Fatalf("messages after a repeat = %d, want 2", count)
			}
			if _, _, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-b")); !errors.Is(err, chat.ErrIdempotencyConflict) {
				t.Fatalf("same key, other request: err=%v, want ErrIdempotencyConflict", err)
			}
			// A new key is a new send.
			next, replayed, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-2", "digest-a"))
			if err != nil || replayed || next.ID == first.ID {
				t.Fatalf("new key = %+v replayed=%v err=%v", next, replayed, err)
			}
		})
	}
}

func TestKeyedSendExpiresAfterRetention665(t *testing.T) {
	for name, open := range turnKeyRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repository := open()
			service, clock, session := newTurnKeyService(t, repository)
			first, _, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
			if err != nil {
				t.Fatal(err)
			}
			finishTurn(t, service, session, first.ID)
			clock.advance(turnKeyRetention)
			if _, found, err := repository.TurnKey(ctx, "owner-a", session, "send-1", clock.Now()); err != nil || found {
				t.Fatalf("expired key found=%v err=%v", found, err)
			}
			again, replayed, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-b"))
			if err != nil || replayed || again.ID == first.ID {
				t.Fatalf("send after expiry = %+v replayed=%v err=%v; want a new turn", again, replayed, err)
			}
			// The expired record was replaced by the new turn's.
			record, found, err := repository.TurnKey(ctx, "owner-a", session, "send-1", clock.Now())
			if err != nil || !found || record.AssistantMessageID != again.ID || record.Fingerprint != "digest-b" ||
				!record.ExpiresAt.Equal(clock.Now().Add(turnKeyRetention)) {
				t.Fatalf("record after expiry = %+v found=%v err=%v", record, found, err)
			}
		})
	}
}

func TestKeyedSendIsScopedToOwnerAndSession665(t *testing.T) {
	for name, open := range turnKeyRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repository := open()
			service, clock, session := newTurnKeyService(t, repository)
			first, _, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
			if err != nil {
				t.Fatal(err)
			}
			other, err := service.CreateSession(ctx, "owner-a", "Owner", "other")
			if err != nil {
				t.Fatal(err)
			}
			second, replayed, err := service.StartKeyedTurn(ctx, keyedTurn(other.ID, "send-1", "digest-a"))
			if err != nil || replayed || second.ID == first.ID {
				t.Fatalf("same key in another session = %+v replayed=%v err=%v", second, replayed, err)
			}
			if _, found, _ := repository.TurnKey(ctx, "owner-b", session, "send-1", clock.Now()); found {
				t.Fatal("another owner read the owner's key")
			}
			foreign := keyedTurn(session, "send-1", "digest-a")
			foreign.OwnerID = "owner-b"
			if _, replayed, err := service.StartKeyedTurn(ctx, foreign); replayed || !errors.Is(err, chat.ErrSessionNotFound) {
				t.Fatalf("foreign repeat replayed=%v err=%v, want ErrSessionNotFound", replayed, err)
			}
		})
	}
}

func TestKeyedSendConcurrentCopiesStartOneTurn665(t *testing.T) {
	for name, open := range turnKeyRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			service, _, session := newTurnKeyService(t, open())
			const copies = 8
			ids := make([]string, copies)
			errs := make([]error, copies)
			var group sync.WaitGroup
			for index := range copies {
				group.Add(1)
				go func() {
					defer group.Done()
					assistant, _, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
					ids[index], errs[index] = assistant.ID, err
				}()
			}
			group.Wait()
			for index := range copies {
				if errs[index] != nil || ids[index] != ids[0] {
					t.Fatalf("copy %d = %q err=%v; want %q", index, ids[index], errs[index], ids[0])
				}
			}
			if count := messageCount(t, service, session); count != 2 {
				t.Fatalf("messages after %d copies = %d, want 2", copies, count)
			}
		})
	}
}

// A released key (its turn never ran) no longer answers; another turn's
// release does not remove it.
func TestReleaseTurnKeyRemovesOnlyItsTurn665(t *testing.T) {
	for name, open := range turnKeyRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repository := open()
			service, clock, session := newTurnKeyService(t, repository)
			first, _, err := service.StartKeyedTurn(ctx, keyedTurn(session, "send-1", "digest-a"))
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ReleaseTurnKey(ctx, "owner-a", session, "send-1", "message-other"); err != nil {
				t.Fatal(err)
			}
			if _, found, _ := repository.TurnKey(ctx, "owner-a", session, "send-1", clock.Now()); !found {
				t.Fatal("a release naming another turn removed the key")
			}
			if err := service.ReleaseTurnKey(ctx, "owner-a", session, "send-1", first.ID); err != nil {
				t.Fatal(err)
			}
			if _, found, _ := repository.TurnKey(ctx, "owner-a", session, "send-1", clock.Now()); found {
				t.Fatal("the released key still answers")
			}
		})
	}
}

// Without a configured retention a key is ignored: the service behaves as it
// did before keys existed.
func TestKeyedSendWithoutRetentionKeepsNoKey665(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := service.StartKeyedTurn(context.Background(), keyedTurn(session.ID, "send-1", "digest-a")); err != nil || replayed {
		t.Fatalf("replayed=%v err=%v", replayed, err)
	}
	if _, found, _ := repository.TurnKey(context.Background(), "owner-a", session.ID, "send-1", time.Now()); found {
		t.Fatal("a service without retention stored the key")
	}
}
