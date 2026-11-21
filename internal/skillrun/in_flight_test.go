package skillrun

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A step has work in flight while a grant still stands between the person's
// decision and the paid submission or its result, or its bound Task has not
// ended. Deleting a session refuses exactly that (#384, #430); a step that has
// not started, is prepared, was denied, finished or is bound to a Task that
// ended is at rest, and a session that holds only such steps can be deleted
// with them.
func TestStepInFlightOnlyForAStandingGrantOrAnActiveTask(t *testing.T) {
	for _, test := range []struct {
		name        string
		phase       StepPhase
		taskState   generation.TaskState
		grantStatus executiongrant.Status
		want        bool
	}{
		{"planned", Planned, "", "", false},
		{"prepared", Prepared, "", "", false},
		{"grant waiting for a decision", GrantPending, "", executiongrant.Pending, true},
		{"grant approved, submission not made", GrantPending, "", executiongrant.Approved, true},
		{"grant claimed in the paid submission", GrantPending, "", executiongrant.Claimed, true},
		{"grant bound, step still has no Task", GrantPending, "", executiongrant.Bound, true},
		{"grant denied", GrantPending, "", executiongrant.Denied, false},
		{"grant row missing", GrantPending, "", "", false},
		{"task queued", TaskBound, generation.TaskQueued, "", true},
		{"task running", TaskBound, generation.TaskRunning, "", true},
		{"task materializing", TaskBound, generation.TaskMaterializing, "", true},
		{"task succeeded, output not recorded", TaskBound, generation.TaskSucceeded, "", false},
		{"task failed", TaskBound, generation.TaskFailed, "", false},
		{"task canceled", TaskBound, generation.TaskCanceled, "", false},
		{"bound Task of unknown state", TaskBound, "", "", true},
		{"output ready", OutputReady, generation.TaskSucceeded, "", false},
		// A grant status or a Task state on a step in another phase says
		// nothing about it.
		{"planned with a stray grant status", Planned, "", executiongrant.Pending, false},
		{"output ready with an active Task", OutputReady, generation.TaskRunning, executiongrant.Bound, false},
	} {
		if got := (Step{Phase: test.phase}).InFlight(test.taskState, test.grantStatus); got != test.want {
			t.Errorf("%s: InFlight(%q, %q) = %v, want %v", test.name, test.taskState, test.grantStatus, got, test.want)
		}
	}
}
