// Package assetlist owns a project's production lists (#327): the Agent
// proposes one draft list, the person confirms or rejects it, and later steps
// run each item. This package holds the workflow rules only: it prices
// nothing, charges nothing, creates no tasks or sessions and registers no
// project assets. The SQLite adapter persists it.
package assetlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/project"
)

// List statuses. The Schema owns their labels; the server owns these ids.
const (
	StatusDraft      = "draft"
	StatusConfirmed  = "confirmed"
	StatusRejected   = "rejected"
	StatusExpired    = "expired"
	StatusSuperseded = "superseded"
)

// Item states. review means waiting for the person's acceptance; the runner
// (#328) writes working/review/failed/skipped and the board (#329) writes
// accepted/returned.
const (
	StateTodo     = "todo"
	StateWorking  = "working"
	StateReview   = "review"
	StateAccepted = "accepted"
	StateReturned = "returned"
	StateFailed   = "failed"
	StateSkipped  = "skipped"
)

// Decision actions of one person's answer to a draft list.
const (
	DecisionConfirm = "confirm"
	DecisionReject  = "reject"
	DecisionReplace = "replace"
)

// Owner-scoped workflow errors. A missing or foreign list/project is always
// ErrNotFound so transport layers can answer 404 without leaking existence.
var (
	ErrNotFound            = errors.New("assetlist: not found")
	ErrInvalid             = errors.New("assetlist: invalid")
	ErrExpired             = errors.New("assetlist: draft expired")
	ErrConflict            = errors.New("assetlist: version or status conflict")
	ErrItemState           = errors.New("assetlist: item state conflict")
	ErrLimit               = errors.New("assetlist: limit reached")
	ErrProjectNotFound     = errors.New("assetlist: project not found")
	ErrSessionNotInProject = errors.New("assetlist: session is not in the project")
)

// ErrActiveConflict reports that the project already has a confirmed list and
// the person asked to confirm another one without replacing it. Transport
// layers turn it into the "replace the current list" refusal.
type ErrActiveConflict struct {
	ListID string
	Title  string
}

func (e *ErrActiveConflict) Error() string {
	return fmt.Sprintf("assetlist: project already has the confirmed list %q (%s)", e.Title, e.ListID)
}

func (e *ErrActiveConflict) Is(target error) bool {
	_, ok := target.(*ErrActiveConflict)
	return ok
}

// ErrActiveWorking reports that a replace would strand a confirmed list whose
// item is still running. The person must resolve the running item first.
type ErrActiveWorking struct {
	ListID string
}

func (e *ErrActiveWorking) Error() string {
	return fmt.Sprintf("assetlist: the confirmed list %s has items in progress", e.ListID)
}

func (e *ErrActiveWorking) Is(target error) bool {
	_, ok := target.(*ErrActiveWorking)
	return ok
}

// List is one production list of a project. Brief is the shared production
// brief (validated against chat.model3d_usage.brief.fields by the caller); the
// items carry references only, never bytes. EffectiveStatus is derived at read
// time: an unexpired draft whose TTL passed reads as expired without a write.
// PlannedStepCount and PlannedModel3DStepCount are plan values summed over the
// items; the actual step accounting stays in #328's execution grant.
type List struct {
	ID              string          `json:"id"`
	RequestKey      string          `json:"request_key,omitempty"`
	OwnerID         string          `json:"owner_id"`
	ProjectID       string          `json:"project_id"`
	SourceSessionID string          `json:"source_session_id,omitempty"`
	OriginMessageID string          `json:"origin_message_id,omitempty"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	Brief           json.RawMessage `json:"brief"`
	Version         int64           `json:"version"`
	DecidedAt       *time.Time      `json:"decided_at,omitempty"`
	DecidedBy       string          `json:"decided_by,omitempty"`
	DecisionReason  string          `json:"decision_reason,omitempty"`
	SupersededBy    string          `json:"superseded_by,omitempty"`
	ExpiresAt       time.Time       `json:"expires_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	EffectiveStatus string          `json:"effective_status,omitempty"`
	// ItemCount is the number of items; summaries carry it without loading the
	// items themselves.
	ItemCount               int    `json:"item_count"`
	Items                   []Item `json:"items,omitempty"`
	PlannedStepCount        int    `json:"planned_step_count"`
	PlannedModel3DStepCount int    `json:"planned_model3d_step_count"`
	// Run is the derived execution summary of a confirmed list (#328); nil
	// when the list has no run. It is never stored on the list row.
	Run *RunSummary `json:"run,omitempty"`
}

