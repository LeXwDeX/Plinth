package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestChatSessionStyleLockPersistsInOrderAndStaysOwnerScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "style-lock.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", Title: "a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if lock, found, err := repository.StyleLock(ctx, "owner-a", "session-a"); err != nil || !found || lock.Enabled || lock.Text != "" || len(lock.References) != 0 {
		t.Fatalf("default style lock = %+v found=%v err=%v", lock, found, err)
	}
	if found, err := repository.SetStyleLock(ctx, "owner-a", "session-a", chat.StyleLock{Enabled: true, Text: "ink", References: []string{"asset-2", "asset-1"}}); err != nil || !found {
		t.Fatalf("set style lock = %v %v", found, err)
	}
	if lock, found, err := repository.StyleLock(ctx, "owner-a", "session-a"); err != nil || !found || !lock.Enabled || lock.Text != "ink" || len(lock.References) != 2 || lock.References[0] != "asset-2" || lock.References[1] != "asset-1" {
		t.Fatalf("stored style lock = %+v found=%v err=%v", lock, found, err)
	}
	if _, found, _ := repository.StyleLock(ctx, "owner-b", "session-a"); found {
		t.Fatal("foreign owner read the style lock")
	}
	if found, _ := repository.SetStyleLock(ctx, "owner-b", "session-a", chat.StyleLock{Enabled: true}); found {
		t.Fatal("foreign owner wrote the style lock")
	}
	if session, found, err := repository.Session(ctx, "owner-a", "session-a"); err != nil || !found || !session.StyleLock.Enabled || session.StyleLock.References[0] != "asset-2" {
		t.Fatalf("session projection = %+v found=%v err=%v", session.StyleLock, found, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if lock, found, err := reopened.StyleLock(ctx, "owner-a", "session-a"); err != nil || !found || lock.References[0] != "asset-2" {
		t.Fatalf("reopened style lock = %+v found=%v err=%v", lock, found, err)
	}
}
