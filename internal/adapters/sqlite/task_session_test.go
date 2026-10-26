package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSessionHistorySearchAndCursorStayOwnerScoped(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, row := range []struct{ id, owner, title string }{
		{"a", "owner", "Landscape"}, {"b", "owner", "Landscape"},
		{"c", "owner", "Landscape"}, {"wildcard", "owner", "Land%scape"},
		{"other", "other-owner", "Landscape"},
	} {
		if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,'2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, row.id, row.owner, row.owner, row.title); err != nil {
			t.Fatal(err)
		}
	}
	query := chat.SessionListQuery{Search: "Landscape", Limit: 2}
	first, err := repository.ListSessionsPage(context.Background(), "owner", query)
	if err != nil || len(first.Sessions) != 2 || first.NextCursor == "" || first.Total != 3 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	query.Cursor = first.NextCursor
	second, err := repository.ListSessionsPage(context.Background(), "owner", query)
	if err != nil || len(second.Sessions) != 1 || second.NextCursor != "" || second.Total != 3 {
		t.Fatalf("second page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, session := range append(first.Sessions, second.Sessions...) {
		if session.OwnerID != "owner" || seen[session.ID] || session.ID == "wildcard" {
			t.Fatalf("unexpected session: %+v", session)
		}
		seen[session.ID] = true
	}
	query = chat.SessionListQuery{Search: "%", Limit: 10}
	literal, err := repository.ListSessionsPage(context.Background(), "owner", query)
	if err != nil || len(literal.Sessions) != 1 || literal.Sessions[0].ID != "wildcard" || literal.Total != 1 {
		t.Fatalf("literal search: %+v %v", literal, err)
	}
	query.Cursor = "malformed"
	if _, err := repository.ListSessionsPage(context.Background(), "owner", query); !errors.Is(err, chat.ErrInvalidSessionPage) {
		t.Fatalf("invalid cursor = %v", err)
	}
}

