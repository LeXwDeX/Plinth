package assetlist

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Run control actions of the person's browser (#328).
const (
	RunActionStart  = "start"
	RunActionPause  = "pause"
	RunActionResume = "resume"
	RunActionStop   = "stop"
)

// ControlRun applies one person-requested action to the owner's list run
// (#328). The action mapping is fixed: start moves idle to running, pause
// moves running (or an automatic presence pause) to paused(user), resume moves
// any pause back to running without re-checking the step cap the person is
// expected to have raised, and stop makes the run terminal. Repeating the
// target status returns the current run (a double click writes once); any
// other state is ErrConflict. expectedVersion is the run's opaque updated_at
// token read from RunSummary.Version; it is the CAS guard of the write, so a
// stale token is ErrConflict. A missing run, list or project is ErrNotFound.
//
// reason is part of the browser contract for later decision trails; the run
// schema stores only the pause reason enum, so it is not persisted.
func (service *Service) ControlRun(
	ctx context.Context,
	ownerID, projectID, listID, action, expectedVersion, reason string,
) (Run, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" {
		return Run{}, ErrNotFound
	}
	if service.runs == nil {
		return Run{}, ErrRunUnavailable
	}
	action = strings.TrimSpace(action)
	switch action {
	case RunActionStart, RunActionPause, RunActionResume, RunActionStop:
	default:
		return Run{}, fmt.Errorf("%w: run action %q", ErrInvalid, action)
	}
	expectedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(expectedVersion))
	if err != nil {
		return Run{}, fmt.Errorf("%w: expected_version", ErrInvalid)
	}
	current, err := service.runs.GetRunByList(ctx, ownerID, projectID, listID)
	if err != nil {
		return Run{}, err
	}
	to, pauseReason, err := controlRunTarget(current, action)
	if err != nil {
		return Run{}, err
	}
	return service.runs.TransitionRun(ctx, ownerID, listID, current.Status, to,
		pauseReason, expectedAt, service.clock())
}

// controlRunTarget is the person's side of the run state machine. A target
// equal to the current status is idempotent; only the automatic presence pause
// is upgraded by a manual pause, because the person asking to pause makes the
// pause sticky. Everything else keeps the runner's transition rules.
func controlRunTarget(current Run, action string) (string, string, error) {
	switch action {
	case RunActionStart:
		switch current.Status {
		case RunIdle, RunRunning:
			return RunRunning, PauseNone, nil
		}
	case RunActionPause:
		switch current.Status {
		case RunRunning:
			return RunPaused, PauseUser, nil
		case RunPaused:
			if current.PauseReason == PausePresence {
				return RunPaused, PauseUser, nil
			}
			return RunPaused, current.PauseReason, nil
		}
	case RunActionResume:
		switch current.Status {
		case RunPaused, RunRunning:
			return RunRunning, PauseNone, nil
		}
	case RunActionStop:
		switch current.Status {
		case RunIdle, RunRunning, RunPaused, RunStopped:
			return RunStopped, PauseNone, nil
		}
	}
	return "", "", ErrConflict
}

// SkipItem marks one of the owner's list items skipped (#328) with the
// person's reason. Only todo and working items move to skipped; review,
// accepted, returned and failed are refused with ErrItemState, and an already
// skipped item is returned untouched. A stale expectedVersion is ErrConflict;
// VersionUnchecked skips the item CAS. The list must belong to the project
// (ErrNotFound otherwise).
//
// Skipping never cancels the item's bound Tasks: the in-flight turn still
// writes its result through the normal completion path, and the runner sees a
// non-working item on its next tick and stops continuing it.
func (service *Service) SkipItem(
	ctx context.Context,
	ownerID, projectID, listID, itemID string,
	expectedVersion int64,
	reason string,
) (Item, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	itemID = strings.TrimSpace(itemID)
	if ownerID == "" || projectID == "" || listID == "" || itemID == "" {
		return Item{}, ErrNotFound
	}
	if service.runs == nil {
		return Item{}, ErrRunUnavailable
	}
	if expectedVersion < 0 && expectedVersion != VersionUnchecked {
		return Item{}, fmt.Errorf("%w: expected_version", ErrInvalid)
	}
	if _, err := service.repository.GetList(ctx, ownerID, projectID, listID); err != nil {
		return Item{}, err
	}
	return service.runs.SkipItem(ctx, ownerID, listID, itemID, strings.TrimSpace(reason),
		expectedVersion, service.clock())
}

// RunSummaryByList reads one list's run state and item counts (#328). A list
// without a run, or one outside the owner/project, is ErrNotFound.
func (service *Service) RunSummaryByList(
	ctx context.Context,
	ownerID, projectID, listID string,
) (RunSummary, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" {
		return RunSummary{}, ErrNotFound
	}
	if service.runs == nil {
		return RunSummary{}, ErrRunUnavailable
	}
	summaries, err := service.runs.RunSummaries(ctx, ownerID, projectID)
	if err != nil {
		return RunSummary{}, err
	}
	summary, found := summaries[listID]
	if !found {
		return RunSummary{}, ErrNotFound
	}
	return summary, nil
}

// runSummaries reads the project's run summaries for display. A deployment
// without the run store has none, so the wire field stays null.
func (service *Service) runSummaries(
	ctx context.Context,
	ownerID, projectID string,
) (map[string]RunSummary, error) {
	if service.runs == nil {
		return nil, nil
	}
	return service.runs.RunSummaries(ctx, ownerID, projectID)
}
