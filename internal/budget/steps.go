package budget

import "time"

// The session projection of a plan's steps (#410). The browser used to pair
// every plan step with the session's Tasks itself; the server now does it and
// the card only renders the result. The pairing rule is unchanged: same
// capability, the pinned Offering when both sides know it, and each Task is
// claimed by one step. Only the Task state vocabulary lives here: this package
// owns plan rules, not the Task state machine, so the caller states the
// terminal facts it read from the kernel.

// StepStatus is one plan step's standing in the session projection.
type StepStatus struct {
	// Index is the step's 1-based position in the plan.
	Index int `json:"index"`
	// State is one of the Step* constants.
	State string `json:"state"`
	// TaskID is the Task that ran the step; empty while none does.
	TaskID string `json:"task_id,omitempty"`
	// TaskState is the matched Task's own state, for the card's detail line.
	TaskState string `json:"task_state,omitempty"`
	// Optional repeats the step's declared flag, so a reader can tell an
	// optional step it may skip from a required one that was not run.
	Optional bool `json:"optional,omitempty"`
}

// StepTask is one Task fact of a session the pairing reads.
type StepTask struct {
	TaskID     string
	Capability string
	OfferingID string
	// State is the Task's own state as the caller read it.
	State string
	// Succeeded and Finished are the Task's terminal facts as the caller read
	// them from the generation kernel.
	Succeeded bool
	Finished  bool
	// CreatedAt is when the Task was submitted; a Task older than the plan's
	// decision did not run one of its steps.
	CreatedAt time.Time
}

// StepStatusInput is everything the projection needs about one session: its
// plan, whether the plan is decided and when, whether the session's turn still
// runs, and the session's Tasks.
type StepStatusInput struct {
	Plan Plan
	// PlanState is the plan's own state; a plan that still waits for the
	// person's decision has every step waiting.
	PlanState string
	DecidedAt time.Time
	// TurnRunning says the session's turn has not ended, so a step without a
	// Task may still get one.
	TurnRunning bool
	Tasks       []StepTask
}

// PlanStepStatuses pairs every step of the plan with the Task that ran it and
// reports each step's state in plan order (#410). A step with no Task reads
// pending while the plan is undecided or the session's turn still runs; once
// nothing can submit it the step reads not_run, or optional_not_run when the
// step is optional.
func PlanStepStatuses(input StepStatusInput) []StepStatus {
	steps := input.Plan.Steps
	statuses := make([]StepStatus, 0, len(steps))
	used := make([]bool, len(input.Tasks))
	for index, step := range steps {
		matched := -1
		if step.OfferingID != "" {
			matched = matchTask(input.Tasks, used, step.Capability, step.OfferingID)
		}
		if matched < 0 {
			// The pinned Offering has no Task: the step takes the next Task of
			// its capability, as the browser's pairing always did.
			matched = matchTask(input.Tasks, used, step.Capability, "")
		}
		status := StepStatus{Index: index + 1, Optional: step.Optional}
		if matched < 0 {
			status.State = stepWithoutTaskState(input, step)
			statuses = append(statuses, status)
			continue
		}
		used[matched] = true
		task := input.Tasks[matched]
		status.TaskID, status.TaskState = task.TaskID, task.State
		switch {
		case task.Succeeded:
			status.State = StepDone
		case task.Finished:
			status.State = StepFailed
		default:
			status.State = StepRunning
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// stepWithoutTaskState is the state of a step no Task ran: waiting while the
// plan is undecided or the session's turn still runs, else not run.
func stepWithoutTaskState(input StepStatusInput, step PlanStep) string {
	if input.PlanState == StatePending || input.TurnRunning {
		return StepPending
	}
	if step.Optional {
		return StepOptionalNotRun
	}
	return StepNotRun
}

// matchTask returns the index of the first unused Task of the capability, and
// of the Offering when one is named; -1 when none is left.
func matchTask(tasks []StepTask, used []bool, capability, offeringID string) int {
	for index, task := range tasks {
		if used[index] || task.Capability != capability {
			continue
		}
		if offeringID != "" && task.OfferingID != offeringID {
			continue
		}
		return index
	}
	return -1
}

// RequiredStepsDone reports whether every step the plan is not complete
// without has a succeeded Task (#410). It is the completion rule: the caller
// applies it when a Task settles, so a plan whose required steps all succeeded
// becomes completed and stops spending. An optional step never blocks it.
func RequiredStepsDone(statuses []StepStatus) bool {
	for _, status := range statuses {
		if status.Optional {
			continue
		}
		if status.State != StepDone {
			return false
		}
	}
	return true
}

// EligibleStepTasks drops the Tasks that started before the plan was decided:
// a historical Task of the same capability must not fill a step of this plan.
// A Task with no usable time is kept, so a caller that cannot date its Tasks
// still pairs them as before.
func EligibleStepTasks(tasks []StepTask, decidedAt time.Time) []StepTask {
	if decidedAt.IsZero() {
		return tasks
	}
	eligible := make([]StepTask, 0, len(tasks))
	for _, task := range tasks {
		if !task.CreatedAt.IsZero() && task.CreatedAt.Before(decidedAt) {
			continue
		}
		eligible = append(eligible, task)
	}
	return eligible
}
