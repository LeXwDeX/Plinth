package generation

import (
	"context"
	"errors"
	"fmt"
)

// ErrSubmissionBlocked identifies a new Task refused by the injected
// SubmissionGate (#328 §2.5.5, kernel layer). It is a retryable refusal: the
// approval, its grant and the frozen input stay intact, and the same submit
// may run again after the production list resumes.
var ErrSubmissionBlocked = errors.New("generation: 新的付费任务被生产清单暂停状态拒绝")

// SubmissionBlockedError is the concrete kernel refusal. Reason is the gate's
// pause reason (presence, user, step_cap, failure, stopped, ...); the kernel
// treats it as opaque and the app maps it to Catalog copy. Cause keeps a gate
// read error so the caller can still tell why the state could not be read.
type SubmissionBlockedError struct {
	Reason string
	Cause  error
}

func (e *SubmissionBlockedError) Error() string {
	if e.Reason == "" {
		return ErrSubmissionBlocked.Error()
	}
	return fmt.Sprintf("%s（%s）", ErrSubmissionBlocked.Error(), e.Reason)
}

func (e *SubmissionBlockedError) Is(target error) bool { return target == ErrSubmissionBlocked }

func (e *SubmissionBlockedError) Unwrap() error { return e.Cause }

// SetSubmissionGate installs the kernel paid-submission gate. It is assembly
// wiring and must run before the Module starts serving tasks; nil disables the
// gate. The gate is consulted only for a Task that would be newly created.
func (m *Module) SetSubmissionGate(gate func(ctx context.Context, ownerID, sessionID string) error) {
	m.submissionGate = gate
}

// admitSubmissionGate applies the kernel paid-submission gate to the Task about
// to be inserted. An already committed Task for the same owner and idempotency
// key is a replay and must pass untouched, including while the list is paused:
// recovery reconciliation depends on it. A key still held by a tombstone is
// refused by the repository insert itself, so the gate need not decide.
func (m *Module) admitSubmissionGate(ctx context.Context, task Task) error {
	if m.submissionGate == nil {
		return nil
	}
	if task.IdempotencyKey != "" {
		_, err := m.repository.GetByIdempotency(ctx, task.OwnerID, task.IdempotencyKey)
		switch {
		case err == nil:
			// The repository insert below returns this committed Task as a replay.
			return nil
		case errors.Is(err, RepositoryNotFound):
			// No Task owns the key: this is a new admission.
		case errors.Is(err, RepositoryConflict):
			// A tombstoned Task holds the key; no new Task can be created.
			return nil
		default:
			// A read failure is a refusal: guessing that submission is safe
			// could take a paid step during a pause whose state is unreadable.
			return mapRepositoryError("submit", err)
		}
	}
	if err := m.submissionGate(ctx, string(task.OwnerID), task.SessionID); err != nil {
		var blocked *SubmissionBlockedError
		if errors.As(err, &blocked) {
			return err
		}
		return &SubmissionBlockedError{Cause: err}
	}
	return nil
}