// Item is one asset of a list. Route is the snapshot taken at proposal time:
// each step names the runtime offering, whether it was available then and why
// not. An unavailable step is kept and shown, never silently swapped.
type Item struct {
	ID string `json:"id"`
	// ListID names the list this item belongs to (#328). Sub-session lookups
	// carry it so a completion report can address the item's run without a
	// second list query.
	ListID                  string              `json:"list_id,omitempty"`
	OwnerID                 string              `json:"owner_id"`
	Kind                    string              `json:"kind"`
	Name                    string              `json:"name"`
	Description             string              `json:"description,omitempty"`
	References              []project.Reference `json:"references"`
	RouteID                 string              `json:"route_id"`
	Route                   []RouteStep         `json:"route"`
	PlannedStepCount        int                 `json:"planned_step_count"`
	PlannedModel3DStepCount int                 `json:"planned_model3d_step_count"`
	State                   string              `json:"state"`
	StateReason             string              `json:"state_reason,omitempty"`
	Version                 int64               `json:"version"`
	SessionID               string              `json:"session_id,omitempty"`
	LastTurnMessageID       string              `json:"last_turn_message_id,omitempty"`
	NextActionAt            time.Time           `json:"next_action_at"`
	Block                   string              `json:"block,omitempty"`
	ProjectAssetID          string              `json:"project_asset_id,omitempty"`
	Round                   int                 `json:"round"`
	// ReturnCount counts the returns of this item (max_rework_rounds bounds
	// it). Round counts the item's Agent turns, so the rework limit reads this
	// field and never the round.
	ReturnCount int `json:"return_count"`
	// WorkRound counts the turns of the current work session. A return starts
	// a new one, so max_rounds_per_item bounds each session instead of the
	// item's whole life.
	WorkRound int `json:"work_round"`
	// AcceptedAt is when the person accepted the item (#329b); nil until then.
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	// ReturnReason and ReturnNote are the return context of the latest rework
	// (#329b): the same sub-session's next turn quotes them, and the per-round
	// history stays in the review rows.
	ReturnReason string          `json:"return_reason,omitempty"`
	ReturnNote   string          `json:"return_note,omitempty"`
	Result       json.RawMessage `json:"result"`
	Review       json.RawMessage `json:"review"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// RouteStep is one planned capability of an item, with the runtime offering
// snapshot. Available false means the step cannot run now; Reason explains it.
type RouteStep struct {
	Capability string `json:"capability"`
	OfferingID string `json:"offering_id,omitempty"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
}

// ProposeInput is one Agent proposal. The server supplies owner, project and
// session: the proposal tool has no project_id parameter. RequestKey is the
// stable idempotency identity of the tool call. ItemInput carries references
// already resolved by the caller; this package validates their shape and
// bounds only.
type ProposeInput struct {
	Owner           string          `json:"owner"`
	ProjectID       string          `json:"project_id"`
	SessionID       string          `json:"session_id"`
	OriginMessageID string          `json:"origin_message_id,omitempty"`
	RequestKey      string          `json:"request_key"`
	Title           string          `json:"title"`
	Items           []ItemInput     `json:"items"`
	Brief           json.RawMessage `json:"brief,omitempty"`
}

// ItemInput is one proposed item. An empty RouteID takes the kind's
// default_route from the Schema; an item that carries a crop reference takes
// application.asset_list.decompose.default_route_with_reference instead when
// the Schema declares one (#331). Crop is the optional edit instruction (#331);
// a proposal carrying one is refused.
type ItemInput struct {
	Kind        string              `json:"kind"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	RouteID     string              `json:"route_id,omitempty"`
	References  []project.Reference `json:"references,omitempty"`
	Crop        *CropInstruction    `json:"crop,omitempty"`
}

// EditInput replaces a draft list's title, brief and items. ExpectedVersion is
// the version the browser read; a concurrent edit is ErrConflict.
type EditInput struct {
	Owner           string          `json:"owner"`
	ProjectID       string          `json:"project_id"`
	ListID          string          `json:"list_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Title           string          `json:"title"`
	Items           []ItemInput     `json:"items"`
	Brief           json.RawMessage `json:"brief,omitempty"`
	// SessionID is the proposal session the edit resolves the availability of
	// its steps against. It is not part of the wire contract: the transport
	// layer reads it from the list's source_session_id.
	SessionID string `json:"-"`
}

