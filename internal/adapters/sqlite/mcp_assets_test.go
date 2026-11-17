package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func assetSession(id, owner string) chat.Session {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	return chat.Session{ID: id, OwnerID: owner, OwnerName: "Test", Title: "MCP · " + id, TitleSource: chat.TitleSourceUser, CreatedAt: now, UpdatedAt: now}
}

func TestMCPAssetCreateStoresTheSessionAndItsMappingTogether(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "assets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	ctx := context.Background()
	if _, found, err := r.FindMCPAsset(ctx, "owner-a", "barrel"); err != nil || found {
		t.Fatalf("found = %v err=%v", found, err)
	}
	asset, created, err := r.CreateMCPAsset(ctx, assetSession("session-1", "owner-a"), "barrel", "Barrel")
	if err != nil || !created || asset.SessionID != "session-1" || asset.Name != "Barrel" || asset.NameKey != "barrel" {
		t.Fatalf("created = %+v %v err=%v", asset, created, err)
	}
	// The session is an ordinary one in the person's list.
	if owned, err := r.OwnsSession(ctx, "owner-a", "session-1"); err != nil || !owned {
		t.Fatalf("owned = %v err=%v", owned, err)
	}
	found, ok, err := r.FindMCPAsset(ctx, "owner-a", "barrel")
	if err != nil || !ok || found.SessionID != "session-1" || !found.CreatedAt.Equal(asset.CreatedAt) {
		t.Fatalf("find = %+v %v err=%v", found, ok, err)
	}
	// The same name again stores nothing and answers with the first asset.
	again, created, err := r.CreateMCPAsset(ctx, assetSession("session-2", "owner-a"), "barrel", "Barrel")
	if err != nil || created || again.SessionID != "session-1" {
		t.Fatalf("again = %+v %v err=%v", again, created, err)
	}
	if owned, _ := r.OwnsSession(ctx, "owner-a", "session-2"); owned {
		t.Fatal("the losing session was stored")
	}
	// Another person's same name is their own asset; the default asset is the empty name.
	if other, created, err := r.CreateMCPAsset(ctx, assetSession("session-3", "owner-b"), "barrel", "Barrel"); err != nil || !created || other.SessionID != "session-3" {
		t.Fatalf("other owner = %+v %v err=%v", other, created, err)
	}
	if def, created, err := r.CreateMCPAsset(ctx, assetSession("session-4", "owner-a"), "", ""); err != nil || !created || def.Name != "" {
		t.Fatalf("default = %+v %v err=%v", def, created, err)
	}
	// Which sessions are assets: the owner's own only.
	marked, err := r.MCPAssetsOfSessions(ctx, "owner-a", []string{"session-1", "session-3", "session-4", "session-x", ""})
	if err != nil || len(marked) != 2 || marked["session-1"].Name != "Barrel" || marked["session-4"].NameKey != "" {
		t.Fatalf("marked = %+v err=%v", marked, err)
	}
	if none, err := r.MCPAssetsOfSessions(ctx, "owner-a", nil); err != nil || len(none) != 0 {
		t.Fatalf("none = %+v err=%v", none, err)
	}
	// Deleting the session takes the mapping with it: the name starts afresh.
	if deleted, err := r.DeleteSession(ctx, "owner-a", "session-1"); err != nil || !deleted {
		t.Fatalf("delete = %v err=%v", deleted, err)
	}
	if _, found, _ := r.FindMCPAsset(ctx, "owner-a", "barrel"); found {
		t.Fatal("the mapping outlived its session")
	}
	if fresh, created, err := r.CreateMCPAsset(ctx, assetSession("session-5", "owner-a"), "barrel", "Barrel"); err != nil || !created || fresh.SessionID != "session-5" {
		t.Fatalf("fresh = %+v %v err=%v", fresh, created, err)
	}
	if _, _, err := r.CreateMCPAsset(ctx, chat.Session{}, "x", "x"); err == nil {
		t.Fatal("a session without an ID or an owner was stored")
	}
}

func TestMCPAssetCreatedAtOnceGivesOneSession(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	ctx := context.Background()
	var wait sync.WaitGroup
	winners := make(chan string, 12)
	creations := make(chan bool, 12)
	for index := range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			asset, created, err := r.CreateMCPAsset(ctx, assetSession("session-"+string(rune('a'+index)), "owner-a"), "barrel", "Barrel")
			if err != nil {
				t.Error(err)
				return
			}
			winners <- asset.SessionID
			creations <- created
		}()
	}
	wait.Wait()
	close(winners)
	close(creations)
	first, made := "", 0
	for id := range winners {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("callers got different sessions: %s and %s", first, id)
		}
	}
	for created := range creations {
		if created {
			made++
		}
	}
	var sessions int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_chat_sessions WHERE owner_id = 'owner-a'`).Scan(&sessions); err != nil || sessions != 1 || made != 1 {
		t.Fatalf("sessions = %d, creations = %d err=%v; one name is one session", sessions, made, err)
	}
}

// Migration 0098 only adds the mapping table: a database that has not got it
// gets it empty, and every session stays as it is.
func TestMigration0098AddsTheAssetMappingWithoutTouchingSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assets-migration.db")
	r, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.CreateSession(ctx, assetSession("plain", "owner-a")); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`DELETE FROM go_schema_migrations WHERE version = 98`, `DROP TABLE mcp_assets`} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var rows, sessions int
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM mcp_assets`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("mapping rows = %d err=%v", rows, err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM go_chat_sessions`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions = %d err=%v", sessions, err)
	}
	var head int
	if err := upgraded.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() || head < 98 {
		t.Fatalf("head = %d err=%v", head, err)
	}
	if marked, err := upgraded.MCPAssetsOfSessions(ctx, "owner-a", []string{"plain"}); err != nil || len(marked) != 0 {
		t.Fatalf("an old session is no asset: %+v err=%v", marked, err)
	}
}
