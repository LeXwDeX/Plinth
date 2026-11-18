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
// with a fresh heartbeat; step_cap needs the blocking item resolved first and
// failure hands the decision to the person.
const (
	PauseNone     = ""
	PauseUser     = "user"
	PausePresence = "presence"
	PauseStepCap  = "step_cap"
	PauseFailure  = "failure"
)

// Item block values are the second axis of an item and never change its
// state. awaiting_approval releases the parallel slot; step_cap pauses the
// run; paused stays reserved for a later step.
const (
	BlockNone             = ""
	BlockAwaitingApproval = "awaiting_approval"
	BlockPaused           = "paused"
	BlockStepCap          = "step_cap"
)

// VersionUnchecked tells an item write to skip the version CAS. Stored
// versions are never negative.
const VersionUnchecked int64 = -1

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
//   - ClaimItem picks the first todo item by position only while the run is
//     running, then moves it todo→working with round+1 and version+1.
//   - FinishItem moves working→review|failed|skipped only.
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
	// ClaimItem reports false when nothing is claimable (no todo item or the
	// run is not running); a lost race is ErrConflict.
	ClaimItem(ctx context.Context, ownerID, listID string, at time.Time) (Item, bool, error)
	FinishItem(ctx context.Context, input FinishItemInput) (Item, error)
	SetItemBlock(ctx context.Context, ownerID, listID, itemID, block string, expectedVersion int64, at time.Time) (Item, error)
	SetItemSession(ctx context.Context, ownerID, listID, itemID, sessionID string, expectedVersion int64, at time.Time) (Item, error)
	SetItemNextActionAt(ctx context.Context, ownerID, listID, itemID string, nextActionAt time.Time, expectedVersion int64, at time.Time) (Item, error)
	// RecordItemTurn stores the assistant message of a turn the server opened
	// for a working item and sets round to the number of started rounds. The
	// two change together so a restart can tell a scheduled turn (the stored
	// message id matches) from one that only has a queued turn row. Only a
	// working item is updated; a different state is ErrItemState.
	RecordItemTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, round int, expectedVersion int64, at time.Time) (Item, error)
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