func TestSessionPageTotalSurvivesAnEmptyPageAfterTheCursor(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "session-total-empty-page.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for index, id := range []string{"s1", "s2", "s3"} {
		stamp := time.Date(2026, 9, 1, 0, 0, index+1, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, id, "owner", "owner", "Project", stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('foreign','other-owner','other-owner','Project','2026-09-01T00:00:04Z','2026-09-01T00:00:04Z')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A cursor older than every row returns no rows, but the owner still has three matches.
	past := encodeChatSessionCursor(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), "zzz")
	empty, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 2, Cursor: past})
	if err != nil || len(empty.Sessions) != 0 || empty.Total != 3 || empty.NextCursor != "" {
		t.Fatalf("cursor past the end = %d sessions, total %d, cursor %q: %v", len(empty.Sessions), empty.Total, empty.NextCursor, err)
	}

	// Deleting the row the next cursor would have read leaves an empty page, not a zero total.
	first, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 2})
	if err != nil || len(first.Sessions) != 2 || first.Total != 3 || first.NextCursor == "" {
		t.Fatalf("first page = %d sessions, total %d, cursor %q: %v", len(first.Sessions), first.Total, first.NextCursor, err)
	}
	if _, err := repository.db.Exec(`DELETE FROM go_chat_sessions WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	after, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(after.Sessions) != 0 || after.Total != 2 || after.NextCursor != "" {
		t.Fatalf("emptied last page = %d sessions, total %d, cursor %q: %v", len(after.Sessions), after.Total, after.NextCursor, err)
	}
}

func TestTaskSessionMigrationBackfillsOnlyUnambiguousOwnedBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; CREATE TABLE go_schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	legacy := &Repository{db: db}
	if err := legacy.runMigrations(context.Background(), migrations()[:22]); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, owner string }{{"a", "owner"}, {"b", "owner"}, {"other", "other-owner"}} {
		if _, err := db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,'same name','2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, row.id, row.owner, row.owner); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"linked", "ambiguous", "foreign", "orphan"} {
		if _, err := db.Exec(`INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,'owner','text-to-image','{}','{}',?,'SUCCEEDED',1,'2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ id, session, task string }{{"m1", "a", "linked"}, {"m2", "a", "ambiguous"}, {"m3", "b", "ambiguous"}, {"m4", "other", "foreign"}} {
		calls, _ := json.Marshal([]map[string]string{{"task_id": row.task}})
		if _, err := db.Exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,created_at) VALUES (?,?,'assistant','','[]',?,'2026-09-01T00:00:00Z')`, row.id, row.session, string(calls)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	for _, row := range []struct{ id, want string }{{"linked", "a"}, {"ambiguous", ""}, {"foreign", ""}, {"orphan", ""}} {
		task, err := current.Get(context.Background(), "owner", generation.TaskID(row.id))
		if err != nil || task.SessionID != row.want {
			t.Fatalf("task %s session = %q, want %q: %v", row.id, task.SessionID, row.want, err)
		}
	}
	page, err := current.List(context.Background(), generation.OwnTasks("owner"), generation.ListQuery{SessionID: "a", Limit: 10})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "linked" {
		t.Fatalf("scoped page = %+v: %v", page, err)
	}
}

func TestCreateTaskRequiresOwnedSessionAndKeepsIdempotencyInSession(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, row := range []struct{ id, owner string }{{"first", "owner"}, {"second", "owner"}, {"foreign", "other-owner"}} {
		if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,'task','2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, row.id, row.owner, row.owner); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	task := generation.Task{
		ID: "task-one", OwnerID: "owner", SessionID: "first",
		Capability: generation.Capability("text-to-image"),
		Offering:   generation.Offering{ID: "offering"},
		Input:      json.RawMessage(`{}`), RequestHash: "same-request", IdempotencyKey: "one-call",
		State: generation.TaskQueued, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	task = approveOwnedTaskForCreate(t, repository, task)
	event := generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}
	if _, replayed, err := repository.Create(context.Background(), task, event, 0); err != nil || replayed {
		t.Fatalf("create task: replayed=%v err=%v", replayed, err)
	}
	if _, replayed, err := repository.Create(context.Background(), task, event, 0); err != nil || !replayed {
		t.Fatalf("same-session replay: replayed=%v err=%v", replayed, err)
	}
	for _, sessionID := range []string{"second", "foreign", "missing"} {
		candidate := task
		candidate.ID = generation.TaskID("task-" + sessionID)
		candidate.SessionID = sessionID
		if sessionID != "second" {
			candidate.IdempotencyKey = "new-" + sessionID
		}
		_, _, err := repository.Create(context.Background(), candidate, event, 0)
		want := generation.RepositorySessionNotFound
		if sessionID == "second" {
			want = generation.RepositoryIdempotencyConflict
		}
		if !errors.Is(err, want) {
			t.Fatalf("session %s: got %v, want %v", sessionID, err, want)
		}
	}
}

func TestSessionCursorStaysContinuousWhenOlderSessionsGainActivity(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	for i, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		stamp := time.Date(2026, 9, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, id, "owner", "owner", "t "+id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Limit: 2})
	if err != nil || len(first.Sessions) != 2 || first.Sessions[0].ID != "s5" || first.Sessions[1].ID != "s4" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	// A newer session appears and an unloaded one is renamed (bumping its
	// activity) between the two page reads: nothing seen is repeated.
	if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('s6','owner','owner','t s6','2026-09-02T00:00:00Z','2026-09-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`UPDATE go_chat_sessions SET updated_at = '2026-09-03T00:00:00Z' WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	// s1 jumped ahead of the cursor, so it is not served again and nothing
	// after the cursor is skipped.
	second, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Sessions) != 2 || second.Sessions[0].ID != "s3" || second.Sessions[1].ID != "s2" || second.NextCursor != "" {
		t.Fatalf("second page: %+v %v", second, err)
	}
}

func TestRenameKeepsOrderingActivity(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "rename-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "a", OwnerID: "owner", OwnerName: "o", Title: "t", TitleSource: chat.TitleSourcePending, CreatedAt: then, UpdatedAt: then}); err != nil {
		t.Fatal(err)
	}
	renamed, found, err := repository.RenameSession(ctx, "owner", "a", "mine", then.Add(time.Hour))
	if err != nil || !found || renamed.Title != "mine" || renamed.TitleSource != chat.TitleSourceUser || !renamed.UpdatedAt.Equal(then) {
		t.Fatalf("rename moved ordering activity: %+v %v %v", renamed, found, err)
	}
	if applied, err := repository.ApplyAutoTitle(ctx, "owner", "a", "auto"); err != nil || applied {
		t.Fatalf("auto title overwrote a rename: %v %v", applied, err)
	}
}

