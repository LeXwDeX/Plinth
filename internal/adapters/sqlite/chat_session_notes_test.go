package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestChatSessionNotesPersistAcrossReopenAndStayScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "notes.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-a", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if notes, found, err := repository.SessionNotes(ctx, "owner-a", "session-a"); err != nil || !found || notes != "" {
		t.Fatalf("a new session starts with empty notes: %q %v %v", notes, found, err)
	}
	if found, err := repository.SetSessionNotes(ctx, "owner-a", "session-a", "hero: 红围巾"); err != nil || !found {
		t.Fatalf("set = %v %v", found, err)
	}
	// A foreign owner can neither read nor write, and it looks like a missing session.
	if _, found, _ := repository.SessionNotes(ctx, "owner-b", "session-a"); found {
		t.Fatal("a foreign owner read the notes")
	}
	if found, _ := repository.SetSessionNotes(ctx, "owner-b", "session-a", "taken"); found {
		t.Fatal("a foreign owner wrote the notes")
	}
	if found, _ := repository.SetSessionNotes(ctx, "owner-a", "missing", "x"); found {
		t.Fatal("a missing session must report not found")
	}
	if notes, _, _ := repository.SessionNotes(ctx, "owner-a", "session-b"); notes != "" {
		t.Fatalf("notes leaked to another session: %q", notes)
	}
	// Editing the summary does not reorder the history list.
	session, _, err := repository.Session(ctx, "owner-a", "session-a")
	if err != nil || !session.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at changed to %v (%v)", session.UpdatedAt, err)
	}
	repository.Close()

	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if notes, found, _ := reopened.SessionNotes(ctx, "owner-a", "session-a"); !found || notes != "hero: 红围巾" {
		t.Fatalf("notes after reopen = %q", notes)
	}
	// Deleting the session removes its notes with it.
	if deleted, err := reopened.DeleteSession(ctx, "owner-a", "session-a"); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if _, found, _ := reopened.SessionNotes(ctx, "owner-a", "session-a"); found {
		t.Fatal("notes outlived their session")
	}
}
