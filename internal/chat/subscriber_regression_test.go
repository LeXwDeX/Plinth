package chat_test

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"testing"
	"time"
)

func TestSlowSubscriberDoesNotBlockOtherSession(t *testing.T) {
	r := newMemoryChatRepository()
	service := newChatService(t, r)
	ctx := context.Background()
	a, _ := service.CreateSession(ctx, "owner", "owner", "A")
	b, _ := service.CreateSession(ctx, "owner", "owner", "B")
	_, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: a.ID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := service.Subscribe(ctx, "owner", a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := service.AppendEvent(ctx, chat.Event{SessionID: a.ID}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { _, err := service.AppendEvent(ctx, chat.Event{SessionID: a.ID}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		<-sub.Events
		<-done
		sub.Close()
		t.Fatal("slow subscriber blocks publishing")
	}
	if _, err := service.AppendEvent(ctx, chat.Event{SessionID: b.ID}); err != nil {
		t.Fatal(err)
	}
	sub.Close()
	sub.Close()
	replay, err := service.ListEvents(ctx, "owner", a.ID, 0)
	if err != nil || len(replay) != 10 {
		t.Fatalf("durable replay: %d, %v", len(replay), err)
	}
}
