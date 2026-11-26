package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: a new turn's events continue the session's event numbering. A client
// that reconnects with the ended turn's last event ID (its stream cursor or
// Last-Event-ID) gets the new turn's events from the first one; with the
// numbering started at 0 again it skipped as many of them.
func TestNewTurnEventsContinueTheSessionNumbering(t *testing.T) {
	r, err := sqlitetest.Open(filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := r.CreateSession(ctx, chat.Session{ID: "s", OwnerID: "owner", Title: "Cursor", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	start := func(id string) chat.Event {
		_, created, err := r.StartTurn(ctx, "owner",
			chat.Message{ID: "user-" + id, SessionID: "s", Role: "user", Content: id, CreatedAt: now},
			chat.Message{ID: "assistant-" + id, SessionID: "s", Role: "assistant", Content: "pending", CreatedAt: now},
			chat.Event{SessionID: "s", Data: map[string]any{"type": "message_created", "messageId": "assistant-" + id}, CreatedAt: now},
			chat.Stream{SessionID: "s", Status: "active"},
			chat.Turn{AssistantMessageID: "assistant-" + id, SessionID: "s", OwnerID: "owner", State: "running", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	start("one")
	var last chat.Event
	for _, kind := range []string{"text", "text", "done"} {
		if last, err = r.AppendEvent(ctx, chat.Event{SessionID: "s", Data: map[string]any{"type": kind}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.FinishStream(ctx, "s", "assistant-one", "done", now); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishTurn(ctx, "assistant-one", "done", "", now); err != nil {
		t.Fatal(err)
	}
	created := start("two")
	if created.ID <= last.ID {
		t.Fatalf("the new turn's first event is %d, at or below the ended turn's last %d", created.ID, last.ID)
	}
	if _, err := r.AppendEvent(ctx, chat.Event{SessionID: "s", Data: map[string]any{"type": "text"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	stale, _, err := r.ListEvents(ctx, "owner", "s", last.ID+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 2 || stale[0].Data["messageId"] != "assistant-two" {
		t.Fatalf("events after the stale cursor = %#v", stale)
	}
	fresh, _, err := r.ListEvents(ctx, "owner", "s", 0)
	if err != nil || len(fresh) != 2 {
		t.Fatalf("events from 0 = %#v, %v", fresh, err)
	}
}
