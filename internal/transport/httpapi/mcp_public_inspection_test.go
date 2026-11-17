package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// platformInspection seeds one persisted self-check record, as the platform's
// review tools would have written it, and returns the record it stored.
func platformInspection(t *testing.T, f *mcpFixture, session string, task generation.Task) chat.SelfCheckRecord {
	t.Helper()
	checkedAt := time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC)
	record := chat.SelfCheckRecord{
		TaskID: string(task.ID), ModelAssetID: "reviewed-model",
		Checks: []chat.SelfCheckCheck{{
			Round: 1, Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
			UsageFailed: []string{"scale", "watertight"},
			Purpose:     "display", PurposeLabel: "展示/渲染", PurposeSource: "default_profile",
			At: checkedAt.Add(-time.Minute),
		}},
		Conclusion: &chat.SelfCheckConclusion{
			Status: "passed", Summary: "the Agent's own words must stay out of the block",
			Waiver: "用户接受", Purpose: "display", PurposeLabel: "展示/渲染", PurposeSource: "default_profile",
			At: checkedAt,
		},
		CreatedAt: checkedAt.Add(-time.Minute), UpdatedAt: checkedAt,
	}
	if _, _, err := f.repository.SaveSelfCheck(context.Background(), "owner-a", session, record, 0); err != nil {
		t.Fatal(err)
	}
	return record
}

func inspectionOf(t *testing.T, f *mcpFixture, key, tool, taskID string) map[string]any {
	t.Helper()
	result := f.call(key, tool, map[string]any{"task_id": taskID})
	if result.isError {
		t.Fatalf("%s = %s", tool, result.raw)
	}
	assertServedSchema(t, f, tool, result)
	inspection, ok := result.status(t)["inspection"].(map[string]any)
	if !ok {
		t.Fatalf("%s: inspection = %v", tool, result.status(t)["inspection"])
	}
	return inspection
}

// TestMCPInspectionReportsThePlatformSelfCheck is #332 step 7c-2: get_task and
// get_task_assets report the platform's last verdict of a succeeded task with
// only the platform's role words, check ids, the waiver boolean and the time.
func TestMCPInspectionReportsThePlatformSelfCheck(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key, generate := f.issueKey("owner-a", "read"), f.issueKey("owner-a", "generate")
	picture := f.upload(generate, testPNG(t, 96))
	started := f.call(generate, "create_model", map[string]any{
		"images": []any{picture}, "asset": "Inspection", "idempotency_key": idem("inspection-3d"),
	})
	task := f.finish("owner-a", taskID(t, started))
	if task.State != generation.TaskSucceeded || task.Capability != generation.ImageTo3D {
		t.Fatalf("task = %+v", task)
	}
	platformInspection(t, f, task.SessionID, task)

	for _, tool := range []string{"get_task", "get_task_assets"} {
		inspection := inspectionOf(t, f, key, tool, string(task.ID))
		if inspection["task_id"] != string(task.ID) || inspection["source"] != "platform_self_check" ||
			inspection["verdict"] != "passed" || inspection["waiver"] != true ||
			inspection["checked_at"] != "2026-11-17T08:30:00Z" {
			t.Fatalf("%s inspection = %v", tool, inspection)
		}
		purpose := inspection["purpose"].(map[string]any)
		if purpose["id"] != "display" || purpose["label"] != "展示/渲染" || purpose["source"] != "default_profile" {
			t.Fatalf("%s purpose = %v", tool, purpose)
		}
		failed, ok := inspection["failed"].([]any)
		if !ok || len(failed) != 2 || failed[0].(map[string]any)["check"] != "scale" ||
			failed[1].(map[string]any)["check"] != "watertight" {
			t.Fatalf("%s failed = %v", tool, inspection["failed"])
		}
	}
}

