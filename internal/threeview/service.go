// Package threeview turns the runs of the three-view recipe into groups: one
// card with three views (slots) that the person starts one at a time, and one
// version in the version strip.
//
// It adds no new execution path. Every view is a step of a SkillRun and is
// started through the same checkpoints as any paid step: PrepareStep freezes
// its input, an ExecutionGrant is requested and approved by the session
// approval mode (or waits for a person), and SubmitStep creates the one Task.
// The group is a projection of those records and of the live Task state; the
// package keeps no state of its own.
//
// Continuing is explicit. The first view is started by the Agent tool; the
// other two start only when the person asks for them, one request each, so no
// credits are spent unattended and no background worker is needed. Starting is
// idempotent, because the step's identity is fixed by the run and the view. A
// view whose attempt ended without a result is retried on its own as the next
// attempt of the same step, with its own identity; the views that finished are
// not touched.
package threeview

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

var (
	// ErrNotFound: the session, run or view is not the caller's or does not exist.
	ErrNotFound = errors.New("threeview: not found")
	// ErrConflict: the view cannot take this action now (it is already
	// running or done, or its failure may not be retried).
	ErrConflict = errors.New("threeview: view cannot take this action")
	// ErrInvalid: the request or the wiring is incomplete.
	ErrInvalid = errors.New("threeview: invalid request")
)

// Status is where one view stands. It is derived from the SkillRun ledger, the
// view's ExecutionGrant and its Task, which stay the only authorities.
type Status string

const (
	NotStarted       Status = "not_started"
	AwaitingApproval Status = "awaiting_approval"
	Denied           Status = "denied"
	Starting         Status = "starting"
	Running          Status = "running"
	Succeeded        Status = "succeeded"
	Failed           Status = "failed"
	Canceled         Status = "canceled"
)

// Action is what the person asks of one view.
type Action string

const (
	// Start continues the group with this view. Repeating it changes nothing.
	Start Action = "start"
	// Retry starts the next attempt of a view whose attempt ended without a
	// result. It names the attempt the person saw, so a repeat is harmless.
	Retry Action = "retry"
)

// Slot is one view of a group.
type Slot struct {
	PriceSource string                  `json:"price_source,omitempty"`
	NativeQuote *generation.NativeQuote `json:"native_quote,omitempty"`
	Key         string                  `json:"key"`
	Label       string                  `json:"label"`
	Status      Status                  `json:"status"`
	Attempt     int                     `json:"attempt"`
	// TaskID is the Task of the current attempt, once it exists.
	TaskID string `json:"task_id,omitempty"`
	// OutputAssetID is the finished picture of the Task.
	OutputAssetID string `json:"output_asset_id,omitempty"`
	// RequestID is the approval the view waits for.
	RequestID string `json:"request_id,omitempty"`
	// Startable is true when "start" would begin or continue this view.
	Startable bool `json:"startable"`
	// Retryable is true when "retry" would start a new attempt.
	Retryable bool `json:"retryable"`
	// EstimatedCredits is what one view of the group costs: read from a view
	// that has a Task, since every view runs the same Offering. It is shown on
	// the button that would start or retry a view. Zero when no view has
	// started yet.
	EstimatedCredits int `json:"estimated_credits,omitempty"`
	// CreditsMetered is the Offering's own declaration that its provider bills
	// in credits; the UI shows a cost only then.
	CreditsMetered bool `json:"credits_metered,omitempty"`
}

// ModelSource says which view a 3D model made from the group uses.
type ModelSource struct {
	Slot string `json:"slot"`
	// Available is true once that view finished.
	Available bool   `json:"available"`
	TaskID    string `json:"task_id,omitempty"`
	AssetID   string `json:"asset_id,omitempty"`
	// Note is the Schema's statement that only this view is used.
	Note string `json:"note"`
}

// Group is the projection of one three-view run.
type Group struct {
	RunID         string      `json:"run_id"`
	Kind          string      `json:"kind"`
	Label         string      `json:"label"`
	RecipeID      string      `json:"recipe_id"`
	SourceAssetID string      `json:"source_asset_id"`
	CreatedAt     time.Time   `json:"created_at"`
	Complete      bool        `json:"complete"`
	Slots         []Slot      `json:"slots"`
	ModelSource   ModelSource `json:"model_source"`
}

// Sessions proves a session belongs to its owner.
type Sessions interface {
	OwnsSession(ctx context.Context, owner, session string) (bool, error)
}

// Config wires the service.
type Config struct {
	Runtime  *skillrun.Runtime
	Sessions Sessions
	Schema   catalog.ChatThreeViewSchema
}

