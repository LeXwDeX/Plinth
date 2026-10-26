package chat_test

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"testing"
	"time"
)

type blockedReadRepository struct {
	chat.Repository
	blockedSession string
	entered        chan struct{}
	release        chan struct{}
}

func (r *blockedReadRepository) ReadStream(ctx context.Context, owner, session string, from int64, now time.Time) (chat.Stream, []chat.Event, bool, bool, error) {
	if session == r.blockedSession {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return chat.Stream{}, nil, false, false, ctx.Err()
		}
	}
	return r.Repository.ReadStream(ctx, owner, session, from, now)
}
func TestSlowStreamReadDoesNotFreezeUnrelatedSession(t *testing.T) {
	r := &blockedReadRepository{Repository: newMemoryChatRepository(), entered: make(chan struct{}), release: make(chan struct{})}
	s := newChatService(t, r)
	ctx := context.Background()
	a, _ := s.CreateSession(ctx, "owner", "owner", "A")
	b, _ := s.CreateSession(ctx, "owner", "owner", "B")
	if _, err := s.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: a.ID, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	existing, err := s.Subscribe(ctx, "owner", a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.blockedSession = a.ID
	subscribed := make(chan struct{})
	go func() {
		defer close(subscribed)
		sub, err := s.Subscribe(ctx, "owner", a.ID, 0)
		if err == nil {
			sub.Close()
		}
	}()
	<-r.entered
	closed := make(chan struct{})
	go func() { existing.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(200 * time.Millisecond):
		close(r.release)
		<-subscribed
		t.Fatal("subscriber Close waits for a blocked database read")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Subscribe(canceled, "owner", a.ID, 0); err != context.Canceled {
		close(r.release)
		<-subscribed
		t.Fatalf("same-session wait ignored cancellation: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AppendEvent(ctx, chat.Event{SessionID: b.ID}); done <- err }()
	select {
	case err := <-done:
		close(r.release)
		<-subscribed
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		close(r.release)
		<-subscribed
		<-done
		t.Fatal("blocked stream read for A freezes publishing to unrelated session B")
	}
}
