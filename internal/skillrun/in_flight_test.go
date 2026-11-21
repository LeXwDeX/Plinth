package skillrun

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A step has work in flight while a grant waits for its decision or is being
// claimed, while the Task a bound grant names has not ended, or while the
// step's own bound Task has not ended. Deleting a session refuses exactly that
// (#384, #430). An approved grant has no Task and no transition of its own, so
// it is at rest: the turn that would submit it is the one the deletion stops.
// A bound grant whose Task ended, a denied grant and a step that has not
// started, is prepared or finished are at rest too.
func TestStepInFlightOnlyForAPendingOrClaimedGrantOrAnActiveTask(t *testing.T) {
	for _, test := range []struct {
		name           string
		phase          StepPhase
		taskState      generation.TaskState
		grantStatus    executiongrant.Status
		grantTaskState generation.TaskState
		want           bool
	}{
		{"planned", Planned, "", "", "", false},
		{"prepared", Prepared, "", "", "", false},
		{"grant waiting for a decision", GrantPending, "", executiongrant.Pending, "", true},
		{"grant claimed in the paid submission", GrantPending, "", executiongrant.Claimed, "", true},
		{"grant approved, submission ahead", GrantPending, "", executiongrant.Approved, "", false},
		{"grant denied", GrantPending, "", executiongrant.Denied, "", false},
		{"grant row missing", GrantPending, "", "", "", false},
		{"bound grant with a queued Task", GrantPending, "", executiongrant.Bound, generation.TaskQueued, true},
		{"bound grant with a running Task", GrantPending, "", executiongrant.Bound, generation.TaskRunning, true},
		{"bound grant with a materializing Task", GrantPending, "", executiongrant.Bound, generation.TaskMaterializing, true},
		{"bound grant with a succeeded Task", GrantPending, "", executiongrant.Bound, generation.TaskSucceeded, false},
		{"bound grant with a failed Task", GrantPending, "", executiongrant.Bound, generation.TaskFailed, false},
		{"bound grant with a canceled Task", GrantPending, "", executiongrant.Bound, generation.TaskCanceled, false},
		{"bound grant with an unreadable Task", GrantPending, "", executiongrant.Bound, "", true},
		{"task queued", TaskBound, generation.TaskQueued, "", "", true},
		{"task running", TaskBound, generation.TaskRunning, "", "", true},
		{"task materializing", TaskBound, generation.TaskMaterializing, "", "", true},
		{"task succeeded, output not recorded", TaskBound, generation.TaskSucceeded, "", "", false},
		{"task failed", TaskBound, generation.TaskFailed, "", "", false},
		{"task canceled", TaskBound, generation.TaskCanceled, "", "", false},
		{"bound Task of unknown state", TaskBound, "", "", "", true},
		{"output ready", OutputReady, generation.TaskSucceeded, "", "", false},
		// A grant status or a Task state on a step in another phase says
		// nothing about it.
		{"planned with a stray grant status", Planned, "", executiongrant.Pending, generation.TaskRunning, false},
		{"output ready with an active Task", OutputReady, generation.TaskRunning, executiongrant.Bound, generation.TaskRunning, false},
	} {
		if got := (Step{Phase: test.phase}).InFlight(test.taskState, test.grantStatus, test.grantTaskState); got != test.want {
			t.Errorf("%s: InFlight(%q, %q, %q) = %v, want %v", test.name,
				test.taskState, test.grantStatus, test.grantTaskState, got, test.want)
		}
	}
}
