package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
)

// #461: the automatic delivery completion's durable record and its scan
// watermark. The scan must never look at a Task that is older than the
// watermark (no historical Task is completed), and one row per Task must keep
// what the task already cost.

func autoCompletionTask(t *testing.T, r *Repository, id, owner, session, capability, updatedAt string) {
	t.Helper()
	offering := `{"id":"meshy/processing/convert","revision":"1","provider_id":"meshy","capability":"format-conversion"}`
	if _, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, session_id, capability, offering_json, input_json, request_hash, idempotency_key,
  state, progress, current_attempt_id, version, created_at, updated_at, finished_at)
 VALUES (?,?,?,?,?,'{}','hash','', 'SUCCEEDED',100,'attempt',1,?,?,?)`,
		id, owner, session, capability, offering, updatedAt, updatedAt, updatedAt); err != nil {
		t.Fatal(err)
	}
}

func TestAutoCompletionWatermarkStartsAtTheFirstScan(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s", "owner", chat.Session{})
	stamp := formatTaskTime(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC))
	autoCompletionTask(t, r, "old-task", "owner", "s", "text-to-3d", stamp)

	if _, known, err := r.AutoCompletionWatermark(ctx); err != nil || known {
		t.Fatalf("watermark known=%v err=%v, want none before the first scan", known, err)
	}
	// The first scan writes the position now and lists nothing: the historical
	// Task above is never looked at.
	candidates, err := r.AutoCompletionCandidates(ctx, []generation.Capability{generation.TextTo3D}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none on the first scan", candidates)
	}
	cursor, known, err := r.AutoCompletionWatermark(ctx)
	if err != nil || !known || cursor.TaskID != "" {
		t.Fatalf("watermark = %+v known=%v err=%v", cursor, known, err)
	}
	if cursor.UpdatedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("watermark = %v, want the time of the first scan", cursor.UpdatedAt)
	}

	// A Task that succeeds after the watermark is found exactly once.
	fresh := formatTaskTime(cursor.UpdatedAt.Add(time.Second))
	autoCompletionTask(t, r, "fresh-task", "owner", "s", "text-to-3d", fresh)
	candidates, err = r.AutoCompletionCandidates(ctx, []generation.Capability{generation.TextTo3D}, 10)
	if err != nil || len(candidates) != 1 || candidates[0].TaskID != "fresh-task" {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}
	// It has a record now, so it is never listed again.
	if err := r.SaveAutoCompletionRecord(ctx, modeldelivery.AutoCompletionRecord{
		TaskID: "fresh-task", OwnerID: "owner", State: modeldelivery.AutoStateConverting,
		PaidRuns: 1, PaidCredits: 1, NextRunAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	candidates, err = r.AutoCompletionCandidates(ctx, []generation.Capability{generation.TextTo3D}, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v err=%v, want the recorded Task left alone", candidates, err)
	}

	// A Task of another capability is not a candidate.
	autoCompletionTask(t, r, "picture-task", "owner", "s", "text-to-image", fresh)
	candidates, err = r.AutoCompletionCandidates(ctx, []generation.Capability{generation.TextTo3D}, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v err=%v, want the other capability ignored", candidates, err)
	}
}

// A record whose round has come is due, and saving it again updates the same
// row instead of adding a second one.
func TestAutoCompletionRecordRoundTripsAndIsDue(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	past := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	record := modeldelivery.AutoCompletionRecord{
		TaskID: "task-1", OwnerID: "owner", PolicyRevision: "2026-10-22.330.1",
		State: modeldelivery.AutoStateConverting, Attempts: 2, PlanID: "plan-1",
		PaidRuns: 1, PaidCredits: 1, Reason: "plan_started",
		FirstRunAt: past.Add(-time.Hour), NextRunAt: past, UpdatedAt: past,
	}
	if err := r.SaveAutoCompletionRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, exists, err := r.AutoCompletionRecord(ctx, "task-1")
	if err != nil || !exists {
		t.Fatalf("record = %+v exists=%v err=%v", got, exists, err)
	}
	if got.State != record.State || got.PaidRuns != 1 || got.PaidCredits != 1 ||
		got.PolicyRevision != record.PolicyRevision || got.PlanID != "plan-1" ||
		!got.FirstRunAt.Equal(record.FirstRunAt) || !got.NextRunAt.Equal(past) {
		t.Fatalf("record = %+v", got)
	}

	due, err := r.AutoCompletionDue(ctx, past.Add(time.Second), 10)
	if err != nil || len(due) != 1 || due[0].TaskID != "task-1" {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	if due, err := r.AutoCompletionDue(ctx, past.Add(-time.Second), 10); err != nil || len(due) != 0 {
		t.Fatalf("due before its round = %+v err=%v", due, err)
	}

	// A Task that is done is never due again.
	record.State, record.NextRunAt = modeldelivery.AutoStateDone, past
	if err := r.SaveAutoCompletionRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if due, err := r.AutoCompletionDue(ctx, past.Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("done record is due: %+v err=%v", due, err)
	}
	var rows int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM model_delivery_auto_completions WHERE task_id = 'task-1'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows = %d err=%v, want one row per task", rows, err)
	}
}
