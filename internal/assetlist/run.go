package assetlist

import (
	"context"
	"encoding/json"
	"time"
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
//   - SetItemBlock, SetItemSession and SetItemNextActionAt update one item and
//     bump its version. A non-negative expectedVersion is a CAS guard;
//     VersionUnchecked skips it.
//
// A lost CAS guard reports ErrConflict; an item in the wrong state reports
// ErrItemState.
type RunStore interface {
	// CreateRun stores the list's run. A list's second CreateRun returns the
	// existing run untouched, like Decide's repeated decision.
	CreateRun(ctx context.Context, run Run) (Run, error)
	GetRunByList(ctx context.Context, ownerID, projectID, listID string) (Run, error)
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
	ItemByID(ctx context.Context, ownerID, listID, itemID string) (Item, error)
	ItemsByList(ctx context.Context, ownerID, listID string) ([]Item, error)
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