// Service projects and advances three-view groups.
type Service struct {
	runtime  *skillrun.Runtime
	sessions Sessions
	schema   catalog.ChatThreeViewSchema
	// guards serialize requests for one view inside this process, so a double
	// click waits for the first request and then finds the view started. The
	// step identity and the ledger's compare-and-set keep it correct across
	// processes; the lock only keeps two requests from racing for one step.
	guards [64]sync.Mutex
}

// New builds the service.
func New(config Config) (*Service, error) {
	if config.Runtime == nil || config.Sessions == nil || config.Schema.RecipeID == "" ||
		config.Schema.ModelSourceStepKey == "" || config.Schema.MaximumListed < 1 {
		return nil, ErrInvalid
	}
	return &Service{runtime: config.Runtime, sessions: config.Sessions, schema: config.Schema}, nil
}

// List projects the groups of one of the owner's sessions, newest first.
func (s *Service) List(ctx context.Context, owner, session string) ([]Group, error) {
	if err := s.owns(ctx, owner, session); err != nil {
		return nil, err
	}
	return s.groups(ctx, owner, session)
}

func (s *Service) owns(ctx context.Context, owner, session string) error {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" {
		return ErrInvalid
	}
	owned, err := s.sessions.OwnsSession(ctx, owner, session)
	if err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	return nil
}

// groups projects the session's newest groups, at most the Schema's listing limit.
func (s *Service) groups(ctx context.Context, owner, session string) ([]Group, error) {
	return s.collect(ctx, owner, session, s.schema.MaximumListed)
}

// runPageSize is the repository's largest page; runs reads page after page.
const runPageSize = 100

// maximumRunPages bounds the scan of one session's runs.
const maximumRunPages = 10

// runs reads the session's runs, newest first, page by page.
func (s *Service) runs(ctx context.Context, owner, session string) ([]skillrun.Run, error) {
	var result []skillrun.Run
	after := ""
	for range maximumRunPages {
		page, err := s.runtime.List(ctx, generation.OwnerID(owner), session, after, runPageSize)
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < runPageSize {
			break
		}
		after = page[len(page)-1].ID
	}
	return result, nil
}

