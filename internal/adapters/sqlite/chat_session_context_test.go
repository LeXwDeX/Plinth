package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// A database that was migrated to 0064 (before #322) and is opened by this
// binary reaches the current head; its sessions read with no level, no usage
// and no compaction.
func TestMigration0065UpgradesExistingSessionsWithoutContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('legacy','owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Roll the ledger back to 0064 and drop what 0065 (and the later 0066, 0067)
	// added, then reopen.
	for _, statement := range []string{
		`DELETE FROM go_schema_migrations WHERE version >= 65`,
		`DROP TABLE llm_settings_versions`,
		`DROP TABLE llm_connection_checks`,
		`ALTER TABLE go_chat_sessions DROP COLUMN chat_model`,
		`DROP TABLE chat_context_compactions`,
		`ALTER TABLE go_chat_sessions DROP COLUMN reasoning_effort`,
		`ALTER TABLE go_chat_sessions DROP COLUMN context_prompt_tokens`,
		`ALTER TABLE go_chat_sessions DROP COLUMN context_completion_tokens`,
		`ALTER TABLE go_chat_sessions DROP COLUMN context_model`,
		`ALTER TABLE go_chat_sessions DROP COLUMN context_updated_at`,
		`ALTER TABLE agent_generation_operations DROP COLUMN reference_count`,
		`ALTER TABLE agent_generation_operations DROP COLUMN reference_total`,
		`DROP INDEX IF EXISTS generation_attempts_delivery_completion`,
		`ALTER TABLE generation_attempts DROP COLUMN delivery_completion_json`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var head int
	if err := reopened.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head < 65 {
		t.Fatalf("migration head = %d err=%v", head, err)
	}
	state, found, err := reopened.SessionContext(context.Background(), "owner-a", "legacy")
	if err != nil || !found || state.ReasoningEffort != "" || state.Usage != nil || state.Latest != nil {
		t.Fatalf("legacy context = %+v found=%v err=%v", state, found, err)
	}
	if session, found, err := reopened.Session(context.Background(), "owner-a", "legacy"); err != nil || !found || session.Title != "old" {
		t.Fatalf("legacy session = %+v found=%v err=%v", session, found, err)
	}
}

func TestSessionContextIsOwnerScopedAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "context.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})

	if found, err := r.SetSessionReasoningEffort(ctx, "owner-a", "s1", "high"); err != nil || !found {
		t.Fatalf("set level: found=%v err=%v", found, err)
	}
	if found, err := r.SetSessionReasoningEffort(ctx, "owner-b", "s1", "max"); err != nil || found {
		t.Fatalf("foreign level write: found=%v err=%v", found, err)
	}
	if _, found, err := r.SessionContext(ctx, "owner-b", "s1"); err != nil || found {
		t.Fatalf("foreign read: found=%v err=%v", found, err)
	}
	at := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	if err := r.RecordContextUsage(ctx, "owner-a", "s1", &chat.ContextUsage{PromptTokens: 356000, CompletionTokens: 120, Model: "deepseek", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	// A foreign owner cannot write the usage of another owner's session.
	if err := r.RecordContextUsage(ctx, "owner-b", "s1", &chat.ContextUsage{PromptTokens: 1}); err != nil {
		t.Fatal(err)
	}
	session, _, _ := r.Session(ctx, "owner-a", "s1")
	if !session.UpdatedAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("usage write changed updated_at: %v", session.UpdatedAt)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	state, found, err := r.SessionContext(ctx, "owner-a", "s1")
	if err != nil || !found || state.ReasoningEffort != "high" || state.Usage == nil ||
		state.Usage.PromptTokens != 356000 || state.Usage.CompletionTokens != 120 || state.Usage.Model != "deepseek" ||
		!state.Usage.UpdatedAt.Equal(at) {
		t.Fatalf("context after reopen = %+v / %+v found=%v err=%v", state, state.Usage, found, err)
	}
	// nil records "the upstream reported none".
	if err := r.RecordContextUsage(ctx, "owner-a", "s1", nil); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := r.SessionContext(ctx, "owner-a", "s1"); state.Usage != nil || state.ReasoningEffort != "high" {
		t.Fatalf("unreported usage = %+v", state)
	}
	if other, _, _ := r.SessionContext(ctx, "owner-b", "s2"); other.Usage != nil || other.ReasoningEffort != "" {
		t.Fatalf("other owner's session changed: %+v", other)
	}
}

func TestCompactionsReplaceTheSummaryOnlyFromTheExpectedOne(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})
	if err := r.RecordContextUsage(ctx, "owner-a", "s1", &chat.ContextUsage{PromptTokens: 900000}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	first, err := r.SaveCompaction(ctx, chat.Compaction{OwnerID: "owner-a", SessionID: "s1", Trigger: chat.CompactionManual,
		Summary: "summary one @img1", ThroughMessageID: "m4", ThroughCreatedAt: base.Add(-time.Hour), Rounds: 2, CreatedAt: base}, "")
	if err != nil || first.ID == "" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	state, _, _ := r.SessionContext(ctx, "owner-a", "s1")
	if state.Usage != nil || state.Latest == nil || state.Latest.ID != first.ID || state.Latest.Summary != "summary one @img1" ||
		state.Latest.ThroughMessageID != "m4" || !state.Latest.ThroughCreatedAt.Equal(base.Add(-time.Hour)) || state.Latest.Rounds != 2 {
		t.Fatalf("after first compaction = %+v / %+v", state, state.Latest)
	}
	// A compaction computed from an older state loses.
	if _, err := r.SaveCompaction(ctx, chat.Compaction{OwnerID: "owner-a", SessionID: "s1", Trigger: chat.CompactionAuto,
		Summary: "stale", ThroughMessageID: "m6", Rounds: 3, CreatedAt: base.Add(time.Minute)}, ""); !errors.Is(err, chat.ErrCompactionConflict) {
		t.Fatalf("stale compaction = %v", err)
	}
	second, err := r.SaveCompaction(ctx, chat.Compaction{OwnerID: "owner-a", SessionID: "s1", Trigger: chat.CompactionAuto,
		Summary: "summary two", ThroughMessageID: "m8", Rounds: 4, CreatedAt: base.Add(2 * time.Minute)}, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveCompaction(ctx, chat.Compaction{OwnerID: "owner-b", SessionID: "s1", Trigger: chat.CompactionManual,
		Summary: "foreign", ThroughMessageID: "m1", CreatedAt: base}, second.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign compaction = %v", err)
	}
	listed, err := r.ListCompactions(ctx, "owner-a", "s1", 10)
	if err != nil || len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != second.ID || listed[1].Trigger != chat.CompactionAuto {
		t.Fatalf("listed = %+v err=%v", listed, err)
	}
	if limited, _ := r.ListCompactions(ctx, "owner-a", "s1", 1); len(limited) != 1 || limited[0].ID != second.ID {
		t.Fatalf("limited = %+v", limited)
	}
	if foreign, err := r.ListCompactions(ctx, "owner-b", "s1", 10); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign list = %+v err=%v", foreign, err)
	}
	// Deleting the session removes its compactions.
	if deleted, err := r.DeleteSession(ctx, "owner-a", "s1"); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	var left int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM chat_context_compactions WHERE session_id = 's1'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("compactions left after delete = %d err=%v", left, err)
	}
}
