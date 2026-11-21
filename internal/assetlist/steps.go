package assetlist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The "give it a few more steps" write of one list item (#330 step 5C). The
// grant lives on the asset (the item's sub-session), not on the item row: the
// used steps and the grant share the session's lifecycle, so a rework of the
// same item keeps both, and an external MCP asset is extended through the same
// storage.
var (
	// ErrStepUnavailable reports an item whose sub-session does not exist yet:
	// there is no asset to extend before the runner starts the item.
	ErrStepUnavailable = errors.New("assetlist: item has no asset to extend")
	// ErrStepExtraLimit reports a target above the Schema's total grant bound;
	// ErrStepExtraSingleLimit one write that adds more than the per-write bound.
	ErrStepExtraLimit       = errors.New("assetlist: extra step total limit reached")
	ErrStepExtraSingleLimit = errors.New("assetlist: extra step single limit reached")
)

// StepExtraLimitError is the refused total grant: the declared bound and the
// target the request asked for. The transport fills the Schema copy with them.
type StepExtraLimitError struct {
	Maximum   int
	Requested int
}

func (e *StepExtraLimitError) Error() string {
	return fmt.Sprintf("assetlist: extra steps %d above the %d bound", e.Requested, e.Maximum)
}

func (e *StepExtraLimitError) Is(target error) bool { return target == ErrStepExtraLimit }

// StepExtraSingleLimitError is one write that adds more than the per-write
// bound in a single call.
type StepExtraSingleLimitError struct {
	Maximum int
}

func (e *StepExtraSingleLimitError) Error() string {
	return fmt.Sprintf("assetlist: one extra-step write above the %d bound", e.Maximum)
}

func (e *StepExtraSingleLimitError) Is(target error) bool { return target == ErrStepExtraSingleLimit }

// StepState is one asset's live 3D step standing. Cap and Extra appear only in
// the cap mode, where Cap+Extra is the item's limit; Used is read in every
// mode. It never carries a guessed zero: a failed read is no state at all.
type StepState struct {
	Mode  string `json:"mode"`
	Used  int    `json:"used"`
	Cap   int    `json:"cap,omitempty"`
	Extra int    `json:"extra,omitempty"`
}

// StepChange is one extra-step write's before and after, for the audit trail.
type StepChange struct {
	Previous StepState
	Current  StepState
}

// StepsConfig installs the extra-step write of a list item (#330 step 5C).
// Set stores the asset's whole grant and answers the before/after standing;
// Maximum is the Schema's per-write bound and TotalMaximum its per-asset
// bound. nil keeps SetItemSteps refusing.
type StepsConfig struct {
	Set          func(ctx context.Context, ownerID, sessionID string, target int) (StepChange, error)
	Maximum      int
	TotalMaximum int
}

// SetItemSteps gives one item's asset a few more 3D steps (#330 step 5C).
// target is the asset's whole grant, not an increment, so a retried write after
// a lost answer cannot add twice; the item version the browser read gates the
// write. The item's step-cap block is cleared in the same call, and a run
// paused at the cap continues on the runner's next tick. Only a real browser
// session reaches this method; the transport enforces that.
//
// A stale version is ErrConflict; an unknown item or list is ErrNotFound; an
// item without a sub-session is ErrStepUnavailable; a target above the
// declared bound is ErrStepExtraLimit. A target below the stored grant is
// ErrConflict: another tab already raised it, and the browser should re-read
// rather than lower it by accident.
func (service *Service) SetItemSteps(
	ctx context.Context,
	ownerID, projectID, listID, itemID string,
	target int, expectedVersion int64,
) (Item, StepChange, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	itemID = strings.TrimSpace(itemID)
	if ownerID == "" || projectID == "" || listID == "" || itemID == "" {
		return Item{}, StepChange{}, ErrNotFound
	}
	if expectedVersion < 0 || target < 0 {
		return Item{}, StepChange{}, fmt.Errorf("%w: extra steps", ErrInvalid)
	}
	if service.steps == nil || service.runs == nil {
		return Item{}, StepChange{}, ErrRunUnavailable
	}
	if service.steps.TotalMaximum > 0 && target > service.steps.TotalMaximum {
		return Item{}, StepChange{}, &StepExtraLimitError{
			Maximum: service.steps.TotalMaximum, Requested: target,
		}
	}
	if _, err := service.repository.GetList(ctx, ownerID, projectID, listID); err != nil {
		return Item{}, StepChange{}, err
	}
	// The execution row carries the live session, block and version; the list
	// repository above only proves the list is the owner's in the project.
	item, err := service.runs.ItemByID(ctx, ownerID, listID, itemID)
	if err != nil {
		return Item{}, StepChange{}, err
	}
	if item.Version != expectedVersion {
		return Item{}, StepChange{}, ErrConflict
	}
	if item.SessionID == "" {
		return Item{}, StepChange{}, ErrStepUnavailable
	}
	change, err := service.steps.Set(ctx, ownerID, item.SessionID, target)
	if err != nil {
		return Item{}, StepChange{}, err
	}
	// The write also anchors the item version, so a second tab's stale target
	// is refused even when the item had no block to clear.
	block := item.Block
	if block == BlockStepCap {
		block = BlockNone
	}
	now := service.clock()
	updated, err := service.runs.SetItemBlock(ctx, ownerID, listID, itemID, block, expectedVersion, now)
	if err != nil {
		return Item{}, StepChange{}, err
	}
	if err := service.resumeStepCapRun(ctx, ownerID, listID, now); err != nil {
		return Item{}, StepChange{}, err
	}
	return updated, change, nil
}

// resumeStepCapRun resumes a run the runner paused at the cap. Only the step
// cap pause is touched: a person's pause stays until they continue, and other
// automatic pauses keep their own rules.
func (service *Service) resumeStepCapRun(ctx context.Context, ownerID, listID string, now time.Time) error {
	run, err := service.runs.GetRunByListID(ctx, ownerID, listID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if run.Status != RunPaused || run.PauseReason != PauseStepCap {
		return nil
	}
	if _, err := service.runs.UpdateRunStatus(ctx, ownerID, listID,
		RunPaused, RunRunning, PauseNone, now); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	return nil
}
