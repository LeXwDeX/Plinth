package sqlite_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A Task created through the real claim-and-bind transaction is one step, not
// two: the grant it consumed is bound and stops counting as an approved step
// without a Task. An approved grant that has no Task yet still counts, so the
// asset never sees a gap or a double count while a step moves from the one to
// the other.
func TestStepUsageCountsABoundTaskOnceThroughTheRealClaimAndBind(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "steps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	request := executiongrant.StepRequest{Entry: approvalpolicy.StepEntryList, Steps: approvalpolicy.StepsPolicy{
		Counted:     []string{"text-to-3d"},
		DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: 5},
		DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 5},
	}}
	session := fixtureSessionID("owner-a", "steps")
	createdAt := time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC)
	newTask := func(id string) generation.Task {
		return generation.Task{
			ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: session, Capability: generation.TextTo3D,
			Offering: generation.Offering{
				ID: "test/offering", ProviderID: "test", ModelID: "test", Capability: generation.TextTo3D,
			},
			Input:        json.RawMessage(`{"prompt":"` + id + `"}`),
			State:        generation.TaskQueued,
			NextActionAt: createdAt,
			Version:      1,
			CreatedAt:    createdAt,
			UpdatedAt:    createdAt,
			Assets:       []generation.Asset{},
			RequestHash:  "request-hash",
		}
	}
	usage := func() int {
		t.Helper()
		summary, err := repository.StepUsage(ctx, "owner-a", session, request)
		if err != nil {
			t.Fatal(err)
		}
		return summary.Used
	}

	// An approved step without a Task holds one step.
	approveCreateFixture(t, repository, newTask("task-held"))
	if used := usage(); used != 1 {
		t.Fatalf("an approved step without a Task counts %d, want 1", used)
	}
	// Created through the real transaction: the Task counts, its grant no longer does.
	created := mustApprovedCreate(t, repository, newTask("task-created"), generation.Event{}, 0)
	bound, err := repository.GetExecutionGrant(ctx, "owner-a", created.Admission.RequestID)
	if err != nil || bound.Status != executiongrant.Bound || bound.TaskID != "task-created" {
		t.Fatalf("grant after Create = %+v err=%v", bound, err)
	}
	if used := usage(); used != 2 {
		t.Fatalf("one held step and one created Task count %d, want 2", used)
	}
}