// TestMCPInspectionIsNullWithoutAPlatformRecord covers the tasks the platform
// did not review (2D, running, failed) and the tasks of another person.
func TestMCPInspectionIsNullWithoutAPlatformRecord(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read", "generate")

	// A succeeded 2D task has no record.
	twoD := f.succeededPlainTask("owner-a", "", "inspection-2d")
	assertServedSchema(t, f, "get_task", f.call(key, "get_task", map[string]any{"task_id": string(twoD.ID)}))
	if got := f.call(key, "get_task", map[string]any{"task_id": string(twoD.ID)}).status(t)["inspection"]; got != nil {
		t.Fatalf("a 2D task has an inspection: %v", got)
	}

	// A running task does not read the record at all.
	started := f.call(key, "generate_image", map[string]any{
		"prompt": "still running", "asset": "Running", "idempotency_key": idem("inspection-running"),
	})
	running := taskID(t, started)
	before := f.inspections.calls.Load()
	if got := f.call(key, "get_task", map[string]any{"task_id": running}).status(t)["inspection"]; got != nil {
		t.Fatalf("a running task has an inspection: %v", got)
	}
	if after := f.inspections.calls.Load(); after != before {
		t.Fatalf("a running task read the self-check store: %d -> %d", before, after)
	}

	// list_tasks, list_tasks of an asset and get_status never name a task.
	before = f.inspections.calls.Load()
	f.call(key, "list_tasks", nil)
	f.call(key, "list_tasks", map[string]any{"asset_id": started.structured["asset"].(map[string]any)["id"]})
	f.call(key, "get_status", nil)
	if after := f.inspections.calls.Load(); after != before {
		t.Fatalf("a listing read the self-check store: %d -> %d", before, after)
	}

	// A failed task has no platform verdict even if a record was written
	// before it failed.
	failed := f.succeededTask("owner-a", "", "inspection-failed")
	platformInspection(t, f, failed.SessionID, failed)
	f.exec(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ? WHERE id = ?`,
		`{"class":"rate_limited","retryable":true,"detail":"upstream"}`, string(failed.ID))
	before = f.inspections.calls.Load()
	if got := f.call(key, "get_task", map[string]any{"task_id": string(failed.ID)}).status(t)["inspection"]; got != nil {
		t.Fatalf("a failed task has an inspection: %v", got)
	}
	if after := f.inspections.calls.Load(); after != before {
		t.Fatalf("a failed task read the self-check store: %d -> %d", before, after)
	}

	// Another person's succeeded task is refused and never read.
	theirs := f.succeededTask("owner-b", "", "inspection-foreign")
	before = f.inspections.calls.Load()
	foreign := f.call(key, "get_task", map[string]any{"task_id": string(theirs.ID)})
	if !foreign.isError {
		t.Fatalf("a foreign task was readable: %s", foreign.raw)
	}
	assertServedSchema(t, f, "get_task", foreign)
	if after := f.inspections.calls.Load(); after != before {
		t.Fatalf("a foreign task read the self-check store: %d -> %d", before, after)
	}
}

// TestMCPInspectionFindsTheRecordWrittenInABranchSession covers the reason the
// lookup is task-scoped: the Agent may review the task in a branch (or the
// record may live in an ancestor session), not in the task's own session.
func TestMCPInspectionFindsTheRecordWrittenInABranchSession(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	f.issueKey("owner-a", "read")
	task := f.succeededPlainTask("owner-a", "", "inspection-branch")
	now := time.Now().UTC()
	if _, err := f.repository.CreateSession(context.Background(), chat.Session{
		ID: "inspection-branch-session", OwnerID: "owner-a", OwnerName: "Owner", Title: "branch",
		ParentSessionID: task.SessionID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	platformInspection(t, f, "inspection-branch-session", task)

	inspection := inspectionOf(t, f, f.issueKey("owner-a", "read"), "get_task", string(task.ID))
	if inspection["verdict"] != "passed" || inspection["checked_at"] != "2026-11-17T08:30:00Z" {
		t.Fatalf("inspection = %v", inspection)
	}
}

// TestMCPInspectionReadFailureDoesNotFailTheCall: the status block never
// fails a call, a read error is logged and inspection stays null.
func TestMCPInspectionReadFailureDoesNotFailTheCall(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	task := f.succeededTask("owner-a", "", "inspection-unreadable")
	platformInspection(t, f, task.SessionID, task)
	f.inspections.fail.Store(true)
	result := f.call(key, "get_task", map[string]any{"task_id": string(task.ID)})
	if result.isError {
		t.Fatalf("a self-check read failure failed the call: %s", result.raw)
	}
	assertServedSchema(t, f, "get_task", result)
	if got := result.status(t)["inspection"]; got != nil {
		t.Fatalf("inspection = %v", got)
	}
}
