package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The busy rule of a session follows the state of the turn that owns its
// stream, not the stream row alone (#384). The stream row is finished by a
// separate write after the turn ends; when that write is late or lost the row
// says "active" for a turn that is long over, and the self-hosted log showed
// people refused with "a task is still running" for minutes and once for 15
// hours after the turn had failed.
func TestStartTurnIsNotBusyOnceTheTurnOfTheStreamIsTerminal(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "busy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "t", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	busy := []string{"queued", "running", "waiting"}
	start := func(id string) error {
		_, _, err := repository.StartTurn(ctx, "owner-a",
			chat.Message{ID: "user-" + id, SessionID: "session-a", Role: "user", Content: "hi", CreatedAt: now},
			chat.Message{ID: id, SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
			chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
			chat.Stream{SessionID: "session-a", Status: "active", BusyTurnStates: busy},
			chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: id,
				State: "queued", CreatedAt: now, UpdatedAt: now})
		return err
	}
	setState := func(id, state string) {
		t.Helper()
		if err := repository.UpdateTurnState(ctx, id, state, "", now); err != nil {
			t.Fatal(err)
		}
	}

	if err := start("turn-1"); err != nil {
		t.Fatal(err)
	}
	for _, state := range busy {
		setState("turn-1", state)
		if err := start("turn-x"); !errors.Is(err, chat.ErrTurnActive) {
			t.Fatalf("a %s turn: StartTurn = %v, want ErrTurnActive", state, err)
		}
	}
	// The turn ends; the stream row is still "active" because its finishing
	// write has not landed.
	setState("turn-1", "done")
	if err := start("turn-2"); err != nil {
		t.Fatalf("StartTurn after the turn ended with its stream row still active: %v", err)
	}
	// The same for a turn that ended in error.
	setState("turn-2", "error")
	if err := start("turn-3"); err != nil {
		t.Fatalf("StartTurn after a failed turn with its stream row still active: %v", err)
	}
	// The newest turn is the one that counts.
	if err := start("turn-4"); !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("StartTurn over an active turn = %v, want ErrTurnActive", err)
	}
}

// The service decides which turn states keep a session busy; the same rule
// reaches the repository through StartTurn. A session is busy while its turn is
// queued, running or waiting for a task, and free the moment the turn finished
// (done or failed) whether or not the stream row was ever finished.
func TestServiceStartsATurnOnceTheTurnBeforeItIsTerminal(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "service-busy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service := endReasonService(t, repository, endReasonStrings)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	start := func() (chat.Message, error) {
		return service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	}
	first, err := start()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := start(); !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("StartTurn over a queued turn = %v, want ErrTurnActive", err)
	}
	if err := service.MarkTurnRunning(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := start(); !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("StartTurn over a running turn = %v, want ErrTurnActive", err)
	}
	// The turn finished; nothing finished its stream.
	if err := service.FinishTurn(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	second, err := start()
	if err != nil {
		t.Fatalf("StartTurn after a finished turn whose stream stayed active: %v", err)
	}
	if err := service.FinishTurn(ctx, second.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := start(); err != nil {
		t.Fatalf("StartTurn after a failed turn whose stream stayed active: %v", err)
	}
}
