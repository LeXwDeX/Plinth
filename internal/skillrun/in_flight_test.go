package skillrun

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A step has work in flight only while a human grant waits for its decision or
// its bound Task has not ended. Deleting a session refuses exactly that (#384);
// a step that has not started, is prepared, was denied, finished or is bound to
// a Task that ended is at rest, and a session that holds only such steps can be
// deleted with them.
func TestStepInFlightOnlyForAPendingGrantOrAnActiveTask(t *testing.T) {
	for _, test := range []struct {
		name         string
		phase        StepPhase
		taskState    generation.TaskState
		grantPending bool
		want         bool
	}{
		{"planned", Planned, "", false, false},
		{"prepared", Prepared, "", false, false},
		{"grant waiting for a decision", GrantPending, "", true, true},
		{"grant denied or decided", GrantPending, "", false, false},
		{"task queued", TaskBound, generation.TaskQueued, false, true},
		{"task running", TaskBound, generation.TaskRunning, false, true},
		{"task materializing", TaskBound, generation.TaskMaterializing, false, true},
		{"task succeeded, output not recorded", TaskBound, generation.TaskSucceeded, false, false},
		{"task failed", TaskBound, generation.TaskFailed, false, false},
		{"task canceled", TaskBound, generation.TaskCanceled, false, false},
		{"bound Task of unknown state", TaskBound, "", false, true},
		{"output ready", OutputReady, generation.TaskSucceeded, false, false},
		// A pending-grant flag or a Task state on a step in another phase says
		// nothing about it.
		{"planned with a stray grant flag", Planned, "", true, false},
		{"output ready with an active Task", OutputReady, generation.TaskRunning, true, false},
	} {
		if got := (Step{Phase: test.phase}).InFlight(test.taskState, test.grantPending); got != test.want {
			t.Errorf("%s: InFlight(%q, %v) = %v, want %v", test.name, test.taskState, test.grantPending, got, test.want)
		}
	}
}
