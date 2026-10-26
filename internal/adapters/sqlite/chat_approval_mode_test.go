package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func openApprovalRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func createApprovalSession(t *testing.T, r *Repository, id, owner string, session chat.Session) {
	t.Helper()
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	session.ID, session.OwnerID, session.OwnerName = id, owner, owner
	session.CreatedAt, session.UpdatedAt = now, now
	if _, err := r.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
}

func TestMigration0043DefaultsExistingSessionsToManual(t *testing.T) {
	r := openApprovalRepo(t)
	// A row inserted the way the 0042 code did (no approval_mode column).
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('legacy','owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	session, found, err := r.Session(context.Background(), "owner-a", "legacy")
	if err != nil || !found || session.ApprovalMode != "manual" {
		t.Fatalf("legacy session = %+v found=%v err=%v", session, found, err)
	}
	if _, err := r.db.Exec(`UPDATE go_chat_sessions SET approval_mode='bogus' WHERE id='legacy'`); err == nil {
		t.Fatal("database accepted an unknown approval mode")
	}
	var head int
	if err := r.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head < 43 {
		t.Fatalf("migration head = %d err=%v", head, err)
	}
}

func TestSetSessionApprovalModeIsOwnerScopedAndKeepsOrdering(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})

	session, found, err := r.SetSessionApprovalMode(ctx, "owner-a", "s1", "auto")
	if err != nil || !found || session.ApprovalMode != "auto" {
		t.Fatalf("set auto = %+v found=%v err=%v", session, found, err)
	}
	if !session.UpdatedAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("mode switch changed updated_at: %v", session.UpdatedAt)
	}
	// Another owner can neither change nor see the session.
	if _, found, err := r.SetSessionApprovalMode(ctx, "owner-b", "s1", "manual"); err != nil || found {
		t.Fatalf("foreign set: found=%v err=%v", found, err)
	}
	if other, _, _ := r.Session(ctx, "owner-b", "s2"); other.ApprovalMode != "manual" {
		t.Fatalf("other owner's session changed: %+v", other)
	}
	if mine, _, _ := r.Session(ctx, "owner-a", "s1"); mine.ApprovalMode != "auto" {
		t.Fatalf("mode lost: %+v", mine)
	}
	if _, _, err := r.SetSessionApprovalMode(ctx, "owner-a", "s1", "yolo"); !errors.Is(err, chat.ErrInvalidApprovalMode) {
		t.Fatalf("invalid mode: %v", err)
	}
	if mine, _, _ := r.Session(ctx, "owner-a", "s1"); mine.ApprovalMode != "auto" {
		t.Fatalf("invalid mode wrote: %+v", mine)
	}
}

func TestCreateSessionStoresModeAndUnknownBecomesManual(t *testing.T) {
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s-auto", "owner-a", chat.Session{ApprovalMode: "auto"})
	createApprovalSession(t, r, "s-odd", "owner-a", chat.Session{ApprovalMode: "whatever"})
	createApprovalSession(t, r, "s-empty", "owner-a", chat.Session{})
	for id, want := range map[string]string{"s-auto": "auto", "s-odd": "manual", "s-empty": "manual"} {
		got, found, err := r.Session(context.Background(), "owner-a", id)
		if err != nil || !found || got.ApprovalMode != want {
			t.Fatalf("%s = %+v found=%v err=%v; want %s", id, got, found, err, want)
		}
	}
}

func TestSessionEstimatedCreditsSumsOwnTasksOnly(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})
	insert := func(id, owner, session, offering, input string) {
		t.Helper()
		_, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key,
 state, progress, materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, ?, 'text-to-image', ?, ?, ?, ?, 'QUEUED', 0, 0, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', ?)`,
			id, owner, offering, input, "hash-"+id, "key-"+id, session)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("t1", "owner-a", "s1", `{"id":"o","estimated_credits":4}`, `{}`)
	insert("t2", "owner-a", "s1", `{"id":"o","estimated_credits":3,"estimated_credits_unit_parameter":"count"}`, `{"count":2}`)
	insert("t3", "owner-b", "s2", `{"id":"o","estimated_credits":100}`, `{}`)

	mine, _, err := r.Session(ctx, "owner-a", "s1")
	if err != nil || mine.EstimatedCredits != 10 {
		t.Fatalf("owner-a credits = %d err=%v, want 10", mine.EstimatedCredits, err)
	}
	other, _, _ := r.Session(ctx, "owner-b", "s2")
	if other.EstimatedCredits != 100 {
		t.Fatalf("owner-b credits = %d", other.EstimatedCredits)
	}
	listed, err := r.ListSessions(ctx, "owner-a")
	if err != nil || len(listed) != 1 || listed[0].EstimatedCredits != 10 {
		t.Fatalf("list credits = %+v err=%v", listed, err)
	}
}