// collect projects the session's groups, newest first; limit 0 means all.
func (s *Service) collect(ctx context.Context, owner, session string, limit int) ([]Group, error) {
	runs, err := s.runs(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(runs))
	for _, run := range runs {
		if run.RecipeID != s.schema.RecipeID || run.OwnerID != generation.OwnerID(owner) || run.SessionID != session {
			continue
		}
		if limit > 0 && len(groups) == limit {
			break
		}
		group, err := s.project(ctx, run)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (s *Service) project(ctx context.Context, run skillrun.Run) (Group, error) {
	group := Group{
		RunID: run.ID, Kind: lineage.GroupThreeView, Label: s.schema.Label, RecipeID: run.RecipeID,
		SourceAssetID: string(run.SourceAssetID), CreatedAt: run.CreatedAt, Complete: len(run.Steps) > 0,
		ModelSource: ModelSource{Slot: s.schema.ModelSourceStepKey, Note: s.schema.ModelSourceNote},
	}
	for _, step := range run.Steps {
		slot, err := s.slot(ctx, run, step)
		if err != nil {
			return Group{}, err
		}
		group.Slots = append(group.Slots, slot)
		group.Complete = group.Complete && slot.Status == Succeeded
		if step.Spec.Key == s.schema.ModelSourceStepKey && slot.Status == Succeeded && slot.OutputAssetID != "" {
			group.ModelSource.Available = true
			group.ModelSource.TaskID, group.ModelSource.AssetID = slot.TaskID, slot.OutputAssetID
		}
	}
	group.priceUnstartedViews()
	return group, nil
}

// priceUnstartedViews prices a view that has no Task of its own yet like the
// views that do: every view of a group runs the same Offering.
func (group *Group) priceUnstartedViews() {
	perView, metered := 0, false
	var quote *generation.NativeQuote
	for _, slot := range group.Slots {
		if slot.NativeQuote != nil {
			quote = slot.NativeQuote
			break
		}
		if slot.EstimatedCredits > 0 || slot.CreditsMetered {
			perView, metered = slot.EstimatedCredits, slot.CreditsMetered
			break
		}
	}
	for index := range group.Slots {
		// An accepted Task or an existing quote is a frozen fact. Only an
		// unstarted slot can inherit this explicitly labelled default estimate.
		if group.Slots[index].TaskID != "" || group.Slots[index].NativeQuote != nil {
			if group.Slots[index].NativeQuote != nil && group.Slots[index].PriceSource == "" {
				group.Slots[index].PriceSource = "frozen"
			}
			continue
		}
		if quote != nil {
			group.Slots[index].NativeQuote = quote
			group.Slots[index].PriceSource = "default"
			group.Slots[index].CreditsMetered = true
			continue
		}
		if group.Slots[index].EstimatedCredits == 0 {
			group.Slots[index].EstimatedCredits = perView
		}
		group.Slots[index].CreditsMetered = group.Slots[index].CreditsMetered || metered
	}
}

// slot derives one view's status. A task the ledger names but the kernel can
// no longer read is reported as an error, never guessed.
func (s *Service) slot(ctx context.Context, run skillrun.Run, step skillrun.Step) (Slot, error) {
	slot := Slot{Key: step.Spec.Key, Label: step.Spec.Label, Attempt: step.Attempt, Status: NotStarted, Startable: true}
	owner := run.OwnerID
	if step.GrantRequestID != "" {
		grant, err := s.runtime.Grants.Grants.GetExecutionGrant(ctx, string(owner), step.GrantRequestID)
		if err != nil {
			return Slot{}, err
		}
		slot.RequestID = grant.RequestID
		switch grant.Status {
		case executiongrant.Pending:
			slot.Status, slot.Startable = AwaitingApproval, false
		case executiongrant.Denied:
			slot.Status, slot.Startable, slot.Retryable = Denied, false, true
		default:
			slot.Status = Starting
			if grant.TaskID != "" && step.TaskID == "" {
				slot.TaskID = grant.TaskID
			}
		}
	}
	if step.TaskID == "" {
		return slot, nil
	}
	slot.Startable, slot.TaskID = false, string(step.TaskID)
	task, err := s.runtime.Generation.Get(ctx, owner, step.TaskID)
	if err != nil {
		return Slot{}, err
	}
	if task.NativeQuote != nil {
		slot.NativeQuote = task.NativeQuote
		slot.PriceSource = "frozen"
	} else {
		slot.EstimatedCredits = task.Offering.EstimateTotalCredits(task.Input)
	}
	slot.CreditsMetered = task.Offering.CreditsMetered
	if task.OwnerID != owner || task.SessionID != run.SessionID {
		return Slot{}, skillrun.ErrConflict
	}
	switch task.State {
	case generation.TaskSucceeded:
		slot.Status = Succeeded
		for _, asset := range task.Assets {
			if asset.Role == step.Spec.OutputRole && asset.StorageKey != "" {
				slot.OutputAssetID = string(asset.ID)
				break
			}
		}
		if step.OutputAssetID != "" {
			slot.OutputAssetID = string(step.OutputAssetID)
		}
	case generation.TaskFailed:
		slot.Status = Failed
		slot.Retryable = task.Failure != nil && task.Failure.Retryable && !task.Failure.Ambiguous
	case generation.TaskCanceled:
		slot.Status, slot.Retryable = Canceled, true
	default:
		slot.Status = Running
	}
	return slot, nil
}

// Advance applies one request to one view and returns the group as it stands.
// The principal must be the authenticated person; the owner and session are
// proven before any record is read.
func (s *Service) Advance(ctx context.Context, principal executiongrant.Principal, session, runID, viewKey string,
	action Action, attempt int) (Group, error) {
	if principal.Source != executiongrant.HumanBrowser || principal.OwnerID == "" || principal.ActorID == "" ||
		(action != Start && action != Retry) || attempt < 0 {
		return Group{}, ErrInvalid
	}
	if err := s.owns(ctx, principal.OwnerID, session); err != nil {
		return Group{}, err
	}
	owner := generation.OwnerID(principal.OwnerID)
	run, err := s.runtime.Get(ctx, owner, runID)
	if err != nil {
		return Group{}, translate(err)
	}
	if run.SessionID != session || run.RecipeID != s.schema.RecipeID || !hasStep(run, viewKey) {
		return Group{}, ErrNotFound
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(principal.OwnerID + "\x00" + runID + "\x00" + viewKey))
	guard := &s.guards[hash.Sum32()%uint32(len(s.guards))]
	guard.Lock()
	defer guard.Unlock()

	if action == Retry {
		if _, err := s.runtime.RestartStep(ctx, owner, runID, viewKey, attempt); err != nil {
			return Group{}, translate(err)
		}
	}
	if err := s.drive(ctx, principal, runID, viewKey); err != nil {
		return Group{}, translate(err)
	}
	run, err = s.runtime.Get(ctx, owner, runID)
	if err != nil {
		return Group{}, translate(err)
	}
	return s.project(ctx, run)
}

func hasStep(run skillrun.Run, key string) bool {
	for _, step := range run.Steps {
		if step.Spec.Key == key {
			return true
		}
	}
	return false
}

// drive moves the view as far as it can go without a person: prepare, request
// its approval (the session mode may approve it at once), and submit once it
// is approved. It stops at a pending approval, a denial or a started task, so
// calling it again at any point either continues or changes nothing.
func (s *Service) drive(ctx context.Context, principal executiongrant.Principal, runID, viewKey string) error {
	owner := generation.OwnerID(principal.OwnerID)
	for range 4 {
		run, err := s.runtime.Get(ctx, owner, runID)
		if err != nil {
			return err
		}
		var step skillrun.Step
		for _, candidate := range run.Steps {
			if candidate.Spec.Key == viewKey {
				step = candidate
			}
		}
		switch {
		case step.TaskID != "" || step.Phase == skillrun.TaskBound || step.Phase == skillrun.OutputReady:
			return nil
		case step.Phase == skillrun.Planned:
			if _, _, err = s.runtime.PrepareStep(ctx, owner, runID, viewKey); err != nil {
				return err
			}
		case step.Phase == skillrun.Prepared:
			if _, _, err = s.runtime.RequestGrant(ctx, principal, runID, viewKey); err != nil {
				return err
			}
		case step.Phase == skillrun.GrantPending:
			grant, err := s.runtime.Grants.Grants.GetExecutionGrant(ctx, principal.OwnerID, step.GrantRequestID)
			if err != nil {
				return err
			}
			switch grant.Status {
			case executiongrant.Approved, executiongrant.Claimed, executiongrant.Bound:
				// The submission outlives this request: a person closing the
				// page must not leave an approved, paid step half-made.
				if _, _, err = s.runtime.SubmitStep(context.WithoutCancel(ctx), owner, runID, viewKey); err != nil {
					return err
				}
				return nil
			default:
				return nil // pending or denied: a person decides
			}
		default:
			return skillrun.ErrConflict
		}
	}
	return nil
}

func translate(err error) error {
	switch {
	case errors.Is(err, skillrun.ErrMissing):
		return ErrNotFound
	case errors.Is(err, skillrun.ErrConflict), errors.Is(err, executiongrant.ErrConflict),
		errors.Is(err, executiongrant.ErrState):
		return ErrConflict
	case errors.Is(err, skillrun.ErrInvalid), errors.Is(err, executiongrant.ErrInvalid):
		return ErrInvalid
	}
	return err
}

// LineageGroups lists the finished views of each group for the version strip.
func (s *Service) LineageGroups(ctx context.Context, owner, session string) ([]lineage.Group, error) {
	groups, err := s.groups(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	result := make([]lineage.Group, 0, len(groups))
	for _, group := range groups {
		item := lineage.Group{
			ID: group.RunID, Kind: group.Kind, Label: group.Label, Complete: group.Complete,
			SourceSlot: group.ModelSource.Slot, CreatedAt: group.CreatedAt,
		}
		for _, slot := range group.Slots {
			if slot.Status == Succeeded && slot.TaskID != "" {
				item.Members = append(item.Members, lineage.GroupMember{Slot: slot.Key, Label: slot.Label, TaskID: slot.TaskID})
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// SourceOf says whether a task is one view of a three-view group of the
// session, and if so which finished view a 3D model made from the group uses.
// A task that is not a view reports false and is used as it is. Every group
// of the session counts, not only the ones the listing shows: a view of an
// older group must still be replaced by its front view.
func (s *Service) SourceOf(ctx context.Context, owner, session, taskID string) (source ModelSource, member bool, err error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" || strings.TrimSpace(taskID) == "" {
		return ModelSource{}, false, ErrInvalid
	}
	run, err := s.runtime.Runs.GetByTask(ctx, generation.OwnerID(owner), session, generation.TaskID(taskID))
	if errors.Is(err, skillrun.ErrMissing) {
		return ModelSource{}, false, nil
	}
	if err != nil {
		return ModelSource{}, false, err
	}
	if run.OwnerID != generation.OwnerID(owner) || run.SessionID != session || run.RecipeID != s.schema.RecipeID {
		return ModelSource{}, false, nil
	}
	group, err := s.project(ctx, run)
	if err != nil {
		return ModelSource{}, false, err
	}
	for _, slot := range group.Slots {
		if slot.TaskID == taskID {
			return group.ModelSource, true, nil
		}
	}
	return ModelSource{}, false, nil
}

// ForGeneration adapts the service to the chat generation tools.
type ForGeneration struct{ Service *Service }

// ModelSource implements generationtool.ThreeViews.
func (a ForGeneration) ModelSource(ctx context.Context, owner, session, taskID string) (generationtool.ModelSource, bool, error) {
	source, member, err := a.Service.SourceOf(ctx, owner, session, taskID)
	if err != nil || !member {
		return generationtool.ModelSource{}, false, err
	}
	return generationtool.ModelSource{Available: source.Available, TaskID: source.TaskID, AssetID: source.AssetID}, true, nil
}

var _ generationtool.ThreeViews = ForGeneration{}
var _ lineage.GroupSource = (*Service)(nil)
