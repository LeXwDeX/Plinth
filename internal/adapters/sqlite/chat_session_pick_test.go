package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestChatSessionPickPersistsAcrossReopenAndStaysScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pick.db")
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
	if pick, found, err := repository.SessionPick(ctx, "owner-a", "session-a"); err != nil || !found || pick.Kind != "" {
		t.Fatalf("a new session has no pick: %+v %v %v", pick, found, err)
	}
	picked := chat.Attachment{Kind: "image", Role: "generated", AssetID: "asset-1", TaskID: "task-1",
		// Only platform IDs are stored: the handle and picture are derived again on use.
		Handle: "@img4", URL: "/api/v1/tasks/task-1/assets/asset-1", ImageSlot: 2}
	if found, err := repository.SetSessionPick(ctx, "owner-a", "session-a", picked); err != nil || !found {
		t.Fatalf("set = %v %v", found, err)
	}
	// A foreign owner can neither read nor write, and it looks like a missing session.
	if _, found, _ := repository.SessionPick(ctx, "owner-b", "session-a"); found {
		t.Fatal("a foreign owner read the pick")
	}
	if found, _ := repository.SetSessionPick(ctx, "owner-b", "session-a", chat.Attachment{Kind: "image", AssetID: "x", TaskID: "y"}); found {
		t.Fatal("a foreign owner wrote the pick")
	}
	if found, _ := repository.SetSessionPick(ctx, "owner-a", "missing", picked); found {
		t.Fatal("a missing session must report not found")
	}
	if pick, _, _ := repository.SessionPick(ctx, "owner-a", "session-b"); pick.Kind != "" {
		t.Fatalf("pick leaked to another session: %+v", pick)
	}
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
	pick, found, _ := reopened.SessionPick(ctx, "owner-a", "session-a")
	want := chat.Attachment{Kind: "image", Role: "generated", AssetID: "asset-1", TaskID: "task-1"}
	if !found || pick != want {
		t.Fatalf("pick after reopen = %+v; want %+v", pick, want)
	}
	// A zero attachment clears the pick.
	if found, err := reopened.SetSessionPick(ctx, "owner-a", "session-a", chat.Attachment{}); err != nil || !found {
		t.Fatalf("clear = %v %v", found, err)
	}
	if pick, found, _ := reopened.SessionPick(ctx, "owner-a", "session-a"); !found || pick.Kind != "" {
		t.Fatalf("pick after clear = %+v", pick)
	}
	// Deleting the session removes its pick with it.
	if _, err := reopened.SetSessionPick(ctx, "owner-a", "session-a", picked); err != nil {
		t.Fatal(err)
	}
	if deleted, err := reopened.DeleteSession(ctx, "owner-a", "session-a"); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if _, found, _ := reopened.SessionPick(ctx, "owner-a", "session-a"); found {
		t.Fatal("the pick outlived its session")
	}
}
