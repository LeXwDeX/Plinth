package assetlist

import (
	"context"
	"errors"
	"strings"
)

// SubmissionDecision is one first-layer paid-gate answer for an item's
// sub-session (#328). Reason is meaningful only when Allowed is false and is
// one of the pause reasons (presence, user, step_cap, failure) or stopped.
type SubmissionDecision struct {
	Allowed bool
	Reason  string
}

// SubmissionAllowed decides whether a new paid submission from sessionID may
// proceed while automatic production owns it (#328). It is the reusable
// "session → item → run → presence" read shared by the tool Preflight and
// PR5's kernel fallback; callers must not duplicate the rules.
//
// The answer is permissive where no automatic production exists: an ordinary
// session (no item), or an item whose run was never created (a deployment
// without the runner), is allowed. Once an item is known, a non-working item
// is stopped; a run that is not running reports its pause reason (or stopped
// for idle/completed); a running run additionally requires an unexpired
// project heartbeat. Read errors are returned untouched: the caller must treat
// them as a refusal rather than guess that submission is safe.
//
// This gate only refuses new submissions. An existing Task, an already
// prepared operation and a bound grant are replayed by their own idempotency
// paths and are not cancelled here.
func (service *Service) SubmissionAllowed(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (SubmissionDecision, error) {
	ownerID = strings.TrimSpace(ownerID)
	sessionID = strings.TrimSpace(sessionID)
	item, found, err := service.ItemBySession(ctx, ownerID, sessionID)
	if err != nil {
		return SubmissionDecision{}, err
	}
	if !found {
		return SubmissionDecision{Allowed: true}, nil
	}
	if item.State != StateWorking {
		return SubmissionDecision{Reason: RunStopped}, nil
	}
	run, err := service.runs.GetRunByListID(ctx, ownerID, item.ListID)
	if errors.Is(err, ErrNotFound) {
		return SubmissionDecision{Allowed: true}, nil
	}
	if err != nil {
		return SubmissionDecision{}, err
	}
	switch run.Status {
	case RunRunning:
	case RunPaused:
		return SubmissionDecision{Reason: submissionPauseReason(run.PauseReason)}, nil
	default:
		// idle waits for the owner's other list; stopped and completed are
		// terminal. None of them takes a new paid step.
		return SubmissionDecision{Reason: RunStopped}, nil
	}
	// A deployment without presence limits keeps the old behaviour: the run
	// alone gates submissions.
	if service.presence != nil && service.presenceGrace > 0 {
		since := service.clock().Add(-service.presenceGrace)
		active, err := service.presence.Active(ctx, ownerID, run.ProjectID, since)
		if err != nil {
			return SubmissionDecision{}, err
		}
		if !active {
			return SubmissionDecision{Reason: PausePresence}, nil
		}
	}
	return SubmissionDecision{Allowed: true}, nil
}

// submissionPauseReason narrows a stored pause reason to the ones the gate can
// report. A paused run without a valid reason stays sticky like user.
func submissionPauseReason(reason string) string {
	switch reason {
	case PauseUser, PausePresence, PauseStepCap, PauseFailure:
		return reason
	default:
		return PauseUser
	}
}