func TestAutoTitleIsConditionalOnThePendingSource(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "title.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, session := range []chat.Session{
		{ID: "pending", OwnerID: "owner", OwnerName: "o", Title: "新对话", TitleSource: chat.TitleSourcePending, CreatedAt: now, UpdatedAt: now},
		{ID: "renamed", OwnerID: "owner", OwnerName: "o", Title: "新对话", TitleSource: chat.TitleSourcePending, CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := repository.ApplyAutoTitle(ctx, "other", "pending", "stolen"); err != nil || applied {
		t.Fatalf("foreign owner applied = %v %v", applied, err)
	}
	if _, _, err := repository.RenameSession(ctx, "owner", "renamed", "mine", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if applied, err := repository.ApplyAutoTitle(ctx, "owner", "renamed", "auto"); err != nil || applied {
		t.Fatalf("rename lost the race: applied = %v %v", applied, err)
	}
	applied, err := repository.ApplyAutoTitle(ctx, "owner", "pending", "auto")
	if err != nil || !applied {
		t.Fatalf("pending apply = %v %v", applied, err)
	}
	if again, _ := repository.ApplyAutoTitle(ctx, "owner", "pending", "again"); again {
		t.Fatal("an automatic title was written twice")
	}
	session, _, err := repository.Session(ctx, "owner", "pending")
	if err != nil || session.Title != "auto" || session.TitleSource != chat.TitleSourceAuto || !session.UpdatedAt.Equal(now) {
		t.Fatalf("session = %+v %v", session, err)
	}
	kept, _, _ := repository.Session(ctx, "owner", "renamed")
	if kept.Title != "mine" || kept.TitleSource != chat.TitleSourceUser {
		t.Fatalf("renamed = %+v", kept)
	}
}

func TestSessionPageTotalCountsOwnerAndSearchBeforeTheCursor(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "session-total.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	insert := func(id, owner, title, updatedAt string) {
		t.Helper()
		if _, err := repository.db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, id, owner, owner, title, updatedAt, updatedAt); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 16; index++ {
		insert("project-"+string(rune('a'+index)), "owner", "Project art", "2026-09-01T00:00:00Z")
	}
	insert("unrelated", "owner", "Unrelated", "2026-08-01T00:00:00Z")
	for index := 0; index < 5; index++ {
		insert("foreign-"+string(rune('a'+index)), "other-owner", "Project art", "2026-09-01T00:00:00Z")
	}
	ctx := context.Background()

	first, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 15})
	if err != nil || len(first.Sessions) != 15 || first.Total != 16 || first.NextCursor == "" {
		t.Fatalf("first page = %d sessions, total %d, cursor %q: %v", len(first.Sessions), first.Total, first.NextCursor, err)
	}
	second, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 15, Cursor: first.NextCursor})
	if err != nil || len(second.Sessions) != 1 || second.Total != 16 || second.NextCursor != "" {
		t.Fatalf("second page = %d sessions, total %d, cursor %q: %v", len(second.Sessions), second.Total, second.NextCursor, err)
	}
	foreign, err := repository.ListSessionsPage(ctx, "other-owner", chat.SessionListQuery{Search: "project", Limit: 15})
	if err != nil || len(foreign.Sessions) != 5 || foreign.Total != 5 {
		t.Fatalf("foreign page = %d sessions, total %d: %v", len(foreign.Sessions), foreign.Total, err)
	}
	none, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "missing", Limit: 15})
	if err != nil || len(none.Sessions) != 0 || none.Total != 0 {
		t.Fatalf("empty page = %d sessions, total %d: %v", len(none.Sessions), none.Total, err)
	}
	exact, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "unrelated", Limit: 15})
	if err != nil || len(exact.Sessions) != 1 || exact.Total != 1 {
		t.Fatalf("single page = %d sessions, total %d: %v", len(exact.Sessions), exact.Total, err)
	}

	// Deleting the last row on the last page lowers the total without leaving a phantom page.
	if _, err := repository.db.Exec(`DELETE FROM go_chat_sessions WHERE id = 'project-p'`); err != nil {
		t.Fatal(err)
	}
	last, err := repository.ListSessionsPage(ctx, "owner", chat.SessionListQuery{Search: "project", Limit: 15})
	if err != nil || len(last.Sessions) != 15 || last.Total != 15 || last.NextCursor != "" {
		t.Fatalf("page after deleting the last row = %d sessions, total %d, cursor %q: %v", len(last.Sessions), last.Total, last.NextCursor, err)
	}
}
