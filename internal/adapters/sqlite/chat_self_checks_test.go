package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func selfCheckTestRepository(t *testing.T) *Repository {
	t.Helper()
	r, err := openTestRepository(filepath.Join(t.TempDir(), "self-check.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	now := time.Now().UTC()
	if _, err := r.CreateSession(context.Background(), chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "t",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateSession(context.Background(), chat.Session{
		ID: "session-b", OwnerID: "owner-b", OwnerName: "Other", Title: "t",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_tasks
		(id, owner_id, capability, offering_json, input_json, request_hash, state, version, created_at, updated_at)
		VALUES ('task-a','owner-a','image-to-3d','{}','{}','hash','SUCCEEDED',1,?,?),
		       ('task-b','owner-a','image-to-3d','{}','{}','hash-b','SUCCEEDED',1,?,?)`,
		formatTaskTime(now), formatTaskTime(now), formatTaskTime(now), formatTaskTime(now)); err != nil {
		t.Fatal(err)
	}
	return r
}

func selfCheckRecord(task, asset string, status string) chat.SelfCheckRecord {
	now := time.Now().UTC()
	record := chat.SelfCheckRecord{
		TaskID: task, ModelAssetID: asset, CreatedAt: now, UpdatedAt: now,
		Checks: []chat.SelfCheckCheck{{Round: 1, Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true, At: now}},
	}
	if status != "" {
		record.Conclusion = &chat.SelfCheckConclusion{Status: status, Summary: "checked", At: now}
	}
	return record
}

func TestSelfCheckStoreInsertsReadsAndUpdatesWithCompareAndSwap(t *testing.T) {
	r := selfCheckTestRepository(t)
	ctx := context.Background()
	first := selfCheckRecord("task-a", "asset-a", "")
	stored, conflict, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", first, 0)
	if err != nil || conflict || stored.Revision != 1 || stored.TaskID != "task-a" {
		t.Fatalf("insert = %+v conflict=%v err=%v", stored, conflict, err)
	}
	var status, raw string
	if err := r.db.QueryRow(`SELECT status, record_json FROM chat_self_checks
		WHERE owner_id='owner-a' AND session_id='session-a' AND task_id='task-a'`).Scan(&status, &raw); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("status before a conclusion = %q", status)
	}
	if decoded, err := chat.DecodeSelfCheckRecord(raw, 1); err != nil || len(decoded.Checks) != 1 {
		t.Fatalf("record_json = %q: %v", raw, err)
	}

	second := selfCheckRecord("task-a", "asset-a", "passed")
	second.Checks = append(second.Checks, chat.SelfCheckCheck{
		Round: 1, Kind: chat.SelfCheckKindReference, Tool: "compare_model_reference",
		Views: []string{"front"}, Handles: []string{"@img1"}, OK: true, At: time.Now().UTC(),
	})
	updated, conflict, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", second, 1)
	if err != nil || conflict || updated.Revision != 2 || updated.Conclusion == nil || updated.Conclusion.Status != "passed" {
		t.Fatalf("update = %+v conflict=%v err=%v", updated, conflict, err)
	}
	if err := r.db.QueryRow(`SELECT status FROM chat_self_checks
		WHERE owner_id='owner-a' AND session_id='session-a' AND task_id='task-a'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "passed" {
		t.Fatalf("status column = %q", status)
	}
	// A stale revision is a lost race, not an overwrite.
	if _, conflict, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", second, 1); err != nil || !conflict {
		t.Fatalf("stale save = conflict %v err %v", conflict, err)
	}
	// An insert against an existing unique triple reports a conflict too.
	if _, conflict, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", first, 0); err != nil || !conflict {
		t.Fatalf("duplicate insert = conflict %v err %v", conflict, err)
	}
}

// TestSelfCheckStoreReadsTaskAcrossSessions is the #332 7c-2 lookup: the
// record may live in a branch or ancestor session, so the task-scoped read
// spans the owner's sessions and orders newest first.
func TestSelfCheckStoreReadsTaskAcrossSessions(t *testing.T) {
	r := selfCheckTestRepository(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := r.CreateSession(ctx, chat.Session{
		ID: "session-c", OwnerID: "owner-a", OwnerName: "Owner", Title: "branch",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	older := selfCheckRecord("task-a", "asset-older", "")
	older.UpdatedAt = now
	if _, _, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", older, 0); err != nil {
		t.Fatal(err)
	}
	newer := selfCheckRecord("task-a", "asset-newer", "passed")
	newer.UpdatedAt = now.Add(time.Minute)
	if _, _, err := r.SaveSelfCheck(ctx, "owner-a", "session-c", newer, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", selfCheckRecord("task-b", "asset-b", ""), 0); err != nil {
		t.Fatal(err)
	}

	records, err := r.SelfChecksByTask(ctx, "owner-a", "task-a", 8)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v err=%v", records, err)
	}
	if records[0].ModelAssetID != "asset-newer" || records[1].ModelAssetID != "asset-older" {
		t.Fatalf("order = %q, %q", records[0].ModelAssetID, records[1].ModelAssetID)
	}
	if limited, err := r.SelfChecksByTask(ctx, "owner-a", "task-a", 1); err != nil || len(limited) != 1 ||
		limited[0].ModelAssetID != "asset-newer" {
		t.Fatalf("limited = %+v err=%v", limited, err)
	}
	if foreign, err := r.SelfChecksByTask(ctx, "owner-b", "task-a", 8); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign = %+v err=%v", foreign, err)
	}
}

func TestSelfCheckStoreScopesReadsAndCascadesDeletes(t *testing.T) {
	r := selfCheckTestRepository(t)
	ctx := context.Background()
	if _, _, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", selfCheckRecord("task-a", "", ""), 0); err != nil {
		t.Fatal(err)
	}
	if _, found, err := r.SelfCheck(ctx, "owner-b", "session-a", "task-a"); err != nil || found {
		t.Fatalf("foreign owner = found %v err %v", found, err)
	}
	if _, found, err := r.SelfCheck(ctx, "owner-a", "session-b", "task-a"); err != nil || found {
		t.Fatalf("foreign session = found %v err %v", found, err)
	}
	if records, err := r.ListSelfChecks(ctx, "owner-b", "session-a"); err != nil || len(records) != 0 {
		t.Fatalf("foreign list = %v err %v", records, err)
	}
	// Deleting the session cascades to its records.
	if _, err := r.db.Exec(`DELETE FROM go_chat_sessions WHERE id='session-a'`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := r.SelfCheck(ctx, "owner-a", "session-a", "task-a"); err != nil || found {
		t.Fatalf("cascade = found %v err %v", found, err)
	}
}

func TestSelfCheckStoreListsNewestFirst(t *testing.T) {
	r := selfCheckTestRepository(t)
	ctx := context.Background()
	for index, task := range []string{"task-a", "task-b"} {
		record := selfCheckRecord(task, "", "")
		record.UpdatedAt = time.Now().UTC().Add(time.Duration(index) * time.Minute)
		record.CreatedAt = record.UpdatedAt
		if _, _, err := r.SaveSelfCheck(ctx, "owner-a", "session-a", record, 0); err != nil {
			t.Fatal(err)
		}
	}
	records, err := r.ListSelfChecks(ctx, "owner-a", "session-a")
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %v err %v", records, err)
	}
	if records[0].TaskID != "task-b" || records[1].TaskID != "task-a" {
		t.Fatalf("order = %v, %v", records[0].TaskID, records[1].TaskID)
	}
}
