package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// sessionDependentTables lists every session-scoped table that has no
// foreign key to go_chat_sessions and therefore must be cleared explicitly
// when a session is deleted (#334). The query returns the rows that belong to
// the given owner and session.
var sessionDependentTables = map[string]string{
	"chat_choices":             `SELECT COUNT(*) FROM chat_choices WHERE owner_id = ? AND session_id = ?`,
	"chat_context_compactions": `SELECT COUNT(*) FROM chat_context_compactions WHERE owner_id = ? AND session_id = ?`,
	"go_agent_state":           `SELECT COUNT(*) FROM go_agent_state WHERE owner_id = ? AND scope = ?`,
}

func seedSessionWithDependents(t *testing.T, r *Repository, ownerID, sessionID string) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO go_chat_sessions (id, owner_id, owner_name, title, created_at, updated_at)
			VALUES (?, ?, 'owner', 'title', '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z')`,
			[]any{sessionID, ownerID}},
		{`INSERT INTO chat_choices (id, owner_id, session_id, assistant_message_id, call_id, tool_name,
			capability, step_index, options_json, default_option_id, allow_default, default_arguments,
			state, created_at)
			VALUES (?, ?, ?, 'assistant-1', 'call-1', 'tool', 'capability', 0, '[]', '', 0, '{}',
			'pending', '2026-10-22T00:00:00Z')`,
			[]any{"choice-" + sessionID, ownerID, sessionID}},
		{`INSERT INTO chat_context_compactions (id, owner_id, session_id, trigger, summary,
			through_message_id, created_at)
			VALUES (?, ?, ?, 'manual', 'summary', 'message-1', '2026-10-22T00:00:00Z')`,
			[]any{"compaction-" + sessionID, ownerID, sessionID}},
		{`INSERT INTO go_agent_state (owner_id, scope, state_key, value_json, updated_at)
			VALUES (?, ?, 'consent_pending', 'true', '2026-10-22T00:00:00Z')`,
			[]any{ownerID, sessionID}},
	}
	for _, statement := range statements {
		if _, err := r.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed %s: %v", sessionID, err)
		}
	}
}

func seedOwningCanvasProject(t *testing.T, r *Repository, ownerID, projectID, sessionID string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO go_canvas_projects
		(id, owner_id, name, session_id, owns_session, viewport, bg_style, created_at, updated_at)
		VALUES (?, ?, 'project', ?, 1, '{}', 'dots', '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z')`,
		projectID, ownerID, sessionID,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func assertSessionDependents(t *testing.T, r *Repository, ownerID, sessionID string, want int) {
	t.Helper()
	for table, query := range sessionDependentTables {
		var count int
		if err := r.db.QueryRow(query, ownerID, sessionID).Scan(&count); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if count != want {
			t.Errorf("%s rows for %s/%s = %d, want %d", table, ownerID, sessionID, count, want)
		}
	}
}

// Both deletion paths — deleting a chat session and deleting the canvas
// project that owns one — must leave no orphaned session data, and must not
// touch another session's rows.
func TestSessionDeletionPathsClearEverySessionDependentTable(t *testing.T) {
	paths := map[string]func(context.Context, *Repository) (bool, error){
		"chat session": func(ctx context.Context, r *Repository) (bool, error) {
			return r.DeleteSession(ctx, "owner-a", "session-a")
		},
		"canvas project": func(ctx context.Context, r *Repository) (bool, error) {
			return r.DeleteProjectAndSession(ctx, "owner-a", "project-a")
		},
	}
	for name, deleteFn := range paths {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r, err := Open(filepath.Join(t.TempDir(), "dependents.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			seedSessionWithDependents(t, r, "owner-a", "session-a")
			seedOwningCanvasProject(t, r, "owner-a", "project-a", "session-a")
			seedSessionWithDependents(t, r, "owner-a", "session-b")
			assertSessionDependents(t, r, "owner-a", "session-a", 1)

			deleted, err := deleteFn(ctx, r)
			if err != nil || !deleted {
				t.Fatalf("delete = %v err=%v", deleted, err)
			}
			assertSessionDependents(t, r, "owner-a", "session-a", 0)
			assertSessionDependents(t, r, "owner-a", "session-b", 1)
			if owns, err := r.OwnsSession(ctx, "owner-a", "session-a"); err != nil || owns {
				t.Fatalf("session after delete owns=%v err=%v", owns, err)
			}
		})
	}
}
