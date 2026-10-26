package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestMigration0049DefaultsExistingSessionsToAuto(t *testing.T) {
	r := openApprovalRepo(t)
	// A row inserted the way the 0048 code did (no agent_scope column).
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('legacy','owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	session, found, err := r.Session(context.Background(), "owner-a", "legacy")
	if err != nil || !found || session.AgentScope != "auto" {
		t.Fatalf("legacy session = %+v found=%v err=%v", session, found, err)
	}
	var head int
	if err := r.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head < 49 {
		t.Fatalf("migration head = %d err=%v", head, err)
	}
}

func TestSetSessionAgentScopeIsOwnerScopedAndKeepsOrdering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scope.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})

	session, found, err := r.SetSessionAgentScope(ctx, "owner-a", "s1", "3d")
	if err != nil || !found || session.AgentScope != "3d" {
		t.Fatalf("set 3d = %+v found=%v err=%v", session, found, err)
	}
	if !session.UpdatedAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("scope switch changed updated_at: %v", session.UpdatedAt)
	}
	if _, found, err := r.SetSessionAgentScope(ctx, "owner-b", "s1", "2d"); err != nil || found {
		t.Fatalf("foreign set: found=%v err=%v", found, err)
	}
	if scope, found, _ := r.SessionAgentScope(ctx, "owner-b", "s1"); found || scope != "" {
		t.Fatalf("a foreign owner read the scope: %q %v", scope, found)
	}
	if other, _, _ := r.Session(ctx, "owner-b", "s2"); other.AgentScope != "auto" {
		t.Fatalf("other owner's session changed: %+v", other)
	}
	if _, _, err := r.SetSessionAgentScope(ctx, "owner-a", "s1", ""); !errors.Is(err, chat.ErrInvalidAgentScope) {
		t.Fatalf("empty scope: %v", err)
	}
	// The scope survives a reopen, in the single read, the page and the list.
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if scope, found, err := reopened.SessionAgentScope(ctx, "owner-a", "s1"); err != nil || !found || scope != "3d" {
		t.Fatalf("scope after reopen = %q found=%v err=%v", scope, found, err)
	}
	page, err := reopened.ListSessionsPage(ctx, "owner-a", chat.SessionListQuery{Limit: 10})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].AgentScope != "3d" {
		t.Fatalf("page = %+v err=%v", page, err)
	}
	listed, err := reopened.ListSessions(ctx, "owner-a")
	if err != nil || len(listed) != 1 || listed[0].AgentScope != "3d" {
		t.Fatalf("list = %+v err=%v", listed, err)
	}
}

func TestCreateSessionStoresTheScopeAndEmptyBecomesAuto(t *testing.T) {
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s-2d", "owner-a", chat.Session{AgentScope: "2d"})
	createApprovalSession(t, r, "s-empty", "owner-a", chat.Session{})
	for id, want := range map[string]string{"s-2d": "2d", "s-empty": "auto"} {
		got, found, err := r.Session(context.Background(), "owner-a", id)
		if err != nil || !found || got.AgentScope != want {
			t.Fatalf("%s = %+v found=%v err=%v; want %s", id, got, found, err, want)
		}
	}
}
