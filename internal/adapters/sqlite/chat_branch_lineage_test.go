package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestMigration0080PreservesExistingSessions(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE go_schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	r := &Repository{db: db}
	all := migrations()
	branch := -1
	for index, item := range all {
		if item.version == 80 {
			branch = index
			break
		}
	}
	if branch < 0 {
		t.Fatal("missing migration 0080")
	}
	if err := r.runMigrations(ctx, all[:branch]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at) VALUES ('old','owner','owner','Original','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := r.runMigrations(ctx, all[branch:]); err != nil {
		t.Fatal(err)
	}
	session, found, err := r.Session(ctx, "owner", "old")
	if err != nil || !found || session.Title != "Original" || session.ParentSessionID != "" || session.ParentMessageID != "" || session.ParentTitle != "" || session.ParentDeleted {
		t.Fatalf("legacy session after migration: %+v found=%v err=%v", session, found, err)
	}
}

func TestBranchLineageSurvivesParentDeletion(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Original"})
	createApprovalSession(t, r, "child", "owner", chat.Session{Title: "Branch"})
	if _, err := r.db.ExecContext(ctx, `UPDATE go_chat_sessions SET parent_session_id='parent', parent_message_id='message', parent_title='Original' WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	if deleted, err := r.DeleteSession(ctx, "other", "parent"); err != nil || deleted {
		t.Fatalf("foreign delete: deleted=%v err=%v", deleted, err)
	}
	child, found, err := r.Session(ctx, "owner", "child")
	if err != nil || !found || child.ParentDeleted || child.ParentSessionID != "parent" || child.ParentMessageID != "message" || child.ParentTitle != "Original" {
		t.Fatalf("child before deletion: %+v found=%v err=%v", child, found, err)
	}
	if deleted, err := r.DeleteSession(ctx, "owner", "parent"); err != nil || !deleted {
		t.Fatalf("delete parent: deleted=%v err=%v", deleted, err)
	}
	child, found, err = r.Session(ctx, "owner", "child")
	if err != nil || !found || !child.ParentDeleted || child.ParentTitle != "Original" || child.ParentSessionID != "parent" || child.ParentMessageID != "message" {
		t.Fatalf("child after deletion: %+v found=%v err=%v", child, found, err)
	}
}
