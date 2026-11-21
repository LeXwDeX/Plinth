package assetlist

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Run errors (#328). A deployment without the run store cannot create runs;
// a Catalog that declares execution but disables it keeps lists manual.
var (
	ErrRunUnavailable    = errors.New("assetlist: run store unavailable")
	ErrExecutionDisabled = errors.New("assetlist: automatic execution is disabled")
)

// Run statuses (#328). One list has at most one run; only a running run claims
// items and opens turns. idle waits for the person to start it because the
// owner already has another running list; stopped and completed are terminal.
const (
	RunIdle      = "idle"
	RunRunning   = "running"
	RunPaused    = "paused"
	RunStopped   = "stopped"
	RunCompleted = "completed"
)

// Run pause reasons. user is sticky until an explicit resume; presence clears
// with a fresh heartbeat; step_cap holds while every remaining item waits at
// the 3D step cap and no other item can progress (#330 §6); failure hands the
// decision to the person.
const (
	PauseNone     = ""
	PauseUser     = "user"
	PausePresence = "presence"
	PauseStepCap  = "step_cap"
	PauseFailure  = "failure"
)

// Item block values are the second axis of an item and never change its
// state. awaiting_approval and step_cap release the parallel slot: the person
// resolves them later and the scheduler keeps claiming other items. step_cap
// is the "3D steps used up" marker of one item; it pauses the run only while
// no other item can progress. paused stays reserved for a later step.
const (
	BlockNone             = ""
	BlockAwaitingApproval = "awaiting_approval"
	BlockPaused           = "paused"
	BlockStepCap          = "step_cap"
)

// VersionUnchecked tells an item write to skip the version CAS. Stored
// versions are never negative.
const VersionUnchecked int64 = -1

// Item control actions of the person's browser (#328). Only skip exists: the
// other item transitions belong to the runner and the completion tool.
const ItemActionSkip = "skip"

// RunSummary is the read-time execution summary of one list (#328): the run
// state plus the item counts of the list panel. It is derived from the run row
// and the item table, never stored. Version is the run's updated_at as an
// opaque RFC3339Nano token; a run has no version column, so every control
// write uses that instant as its CAS token.
type RunSummary struct {
	Status      string     `json:"status"`
	PauseReason string     `json:"pause_reason,omitempty"`
	Version     string     `json:"version"`
	ItemCounts  ItemCounts `json:"item_counts"`
}

// ItemCounts counts a list's items per state. skipped is terminal like
// accepted and failed; review and returned still wait for a person.
type ItemCounts struct {
	Todo     int `json:"todo"`
	Working  int `json:"working"`
	Review   int `json:"review"`
	Accepted int `json:"accepted"`
	Returned int `json:"returned"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
}

// Add counts n items in state. A state outside the documented ones is not
// counted, so the counts sum to at most the item total.
func (counts *ItemCounts) Add(state string, n int) {
	switch state {
	case StateTodo:
		counts.Todo += n
	case StateWorking:
		counts.Working += n
	case StateReview:
		counts.Review += n
	case StateAccepted:
		counts.Accepted += n
	case StateReturned:
		counts.Returned += n
	case StateFailed:
		counts.Failed += n
	case StateSkipped:
		counts.Skipped += n
	}
}

// Run is the execution state of one confirmed list. It carries no list
// content: the runner reads the list and its items from the list repository.
// PauseReason is empty unless Status is RunPaused.
type Run struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id"`
	ProjectID   string    `json:"project_id"`
	ListID      string    `json:"list_id"`
	Status      string    `json:"status"`
	PauseReason string    `json:"pause_reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Waiting reports that the run waits for the owner's other running list.
func (run Run) Waiting() bool { return run.Status == RunIdle }

// Active reports that the run claims items and opens turns.
func (run Run) Active() bool { return run.Status == RunRunning }

// EnsureRun stores the confirmed list's run exactly once (#328). Repeating the
// call returns the stored run untouched, like CreateRun. The first run starts
// running unless the owner already has another running list, in which case it
// starts idle and waits for the person to start it. A deployment without the
// run store, or with execution disabled, creates nothing.
func (service *Service) EnsureRun(
	ctx context.Context,
	ownerID, projectID, listID string,
) (Run, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if service.runs == nil {
		return Run{}, ErrRunUnavailable
	}
	if !service.executionEnabled {
		return Run{}, ErrExecutionDisabled
	}
	if ownerID == "" || projectID == "" || listID == "" {
		return Run{}, ErrNotFound
	}
	existing, err := service.runs.GetRunByList(ctx, ownerID, projectID, listID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Run{}, err
	}
	status := RunRunning
	running, err := service.runs.RunsByStatus(ctx, RunRunning)
	if err != nil {
		return Run{}, err
	}
	for _, other := range running {
		if other.OwnerID == ownerID {
			status = RunIdle
			break
		}
	}
	id, err := service.newID("run_")
	if err != nil {
		return Run{}, err
	}
	now := service.clock()
	return service.runs.CreateRun(ctx, Run{
		ID:        id,
		OwnerID:   ownerID,
		ProjectID: projectID,
		ListID:    listID,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// SetItemSession binds one item to its sub-session (#328), bumping the item
// version. expectedVersion is the version the caller read; VersionUnchecked
// skips the CAS. Chat calls this while creating the item's session, so it does
// not have to take the whole run port.
func (service *Service) SetItemSession(
	ctx context.Context,
	ownerID, listID, itemID, sessionID string,
	expectedVersion int64,
) (Item, error) {
	if service.runs == nil {
		return Item{}, ErrRunUnavailable
	}
	return service.runs.SetItemSession(ctx, ownerID, listID, itemID, sessionID,
		expectedVersion, service.clock())
}

// FinishItemInput is one terminal item transition. FinishItem only moves a
// working item to review, failed or skipped; StateReason, Result and Review
// are stored as given (empty JSON becomes {}). A non-zero NextActionAt is the
// earliest next scheduling instant; the zero time stores the empty marker.
type FinishItemInput struct {
	OwnerID      string
	ListID       string
	ItemID       string
	State        string
	StateReason  string
	Result       json.RawMessage
	Review       json.RawMessage
	NextActionAt time.Time
	At           time.Time
}

// RunStore is the run and item execution port of one owner's lists. Every
// read filters the owner (and the list) and reports a missing or foreign row
// as ErrNotFound. State-changing methods run in one BEGIN IMMEDIATE
// transaction with a CAS guard:
//
//   - UpdateRunStatus applies the expected from status and the Go-layer
//     transition rules (idle→running, running↔paused, →stopped,
//     running→completed). Repeating the target status returns the current run.
//   - TransitionRun is the person's control write: the same transition rules
//     plus a CAS on the run's updated_at token. Repeating the target status is
//     idempotent; a presence pause may upgrade to the sticky user pause.
//   - ClaimItem picks the first returned or todo item by position only while
//     the run is running, then moves it to working. A returned item keeps its
//     round (the return already counted it, #329b); a todo item gets round+1.
//     Both start a work session at work_round 1 and bump the version.
//   - FinishItem moves working→review|failed|skipped only.
//   - SkipItem moves todo|working→skipped under the item version CAS and never
//     touches the item's session or its bound Tasks.
//   - RunSummaries reads every run of a project with its per-state item
//     counts for the list panel.
//   - SetItemBlock, SetItemSession, SetItemNextActionAt and RecordItemTurn
//     update one item and bump its version. A non-negative expectedVersion is
//     a CAS guard; VersionUnchecked skips it.
//
// A lost CAS guard reports ErrConflict; an item in the wrong state reports
// ErrItemState.
type RunStore interface {
	// CreateRun stores the list's run. A list's second CreateRun returns the
	// existing run untouched, like Decide's repeated decision.
	CreateRun(ctx context.Context, run Run) (Run, error)
	GetRunByList(ctx context.Context, ownerID, projectID, listID string) (Run, error)
	// GetRunByListID reads the run of one list when only the item's list is
	// known (the submission gate). list_id is unique, so the project filter is
	// unnecessary; the run carries its ProjectID for the presence check.
	GetRunByListID(ctx context.Context, ownerID, listID string) (Run, error)
	// RunsByStatus scans runs of every owner for the runner's tick.
	RunsByStatus(ctx context.Context, status string) ([]Run, error)
	UpdateRunStatus(ctx context.Context, ownerID, listID, from, to, pauseReason string, at time.Time) (Run, error)
	// TransitionRun applies one person-requested transition under a CAS on
	// expectedUpdatedAt, the run's updated_at version token. A current status
	// already equal to to returns the current run untouched, except that a
	// presence pause upgrades to the sticky user pause. A stale token, a
	// status mismatch or an illegal transition is ErrConflict.
	TransitionRun(ctx context.Context, ownerID, listID, from, to, pauseReason string, expectedUpdatedAt, at time.Time) (Run, error)
	// RunSummaries reads every run of the owner's project with its per-state
	// item counts, keyed by list id. A list without a run is absent.
	RunSummaries(ctx context.Context, ownerID, projectID string) (map[string]RunSummary, error)
	// ClaimItem reports false when nothing is claimable (no todo item or the
	// run is not running); a lost race is ErrConflict.
	ClaimItem(ctx context.Context, ownerID, listID string, at time.Time) (Item, bool, error)
	FinishItem(ctx context.Context, input FinishItemInput) (Item, error)
	// SkipItem marks one todo or working item skipped and stores reason as its
	// state_reason. An already skipped item returns the current row; any other
	// state is ErrItemState. A non-negative expectedVersion is the item CAS
	// guard, VersionUnchecked skips it. It never cancels a bound Task.
	SkipItem(ctx context.Context, ownerID, listID, itemID, reason string, expectedVersion int64, at time.Time) (Item, error)
	SetItemBlock(ctx context.Context, ownerID, listID, itemID, block string, expectedVersion int64, at time.Time) (Item, error)
	// SetItemStepCapTurn records the turn the runner blocked at the 3D step
	// cap (#330 step 5C). The item's next advance past that turn opens a fresh
	// turn instead of re-evaluating the capped one. It bumps the item version.
	SetItemStepCapTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, expectedVersion int64, at time.Time) (Item, error)
	SetItemSession(ctx context.Context, ownerID, listID, itemID, sessionID string, expectedVersion int64, at time.Time) (Item, error)
	SetItemNextActionAt(ctx context.Context, ownerID, listID, itemID string, nextActionAt time.Time, expectedVersion int64, at time.Time) (Item, error)
	// RecordItemTurn stores the assistant message of a turn the server opened
	// for a working item and sets round (the item's whole life) and workRound
	// (the current work session). The three change together so a restart can
	// tell a scheduled turn (the stored message id matches) from one that only
	// has a queued turn row. Only a working item is updated; a different state
	// is ErrItemState.
	RecordItemTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, round, workRound int, expectedVersion int64, at time.Time) (Item, error)
	ItemByID(ctx context.Context, ownerID, listID, itemID string) (Item, error)
	ItemsByList(ctx context.Context, ownerID, listID string) ([]Item, error)
	// ItemBySession finds the item whose sub-session is sessionID (#328). An
	// unknown or foreign session reports false without an error: it is an
	// ordinary session, not a broken read.
	ItemBySession(ctx context.Context, ownerID, sessionID string) (Item, bool, error)
}

// PresenceStore is the per-tab heartbeat of a project. Upsert is idempotent
// per (owner, project, tab); Leave removes that tab only; Active reports any
// unexpired heartbeat at or after since; Prune drops every row last seen
// before the instant. Expired rows are never kept as history.
type PresenceStore interface {
	Upsert(ctx context.Context, ownerID, projectID, tabID string, at time.Time) error
	Leave(ctx context.Context, ownerID, projectID, tabID string) error
	Active(ctx context.Context, ownerID, projectID string, since time.Time) (bool, error)
	Prune(ctx context.Context, before time.Time) (int64, error)
}