// DecideInput is one person's confirm, reject or replace answer. Reason is
// stored with the decision (required nowhere by the server).
type DecideInput struct {
	Owner     string `json:"owner"`
	ProjectID string `json:"project_id"`
	ListID    string `json:"list_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
}

// Decision is the repository port's parameter: the service fixes the time and
// the adapter re-reads the list inside its write transaction.
type Decision struct {
	OwnerID   string
	ProjectID string
	ListID    string
	Action    string
	Reason    string
	At        time.Time
}

// Repository is the owner-scoped persistence port. Every read filters the
// owner (and project) and reports a missing or foreign row as ErrNotFound.
// Propose, EditList and Decide run in one write transaction each:
//
//   - Propose first returns an existing list with the same request_key
//     untouched; otherwise it counts the project's live (draft+confirmed)
//     lists and refuses at maximum with ErrLimit, then supersedes the drafts
//     and inserts the new draft with its items. The count runs before the
//     supersede so an over-limit project cannot keep replacing drafts; once
//     old drafts reach a terminal status they stop consuming the quota.
//   - EditList applies the expected_version CAS and replaces all items; only a
//     draft may be edited.
//   - Decide checks the status, TTL, the project's confirmed list and the
//     replace guard atomically: a confirmed list with working items refuses a
//     replace. The called errors are the typed errors of this package.
type Repository interface {
	ProjectExists(ctx context.Context, ownerID, projectID string) (bool, error)
	SessionProject(ctx context.Context, ownerID, sessionID string) (string, error)
	FindByRequestKey(ctx context.Context, ownerID, requestKey string) (List, bool, error)
	ListLists(ctx context.Context, ownerID, projectID string) ([]List, error)
	GetList(ctx context.Context, ownerID, projectID, listID string) (List, error)
	// CountLiveLists counts the project's draft+confirmed lists; terminal
	// lists do not consume the project quota. Propose re-checks the same query
	// inside its write transaction, so callers use this for display only.
	CountLiveLists(ctx context.Context, ownerID, projectID string) (int, error)
	// Propose stores one draft. maximum <= 0 means unlimited.
	Propose(ctx context.Context, list List, items []Item, maximum int) (List, error)
	EditList(ctx context.Context, list List, items []Item, expectedVersion int64) (List, error)
	Decide(ctx context.Context, decision Decision) (List, error)
	// DecideItems applies one batch of acceptance decisions in one write
	// transaction (#329b): the list_version CAS gates the whole batch, every
	// item must still be awaiting review, the accepted items' project assets
	// are inserted last, and a return reopens a completed run. A stale version
	// is ErrConflict and an item in the wrong state is ErrItemState, with no
	// partial write; an asset that no longer fits the bound is
	// ProjectAssetBoundError with the same rollback.
	DecideItems(ctx context.Context, batch DecisionBatch) (List, error)
}

// DecisionBatch is one person's accept-or-return submission of the board
// (#329b). ExpectedVersion is the list version the browser read; Items holds
// one decision per item (accept may carry several, return exactly one).
// Assets are the project asset rows the accepted items register and
// AssetMaximum is the project's asset bound: the repository inserts both in
// the same transaction, so a refused version CAS leaves no orphan asset.
type DecisionBatch struct {
	OwnerID         string
	ProjectID       string
	ListID          string
	ExpectedVersion int64
	ReviewerID      string
	At              time.Time
	Items           []ItemDecision
	Assets          []project.Asset
	AssetMaximum    int
}

// ItemDecision is one item's write. ReviewID is the new review row id;
// ProjectAssetID is the idempotent project asset an accept registered;
// Reasons and Note are the return context.
type ItemDecision struct {
	ReviewID       string
	ItemID         string
	Verdict        string
	ProjectAssetID string
	Reasons        []string
	Reason         string
	Note           string
}
