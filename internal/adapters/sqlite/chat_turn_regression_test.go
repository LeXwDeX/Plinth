package sqlite_test

import (
	"context"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"path/filepath"
	"testing"
	"time"
)

func TestOverlappingTurnsCannotOverwriteReplay(t *testing.T) {
	r, e := sqlite.Open(filepath.Join(t.TempDir(), "probe.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	_, e = r.CreateSession(ctx, chat.Session{ID: "same-session", OwnerID: "owner", Title: "Probe", CreatedAt: now, UpdatedAt: now})
	if e != nil {
		t.Fatal(e)
	}
	start := func(id string) {
		_, _, e := r.StartTurn(ctx, "owner", chat.Message{ID: "user-" + id, SessionID: "same-session", Role: "user", Content: id, CreatedAt: now}, chat.Message{ID: "assistant-" + id, SessionID: "same-session", Role: "assistant", Content: "pending", CreatedAt: now}, chat.Event{SessionID: "same-session", Data: map[string]any{"type": "message_created", "messageId": "assistant-" + id}, CreatedAt: now}, chat.Stream{SessionID: "same-session", Status: "active"}, chat.Turn{AssistantMessageID: "assistant-" + id, SessionID: "same-session", OwnerID: "owner", State: "running", CreatedAt: now, UpdatedAt: now})
		if e != nil {
			t.Fatal(e)
		}
	}
	start("one")
	_, e = r.AppendEvent(ctx, chat.Event{SessionID: "same-session", Data: map[string]any{"type": "text", "content": "first response"}, CreatedAt: now})
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = r.StartTurn(ctx, "owner", chat.Message{ID: "user-two", SessionID: "same-session", CreatedAt: now}, chat.Message{ID: "assistant-two", SessionID: "same-session", CreatedAt: now}, chat.Event{SessionID: "same-session", CreatedAt: now}, chat.Stream{SessionID: "same-session", Status: "active"}, chat.Turn{AssistantMessageID: "assistant-two", SessionID: "same-session", State: "running", CreatedAt: now, UpdatedAt: now})
	if e == nil {
		t.Fatal("overlapping turn accepted")
	}
	events, found, e := r.ListEvents(ctx, "owner", "same-session", 0)
	if e != nil || !found || len(events) != 2 {
		t.Fatalf("replay changed: %d %v %v", len(events), found, e)
	}
}

func TestConcurrentChatTurnsAndLateCompletionAreIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	_, err = a.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	start := func(r *sqlite.Repository, id string) error {
		_, _, err := r.StartTurn(ctx, "owner", chat.Message{ID: "u-" + id, SessionID: "session", CreatedAt: now}, chat.Message{ID: id, SessionID: "session", CreatedAt: now}, chat.Event{SessionID: "session", CreatedAt: now}, chat.Stream{SessionID: "session", Status: "active"}, chat.Turn{AssistantMessageID: id, SessionID: "session", OwnerID: "owner", State: "running", CreatedAt: now, UpdatedAt: now})
		return err
	}
	ready := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-ready; results <- start(a, "one") }()
	go func() { <-ready; results <- start(b, "two") }()
	close(ready)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, chat.ErrTurnActive) {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success %d conflicts %d", success, conflict)
	}
	turns, err := a.ListTurns(ctx, []string{"running"})
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns: %+v %v", turns, err)
	}
	first := turns[0].AssistantMessageID
	if err := a.FinishStream(ctx, "session", first, "done", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := start(b, "next"); err != nil {
		t.Fatal(err)
	}
	if err := a.FinishStream(ctx, "session", first, "done", now.Add(time.Minute)); !errors.Is(err, chat.ErrStreamNotFound) {
		t.Fatalf("stale completion: %v", err)
	}
	stream, events, _, _, err := b.ReadStream(ctx, "owner", "session", 0, now)
	if err != nil || stream.Status != "active" || len(events) != 1 {
		t.Fatalf("new stream damaged: %+v %d %v", stream, len(events), err)
	}
}
