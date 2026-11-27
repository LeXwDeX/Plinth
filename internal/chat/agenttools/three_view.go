package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

const threeViewRecipeID = "image-three-view"

// createThreeView only prepares the first step and asks for an exact human
// grant. Every paid submission goes through SkillRun -> Generation after the
// human decision. The run key survives turn and process recovery.
func (e *Executor) createThreeView(ctx context.Context, command chat.ToolCommand,
	observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	if e.skillRuntime == nil {
		return chat.ToolResult{Error: "three-view skill unavailable", NoSubmission: true}, nil
	}
	if command.OwnerID == "" || command.SessionID == "" || command.Call.ID == "" ||
		command.AssistantMessageID == "" {
		return chat.ToolResult{NoSubmission: true}, skillrun.ErrInvalid
	}
	run, execution, grant, err := e.existingThreeView(ctx, command)
	if err != nil && !errors.Is(err, skillrun.ErrMissing) {
		return chat.ToolResult{}, err
	}
	// A run whose grants were recorded budget-eligible continues inside the
	// approved plan (#243). A strict-mode run never becomes one later.
	continuationRun := err == nil && grant.BudgetEligible && e.threeViewService != nil &&
		e.threeViewService.ContinuationEnabled()
	if err == nil && grant.Status == executiongrant.Bound {
		if continuationRun {
			stopped, stopErr := e.skillTurnStopped(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if stopErr != nil {
				return chat.ToolResult{NoSubmission: true}, stopErr
			}
			if stopped {
				return chat.ToolResult{AgentContent: e.threeView.Messages.TurnStopped, NoSubmission: true}, nil
			}
			return e.continueThreeView(ctx, command, run, observe)
		}
		return e.observeThreeView(ctx, command, run, execution, grant, observe)
	}
	if command.BoundTaskID != "" {
		return chat.ToolResult{TaskID: command.BoundTaskID}, chat.ErrTurnCheckpointConflict
	}
	stopped, stopErr := e.skillTurnStopped(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if stopErr != nil {
		return chat.ToolResult{NoSubmission: true}, stopErr
	}
	if stopped {
		return chat.ToolResult{AgentContent: e.threeView.Messages.TurnStopped, NoSubmission: true}, nil
	}
	created := false
	// release ends the duplicate guard's lock of a new group (#665).
	release := func() {}
	// superseded holds the earlier groups' approved, unsubmitted grants a new
	// group replaces; counted says this call counted against the turn's
	// generations, so a failure before its first grant takes it back (#665).
	var superseded []supersededThreeViewGrant
	counted := false
	takeBack := func(result chat.ToolResult, err error) (chat.ToolResult, error) {
		if counted {
			e.forgetGenerationCall(e.scopeKey(command), command.AssistantMessageID, command.Call.ID)
			result.GenerationUnitsTakenBack = true
		}
		return result, err
	}
	// resumed: the call's own run exists, but its first view never got a grant:
	// the call failed, or the process restarted, between creating the run and
	// requesting the grant. The replay finishes that start as a new run does
	// (#665): under the session lock it runs the duplicate guard again for the
	// run's own recipe and source, counts the call (once per call ID), weighs
	// the continuation, replaces the ended groups' approvals and requests the
	// grant. Before, it requested a plain grant only: the old approval stayed,
	// the auto mode or plan did not continue it and the count was lost.
	resumed := false
	if err == nil && grant.RequestID == "" && command.BoundTaskID == "" {
		releaseSource, lockErr := e.lockThreeViewSession(ctx, command.OwnerID, command.SessionID)
		if lockErr != nil {
			return chat.ToolResult{NoSubmission: true}, lockErr
		}
		defer releaseSource()
		active, found, ended, guardErr := e.activeThreeViewForSource(ctx, command, run.RecipeID, run.SourceAssetID)
		if guardErr != nil {
			return chat.ToolResult{NoSubmission: true}, guardErr
		}
		// A refusal here leaves the run without a grant (inert: no card, not
		// active for the guard); it takes back what an earlier attempt of the
		// call may have left counted.
		if found != threeViewNoGroup {
			result := e.threeViewGuardRefusal(active)
			result.GenerationUnitsTakenBack = true
			return result, nil
		}
		if e.generationLimitReachedForCall(e.scopeKey(command), command.AssistantMessageID,
			command.Call.ID, command.PriorGenerationCalls, e.maxGeneration) {
			result := e.limitResult(command)
			result.GenerationUnitsTakenBack = true
			return result, nil
		}
		superseded, counted, resumed, release = ended, true, true, releaseSource
	}
	if errors.Is(err, skillrun.ErrMissing) {
		// Mutable model/reference/recipe selection applies only to a new run.
		// Existing runs keep their original recipe and frozen source.
		if e.skillReferences == nil {
			return chat.ToolResult{Error: "three-view skill unavailable", NoSubmission: true}, nil
		}
		var args struct {
			ReferenceImageIndices []int           `json:"reference_image_indices"`
			ReferenceHandles      []string        `json:"reference_handles"`
			SubjectType           string          `json:"subject_type"`
			Purpose               string          `json:"purpose"`
			IncludeRight          json.RawMessage `json:"include_right"`
		}
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return chat.ToolResult{AgentContent: e.threeView.Messages.ArgumentsInvalid, NoSubmission: true}, nil
		}
		// A four-view request must say so with a JSON boolean: a string "true"
		// silently made three views before (#665).
		if _, err := threeViewIncludeRight(args.IncludeRight); err != nil {
			return chat.ToolResult{Error: err.Error(), NoSubmission: true}, nil
		}
		// The use decides the recipe: a design sheet keeps the source's shape
		// and pose, a model input renders the orthographic set for 3D (#409).
		// An absent value takes the declared default, so an old run and a new
		// call without the argument keep the historical recipe.
		purpose, known := e.threeViewPurpose(args.Purpose)
		if !known {
			return chat.ToolResult{AgentContent: e.threeViewPurposeRefusal(args.Purpose), NoSubmission: true}, nil
		}
		subjectType, subjectRefusal := e.threeViewSubjectType(args.SubjectType)
		if subjectRefusal != "" {
			return chat.ToolResult{AgentContent: subjectRefusal, NoSubmission: true}, nil
		}
		// The model chosen for this tool's own domain, whichever workspace is in view.
		toolDomain := command.Options.CreativeDomain
		if len(command.Tool.Execution.Domains) == 1 {
			toolDomain = command.Tool.Execution.Domains[0]
		}
		selected := e.threeViewModel(command, toolDomain)
		if selected.ID == "" {
			return chat.ToolResult{AgentContent: e.threeView.Messages.ModelUnavailable, NoSubmission: true}, nil
		}
		urls, handleErr := e.threeViewImages(command, args.ReferenceImageIndices, args.ReferenceHandles)
		if handleErr != "" {
			return chat.ToolResult{AgentContent: handleErr, NoSubmission: true}, nil
		}
		if len(urls) != 1 {
			return chat.ToolResult{AgentContent: e.threeView.Messages.ReferenceRequired, NoSubmission: true}, nil
		}
		assets, err := e.skillReferences.Resolve(ctx, references.ReferenceScope{
			Owner: generation.OwnerID(command.OwnerID), SessionID: command.SessionID, ProjectID: command.Options.ProjectID,
		}, urls)
		if err != nil || len(assets) != 1 || assets[0] == "" {
			return chat.ToolResult{AgentContent: e.threeView.Messages.ImageUnavailable, NoSubmission: true}, nil
		}
		revision := e.skillRecipes[purpose.RecipeID]
		if revision == "" {
			return chat.ToolResult{NoSubmission: true}, skillrun.ErrInvalid
		}
		// One group per source picture at a time (#665): a parallel call or a
		// re-ask while the earlier group waits on its card or runs would start
		// a second front view, charge it and raise a second card. The lock is
		// held until this call's own first grant is recorded on its run.
		releaseSource, err := e.lockThreeViewSession(ctx, command.OwnerID, command.SessionID)
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		defer releaseSource()
		var active skillrun.Run
		var found threeViewGuard
		active, found, superseded, err = e.activeThreeViewForSource(ctx, command, purpose.RecipeID, assets[0])
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		} else if found != threeViewNoGroup {
			return e.threeViewGuardRefusal(active), nil
		}
		// A grant to replace that a person continued since the guard read it
		// (claimed or bound) means that group runs again: answer duplicate_run
		// before creating anything, so the new run is not left without a grant.
		// The denial below reads back again for the short window after this.
		if moved, err := e.supersededMovedOn(ctx, superseded); err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		} else if moved != "" {
			return chat.ToolResult{AgentContent: strings.ReplaceAll(e.threeView.Messages.DuplicateRun, "{run}", moved),
				NoSubmission: true}, nil
		}
		// A new run is one paid generation of the turn, counted against
		// max_generation_calls_per_turn like generateWithParams (#563); the
		// same call back on its own run (recovery, observation) does not
		// reach here and is not counted again. A call the duplicate guard
		// refused starts no run and is not counted (#665).
		if e.generationLimitReachedForCall(e.scopeKey(command), command.AssistantMessageID,
			command.Call.ID, command.PriorGenerationCalls, e.maxGeneration) {
			return e.limitResult(command), nil
		}
		counted = true
		release = releaseSource
		// The frame of every view follows the source subject's shape instead
		// of the Offering's default square (#283), or the source picture's own
		// shape for a use that declares it (#409). It is frozen with the run;
		// nothing here can fail the run, a source that cannot be measured keeps
		// the default frame.
		var frame map[string]json.RawMessage
		if e.threeViewService != nil {
			frame = e.threeViewService.FrameInput(ctx, generation.OwnerID(command.OwnerID), selected,
				subjectType, purpose.ID, assets[0])
		}
		run, err = e.skillRuntime.Create(ctx, skillrun.CreateCommand{
			Harness: threeViewHarnessBinding(command),
			OwnerID: generation.OwnerID(command.OwnerID), SessionID: command.SessionID,
			RecipeID: purpose.RecipeID, ExpectedRevision: revision,
			SelectedOfferingID: selected.ID, SourceAssetID: assets[0],
			IdempotencyKey: threeViewRunKey(command), SubjectType: subjectType,
			StaticInput: frame,
		})
		if err != nil {
			return takeBack(chat.ToolResult{NoSubmission: true}, err)
		}
		created = true
	}
	if len(run.Steps) == 0 {
		return takeBack(chat.ToolResult{NoSubmission: true}, skillrun.ErrConflict)
	}
	canContinue := false
	if e.threeViewService != nil && e.threeViewService.ContinuationEnabled() && (created || resumed || grant.BudgetEligible) {
		canContinue, err = e.threeViewService.CanContinue(ctx, command.OwnerID, command.SessionID, run.ID)
		if err != nil {
			return takeBack(chat.ToolResult{NoSubmission: true}, err)
		}
	}
	stepKey := run.Steps[0].Spec.Key
	if _, _, err := e.skillRuntime.PrepareStep(ctx, generation.OwnerID(command.OwnerID), run.ID, stepKey); err != nil {
		return takeBack(chat.ToolResult{NoSubmission: true}, err)
	}
	if created || resumed {
		// The new group is created and its first view prepared; only now does
		// it replace the earlier groups' approved views whose turn ended (a
		// person could still continue one from the panel while the new group
		// pays for the same views). A failure to create or prepare leaves
		// those approvals as they were; the replay of the call finishes the
		// start (resumed above). A grant that moved on since the check before
		// Create (the person continued it: claimed or bound) is not denied:
		// that group runs again, so the new one requests no grant and its run
		// stays without one (inert: no card, not active for the guard; the
		// SkillRun store has no way to abandon a run). The approvals denied
		// before it are replaced by the group the person continued, which
		// renders the same views (#665).
		if replaced, err := e.replaceEndedThreeViewGrants(ctx, superseded); err != nil {
			return takeBack(chat.ToolResult{NoSubmission: true}, err)
		} else if replaced != "" {
			return takeBack(chat.ToolResult{AgentContent: strings.ReplaceAll(e.threeView.Messages.DuplicateRun,
				"{run}", replaced), NoSubmission: true}, nil)
		}
	}
	if canContinue {
		_, _, err = e.skillRuntime.RequestAgentGrantWithBudget(ctx, generation.OwnerID(command.OwnerID),
			run.ID, stepKey, command.AssistantMessageID)
	} else {
		_, _, err = e.skillRuntime.RequestAgentGrant(ctx, generation.OwnerID(command.OwnerID),
			run.ID, stepKey, command.AssistantMessageID)
	}
	release()
	if err != nil {
		return takeBack(chat.ToolResult{NoSubmission: true}, err)
	}
	run, execution, grant, err = e.existingThreeView(ctx, command)
	if err != nil {
		return chat.ToolResult{}, err
	}
	switch grant.Status {
	case executiongrant.Pending:
		return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
	case executiongrant.Denied:
		return chat.ToolResult{AgentContent: e.threeView.Messages.Denied, NoSubmission: true}, nil
	case executiongrant.Claimed:
		return chat.ToolResult{Suspend: true}, nil
	case executiongrant.Bound:
		return e.observeThreeView(ctx, command, run, execution, grant, observe)
	case executiongrant.Approved:
	default:
		return chat.ToolResult{NoSubmission: true}, fmt.Errorf("unexpected skill grant state %q", grant.Status)
	}
	stopped, err = e.skillTurnStopped(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if stopped {
		return chat.ToolResult{AgentContent: e.threeView.Messages.TurnStopped, NoSubmission: true}, nil
	}
	run, accepted, err := e.skillRuntime.SubmitStep(context.WithoutCancel(ctx), generation.OwnerID(command.OwnerID), run.ID, stepKey)
	if err != nil {
		// Submit may have committed before a response was lost. The run and
		// step identity remain durable, so keep this call unfinished.
		if accepted.TaskID != "" {
			return e.awaitThreeView(command, string(accepted.TaskID), generation.Offering{}, err)
		}
		if wait := generation.AdmissionRetryAfter(err); wait > 0 {
			// 在途准入已满：内核确定没有提交，步骤仍停在已批准的授权上。
			// 交给编排器按 RetryAfter 排队，唤醒后同一调用重放到这里再提交。
			// 排队超过时限时编排器把这条回执交给模型：说明没有提交及原因。
			return chat.ToolResult{AgentContent: e.threeView.Messages.AdmissionFull, NoSubmission: true, RetryAfter: wait}, err
		}
		return chat.ToolResult{}, err
	}
	if accepted.TaskID == "" || len(run.Steps) == 0 {
		return chat.ToolResult{}, skillrun.ErrConflict
	}
	run, execution, grant, err = e.existingThreeView(ctx, command)
	if err != nil {
		return e.awaitThreeView(command, string(accepted.TaskID), generation.Offering{}, err)
	}
	result, err := e.observeThreeView(ctx, command, run, execution, grant, observe)
	if err != nil || !canContinue {
		return result, err
	}
	// The budget continuation keeps this call open for the whole view series:
	// every later view is submitted on replay after its predecessor finished
	// (#243).
	offering := generation.Offering{}
	if task, readErr := e.skillRuntime.Generation.Get(ctx, generation.OwnerID(command.OwnerID),
		generation.TaskID(result.TaskID)); readErr == nil {
		offering = task.Offering
	}
	result.AwaitTask = true
	result.WaitUntil = e.threeViewWaitUntil(command, offering)
	return result, nil
}

// threeViewIncludeRight reads the include_right argument strictly: absent or
// null is false, a JSON boolean is its value, and anything else (a string
// "true", a number) is an error naming the expected type (#665).
func threeViewIncludeRight(raw json.RawMessage) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("include_right must be a JSON boolean (true or false), got %s", trimmed)
	}
	return value, nil
}

// threeViewLocks is a keyed lock with reference counts: an entry lives only
// while a call holds or waits for it, so sessions do not accumulate mutexes
// (#665). A waiter gives up when its context ends.
type threeViewLocks struct {
	mu      sync.Mutex
	entries map[string]*threeViewLock
}

type threeViewLock struct {
	held chan struct{}
	refs int
}

func (l *threeViewLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = map[string]*threeViewLock{}
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &threeViewLock{held: make(chan struct{}, 1)}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	select {
	case entry.held <- struct{}{}:
	case <-ctx.Done():
		l.drop(key, entry)
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.held
			l.drop(key, entry)
		})
	}, nil
}

func (l *threeViewLocks) drop(key string, entry *threeViewLock) {
	l.mu.Lock()
	entry.refs--
	if entry.refs == 0 && l.entries[key] == entry {
		delete(l.entries, key)
	}
	l.mu.Unlock()
}

// size is the number of keys held or waited for; tests read it.
func (l *threeViewLocks) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// lockThreeViewSession serializes the start of new groups in one session and
// returns an idempotent release. It gives up when ctx ends.
func (e *Executor) lockThreeViewSession(ctx context.Context, owner, session string) (func(), error) {
	return e.threeViewSources.acquire(ctx, owner+"\x00"+session)
}

// activeThreeViewForSource finds another group of the session, of the same
// recipe and source picture, that still waits on a card or has a view in
// flight (#665): a view whose grant is pending or approved while its turn can
// still use it, or being claimed, or whose Task has not ended; or a group
// between two views whose continuation turn is still going. A finished or
// failed group, and one whose card belongs to a stopped turn, is not active:
// asking again for it starts a new group as before. Without such a group it
// also returns the approved, unsubmitted grants of those groups whose turn
// ended (superseded): a new group replaces them.
func (e *Executor) activeThreeViewForSource(ctx context.Context, command chat.ToolCommand, recipeID string,
	source generation.AssetID,
) (skillrun.Run, threeViewGuard, []supersededThreeViewGrant, error) {
	owner := generation.OwnerID(command.OwnerID)
	limit := e.threeView.MaximumListed
	if limit <= 0 {
		limit = 20
	}
	runs, err := e.skillRuntime.List(ctx, owner, command.SessionID, "", limit)
	if err != nil {
		return skillrun.Run{}, threeViewNoGroup, nil, err
	}
	key := threeViewRunKey(command)
	var superseded []supersededThreeViewGrant
	for _, run := range runs {
		if run.RecipeID != recipeID || run.SourceAssetID != source || run.IdempotencyKey == key {
			continue
		}
		for _, step := range run.Steps {
			active, idle, err := e.threeViewStepActive(ctx, owner, step)
			if err != nil {
				return skillrun.Run{}, threeViewNoGroup, nil, err
			}
			if active {
				return run, threeViewGroupActive, nil, nil
			}
			if idle.RequestID != "" {
				superseded = append(superseded, supersededThreeViewGrant{runID: run.ID, grant: idle})
			}
		}
		active, err := e.threeViewBetweenViews(ctx, owner, run)
		if err != nil {
			return skillrun.Run{}, threeViewNoGroup, nil, err
		}
		if active {
			return run, threeViewGroupActive, nil, nil
		}
	}
	return skillrun.Run{}, threeViewNoGroup, superseded, nil
}

// threeViewGuard is what the duplicate guard found for a new group's source.
type threeViewGuard int

const (
	threeViewNoGroup threeViewGuard = iota
	// threeViewGroupActive: a group waits on its card or runs (#665).
	threeViewGroupActive
)

// threeViewGuardRefusal is the receipt of a call the duplicate guard refused:
// nothing was created, submitted or charged.
func (e *Executor) threeViewGuardRefusal(run skillrun.Run) chat.ToolResult {
	return chat.ToolResult{AgentContent: strings.ReplaceAll(e.threeView.Messages.DuplicateRun, "{run}", run.ID),
		NoSubmission: true}
}

// supersededThreeViewGrant is an approved, unsubmitted view of an earlier
// group whose turn ended, with the run that holds it.
type supersededThreeViewGrant struct {
	runID string
	grant executiongrant.Grant
}

// replaceEndedThreeViewGrants denies the superseded approvals for a new group
// (AbandonedActor, audited), which releases what they held; the person can
// still restart such a view. It returns the run of a grant the denial did not
// reach because it moved on after the guard read it (claimed or bound by the
// person continuing it): that group runs again and the new one must not start.
func (e *Executor) replaceEndedThreeViewGrants(ctx context.Context, superseded []supersededThreeViewGrant) (string, error) {
	for _, entry := range superseded {
		grant := entry.grant
		if err := e.skillRuntime.Grants.AbandonRefusedGrant(ctx, grant.OwnerID, grant.RequestID); err != nil {
			return "", err
		}
		// The denial only applies to a grant still approved; read back what
		// it did instead of trusting a silent no-op.
		after, err := e.skillRuntime.Grants.Grants.GetExecutionGrant(ctx, grant.OwnerID, grant.RequestID)
		if err != nil {
			return "", err
		}
		if after.Status != executiongrant.Denied {
			return entry.runID, nil
		}
	}
	return "", nil
}

// supersededMovedOn reads the grants to replace again and returns the run of
// one that is no longer approved: a person continued it after the guard read
// it, so its group runs again. A grant already denied (nothing to replace) does
// not count.
func (e *Executor) supersededMovedOn(ctx context.Context, superseded []supersededThreeViewGrant) (string, error) {
	for _, entry := range superseded {
		now, err := e.skillRuntime.Grants.Grants.GetExecutionGrant(ctx, entry.grant.OwnerID, entry.grant.RequestID)
		if err != nil {
			return "", err
		}
		if now.Status != executiongrant.Approved && now.Status != executiongrant.Denied {
			return entry.runID, nil
		}
	}
	return "", nil
}

// threeViewTurnStopped reports whether the grant's trigger turn was stopped.
// Such a grant can never be claimed (a three-view card is never detached from
// its turn), so it does not keep its group active. A turn that no longer
// exists is not a stop.
func (e *Executor) threeViewTurnStopped(ctx context.Context, grant executiongrant.Grant) (bool, error) {
	if grant.TriggerTurnID == "" || e.skillTurnStopped == nil {
		return false, nil
	}
	stopped, err := e.skillTurnStopped(ctx, grant.OwnerID, grant.SessionID, grant.TriggerTurnID)
	if errors.Is(err, chat.ErrMessageNotFound) {
		return false, nil
	}
	return stopped, err
}

// threeViewTurnOver reports whether the grant's trigger turn has ended (any
// end reason) or no longer exists. Without the turn reader, or without a
// trigger turn, nothing is known and the turn counts as going on.
func (e *Executor) threeViewTurnOver(ctx context.Context, grant executiongrant.Grant) (bool, error) {
	if grant.TriggerTurnID == "" || e.threeViewTurnEnded == nil {
		return false, nil
	}
	ended, err := e.threeViewTurnEnded(ctx, grant.OwnerID, grant.SessionID, grant.TriggerTurnID)
	if errors.Is(err, chat.ErrMessageNotFound) {
		return true, nil
	}
	return ended, err
}

// threeViewStepActive reports whether a view keeps its group active. A view
// that does not, but holds an approval its ended turn never submitted, returns
// that grant as idle: a person could still continue it from the panel.
func (e *Executor) threeViewStepActive(ctx context.Context, owner generation.OwnerID, step skillrun.Step) (bool, executiongrant.Grant, error) {
	none := executiongrant.Grant{}
	taskActive := func(id generation.TaskID) (bool, executiongrant.Grant, error) {
		task, err := e.skillRuntime.Generation.Get(ctx, owner, id)
		if err != nil {
			return false, none, err
		}
		return !task.State.Terminal(), none, nil
	}
	switch step.Phase {
	case skillrun.GrantPending:
		if step.GrantRequestID == "" {
			return false, none, nil
		}
		grant, err := e.skillRuntime.Grants.Grants.GetExecutionGrant(ctx, string(owner), step.GrantRequestID)
		if err != nil {
			return false, none, err
		}
		switch grant.Status {
		case executiongrant.Pending, executiongrant.Approved:
			// A pending card waits until it expires; an approved grant counts
			// only while the step can still submit with it. Neither counts once
			// its turn was stopped: nobody can approve or claim it then. A
			// grant the kernel refused for good is denied by SubmitStep.
			if grant.Status == executiongrant.Pending && e.skillRuntime.Grants.Expired(grant) {
				return false, none, nil
			}
			stopped, err := e.threeViewTurnStopped(ctx, grant)
			if err != nil || stopped {
				return false, none, err
			}
			if grant.Status == executiongrant.Approved {
				// An approval is submitted by its turn when it replays the
				// call. Once that turn has ended for any reason (an error
				// between approval and submit included) or is gone, nothing
				// replays it: the group no longer counts as running, or the
				// guard would refuse every new ask for good (#665). A person
				// can still continue the view from the panel. A turn a crash
				// left running is ended by the turn recovery.
				over, err := e.threeViewTurnOver(ctx, grant)
				if err != nil {
					return false, none, err
				}
				if over {
					return false, grant, nil
				}
				return true, none, nil
			}
			return true, none, nil
		case executiongrant.Claimed:
			return true, none, nil
		case executiongrant.Bound:
			return taskActive(generation.TaskID(grant.TaskID))
		}
	case skillrun.TaskBound:
		if step.TaskID != "" {
			return taskActive(step.TaskID)
		}
	}
	return false, none, nil
}

// threeViewBetweenViews reports a group the server continues by itself that
// sits between two views (#665): an earlier view is finished and a later
// required view is still planned, while the turn that approved the latest
// view on the continuation basis (a production plan or the auto mode) is
// still going and was not stopped. That turn submits the next view when it
// replays the call. A group whose turn ended or stopped is paused.
func (e *Executor) threeViewBetweenViews(ctx context.Context, owner generation.OwnerID, run skillrun.Run) (bool, error) {
	if e.threeViewTurnEnded == nil {
		return false, nil
	}
	latest, planned := "", false
	for _, step := range run.Steps {
		switch {
		case step.Phase == skillrun.OutputReady && step.GrantRequestID != "":
			latest = step.GrantRequestID
		case (step.Phase == skillrun.Planned || step.Phase == skillrun.Prepared) && !step.Spec.Optional &&
			step.ReplacementAssetID == "":
			planned = true
		}
	}
	if latest == "" || !planned {
		return false, nil
	}
	grant, err := e.skillRuntime.Grants.Grants.GetExecutionGrant(ctx, string(owner), latest)
	if err != nil {
		return false, err
	}
	continued := grant.BudgetEligible && (grant.ApprovedByBudget() ||
		grant.DecidedBy == executiongrant.PolicyActorPrefix+approvalpolicy.ModeAuto)
	if !continued || grant.TriggerTurnID == "" {
		return false, nil
	}
	if stopped, err := e.threeViewTurnStopped(ctx, grant); err != nil || stopped {
		return false, err
	}
	ended, err := e.threeViewTurnEnded(ctx, grant.OwnerID, grant.SessionID, grant.TriggerTurnID)
	if errors.Is(err, chat.ErrMessageNotFound) {
		return false, nil
	}
	return !ended, err
}

func threeViewRunKey(command chat.ToolCommand) string {
	return "chat-skill:" + command.SessionID + ":" + command.Call.ID
}

// threeViewPurpose resolves the requested use to its declared recipe. An
// absent value takes the Schema's default; a Schema without declared uses has
// the one historical recipe. An unknown use is refused, so a new recipe never
// changes what an old call or an old run means (#409).
func (e *Executor) threeViewPurpose(requested string) (catalog.ChatThreeViewPurpose, bool) {
	if len(e.threeView.Purposes) == 0 {
		// A Schema that declares no use, or an Executor wired without the
		// three-view Schema at all, still has the historical recipe: it is the
		// one the tool always ran before uses existed (#409).
		recipe := e.threeView.RecipeID
		if recipe == "" {
			recipe = threeViewRecipeID
		}
		return catalog.ChatThreeViewPurpose{ID: e.threeView.DefaultPurpose, RecipeID: recipe}, true
	}
	wanted := strings.TrimSpace(requested)
	if wanted == "" {
		wanted = e.threeView.DefaultPurpose
	}
	for _, purpose := range e.threeView.Purposes {
		if purpose.ID == wanted {
			return purpose, true
		}
	}
	return catalog.ChatThreeViewPurpose{}, false
}

func (e *Executor) threeViewPurposeRefusal(requested string) string {
	choices := make([]string, 0, len(e.threeView.Purposes))
	for _, purpose := range e.threeView.Purposes {
		choices = append(choices, purpose.ID)
	}
	return strings.NewReplacer("{purpose}", strings.TrimSpace(requested),
		"{choices}", strings.Join(choices, ", ")).Replace(e.threeView.Messages.PurposeInvalid)
}

// threeViewRecipeKnown reports whether a run's recipe is one of the uses this
// Schema declares, or the historical recipe a run predating purposes used
// (#409). Everything else is a different call's run.
func (e *Executor) threeViewRecipeKnown(recipeID string) bool {
	if recipeID == "" {
		return false
	}
	if recipeID == threeViewRecipeID || recipeID == e.threeView.RecipeID {
		return true
	}
	for _, purpose := range e.threeView.Purposes {
		if purpose.RecipeID == recipeID {
			return true
		}
	}
	return false
}

// threeViewModel is the model of a new group: the shared four-layer choice the
// generation tool makes for refine_image (#409), and the person's explicit pick
// for this tool's domain when no generation executor is wired.
func (e *Executor) threeViewModel(command chat.ToolCommand, toolDomain string) generation.Offering {
	if chooser, ok := e.generate.(interface {
		ChooseThreeViewModel(chat.ToolCommand) (generation.Offering, string)
	}); ok {
		selected, _ := chooser.ChooseThreeViewModel(command)
		return selected
	}
	if e.skillOfferings == nil {
		return generation.Offering{}
	}
	model := command.Options.ModelFor(toolDomain)
	if model == "" {
		return generation.Offering{}
	}
	for _, offering := range e.skillOfferings.Offerings(generation.ImageToImage) {
		if offering.Enabled && e.skillOfferings.OfferingAvailable(offering.ID) &&
			(model == offering.ModelID || model == string(offering.ID)) {
			return offering
		}
	}
	return generation.Offering{}
}

func threeViewHarnessBinding(command chat.ToolCommand) harness.Binding {
	if command.Options.Harness != nil {
		return command.Options.Harness.Selection.Binding
	}
	return harness.Binding{}
}
func (e *Executor) existingThreeView(ctx context.Context, command chat.ToolCommand) (skillrun.Run, skillrun.Execution, executiongrant.Grant, error) {
	run, execution, grant, err := e.readThreeView(ctx, command)
	if err == nil && !harness.SameBinding(run.Harness, threeViewHarnessBinding(command)) {
		return skillrun.Run{}, skillrun.Execution{}, executiongrant.Grant{}, chat.ErrTurnCheckpointConflict
	}
	return run, execution, grant, err
}

// resolveThreeViewState reads the run, the step named by the call's bound Task
// (or the first step), and its exact grant. A Task bound to the grant while the
// Skill step binding was lost is recovered only when the grant really names
// that Task; any other Task is a different call's (#243).
func (e *Executor) resolveThreeViewState(ctx context.Context, command chat.ToolCommand) (skillrun.Run, skillrun.Execution, executiongrant.Grant, error) {
	run, execution, grant, err := e.skillRuntime.ResolveAgentStepAt(ctx, generation.OwnerID(command.OwnerID),
		command.SessionID, threeViewRunKey(command), command.AssistantMessageID, command.BoundTaskID)
	if command.BoundTaskID != "" && errors.Is(err, skillrun.ErrMissing) {
		run, execution, grant, err = e.skillRuntime.ResolveAgentStep(ctx, generation.OwnerID(command.OwnerID),
			command.SessionID, threeViewRunKey(command), command.AssistantMessageID)
		if err == nil && (grant.Status != executiongrant.Bound || grant.TaskID != command.BoundTaskID) {
			err = chat.ErrTurnCheckpointConflict
		}
	}
	if err != nil {
		if errors.Is(err, skillrun.ErrConflict) ||
			(command.BoundTaskID != "" && errors.Is(err, skillrun.ErrMissing)) {
			err = errors.Join(chat.ErrTurnCheckpointConflict, err)
		}
		return run, execution, grant, err
	}
	return run, execution, grant, nil
}

// Read-only identity lookup is used by Stop before executable tool resolution.
// ResolveAgentStep still verifies the stored Run, Grant, turn and step identity.
func (e *Executor) readThreeView(ctx context.Context, command chat.ToolCommand) (skillrun.Run, skillrun.Execution, executiongrant.Grant, error) {
	run, execution, grant, err := e.resolveThreeViewState(ctx, command)
	if err != nil {
		return run, execution, grant, err
	}
	if !e.threeViewRecipeKnown(run.RecipeID) || execution.Step.Spec.Capability != generation.ImageToImage ||
		(command.FrozenIdempotencyKey != "" && execution.Step.Spec.Key == run.Steps[0].Spec.Key &&
			command.FrozenIdempotencyKey != execution.IdempotencyKey) ||
		(command.BoundTaskID != "" && command.BoundTaskID != grant.TaskID) {
		return skillrun.Run{}, skillrun.Execution{}, executiongrant.Grant{}, chat.ErrTurnCheckpointConflict
	}
	return run, execution, grant, nil
}

// TaskIdentityForCall exposes the actual Skill step identity for Stop. It reads
// the owner/session/turn-scoped grant, even before a Task binding reached Chat.
func (e *Executor) TaskIdentityForCall(ctx context.Context, command chat.ToolCommand) (chat.ToolTaskIdentity, error) {
	if command.Call.Name != ToolCreateThreeView {
		return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
	}
	if e.skillRuntime == nil {
		return chat.ToolTaskIdentity{}, skillrun.ErrInvalid
	}
	// The call's frozen identity must be this run's first step; a persisted
	// Task must belong to this run, even when it is an older view of a
	// continuation (#243). Both refusals are checkpoint conflicts, not a reason
	// to cancel this call's current view.
	if command.FrozenIdempotencyKey != "" {
		_, first, _, err := e.skillRuntime.ResolveAgentStep(ctx, generation.OwnerID(command.OwnerID),
			command.SessionID, threeViewRunKey(command), command.AssistantMessageID)
		if errors.Is(err, skillrun.ErrMissing) {
			return chat.ToolTaskIdentity{}, nil
		}
		if err != nil {
			return chat.ToolTaskIdentity{}, errors.Join(chat.ErrTurnCheckpointConflict, err)
		}
		if first.IdempotencyKey != command.FrozenIdempotencyKey {
			return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
		}
	}
	if command.BoundTaskID != "" {
		_, _, grant, err := e.resolveThreeViewState(ctx, command)
		if errors.Is(err, skillrun.ErrMissing) {
			return chat.ToolTaskIdentity{}, nil
		}
		if err != nil {
			return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
		}
		if grant.Status != executiongrant.Bound || grant.TaskID != command.BoundTaskID {
			return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
		}
	}
	// Stop must cancel the view in flight, which may be a later one of a budget
	// continuation rather than the first (#243).
	_, execution, grant, err := e.skillRuntime.ResolveAgentCurrentStep(ctx, generation.OwnerID(command.OwnerID),
		command.SessionID, threeViewRunKey(command), command.AssistantMessageID)
	if errors.Is(err, skillrun.ErrMissing) {
		return chat.ToolTaskIdentity{}, nil
	}
	if err != nil {
		return chat.ToolTaskIdentity{}, errors.Join(chat.ErrTurnCheckpointConflict, err)
	}
	if execution.RunID == "" || execution.Step.Spec.Capability != generation.ImageToImage {
		return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
	}
	identity := chat.ToolTaskIdentity{IdempotencyKey: execution.IdempotencyKey}
	if grant.Status == executiongrant.Bound {
		identity.TaskID = grant.TaskID
	}
	return identity, nil
}

func (e *Executor) awaitThreeView(command chat.ToolCommand, taskID string, offering generation.Offering, err error) (chat.ToolResult, error) {
	deadline := e.threeViewWaitUntil(command, offering)
	if !time.Now().Before(deadline) {
		return chat.ToolResult{TaskID: taskID, Error: e.reconcileText(command)}, err
	}
	return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: deadline}, err
}

// threeViewWaitUntil is how long one view may take from now: the turn's
// existing deadline when it has one, else the Offering's job timeout plus the
// upstream queue allowance (#136).
func (e *Executor) threeViewWaitUntil(command chat.ToolCommand, offering generation.Offering) time.Time {
	if !command.TaskWaitDeadline.IsZero() {
		return command.TaskWaitDeadline
	}
	timeout := offering.Timeout
	if timeout <= 0 {
		timeout = e.taskObservationTimeout
	} else {
		timeout += offering.UpstreamQueueTimeout
	}
	return time.Now().Add(timeout)
}

// threeViewSubjectType validates the Agent's subject_type against the declared
// templates (#243). An absent value means the declared default; a historical
// Schema without subject types accepts the empty value only.
func (e *Executor) threeViewSubjectType(requested string) (string, string) {
	requested = strings.TrimSpace(requested)
	if len(e.threeView.SubjectTypes) == 0 {
		return requested, ""
	}
	wanted := requested
	if wanted == "" {
		wanted = e.threeView.DefaultSubjectType
	}
	choices := make([]string, 0, len(e.threeView.SubjectTypes))
	for _, subject := range e.threeView.SubjectTypes {
		choices = append(choices, subject.ID)
		if subject.ID == wanted {
			return wanted, ""
		}
	}
	return "", strings.NewReplacer("{subject}", requested, "{choices}", strings.Join(choices, ", ")).
		Replace(e.threeView.Messages.SubjectInvalid)
}

// continueThreeView advances a continuation (the budget plan's, #243, or the
// auto mode's, #663) after one view's Task finished: it settles and reviews
// that view, submits the next view the basis covers, and ends the tool call
// only when the group is complete
// or paused for a person. Accepted views travel to the Agent as same-turn
// pictures so it can check clothing and colors. Every view it submits counts
// against the turn's generation checkpoint on this call (#693); a view a
// person already started is waited for, not counted.
func (e *Executor) continueThreeView(ctx context.Context, command chat.ToolCommand, run skillrun.Run,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	// The call replays with the arguments it was made with: a four-view request
	// (include_right) also makes the right view when the session approves every
	// paid step (#663).
	// The value was checked when the group was created (threeViewIncludeRight).
	var args struct {
		IncludeRight json.RawMessage `json:"include_right"`
	}
	_ = json.Unmarshal(command.Call.Arguments, &args)
	includeRight, _ := threeViewIncludeRight(args.IncludeRight)
	scope, turn := e.scopeKey(command), command.AssistantMessageID
	remaining := e.generationRemaining(scope, turn, command.Call.ID, command.PriorGenerationCalls,
		command.OwnGenerationUnits, e.maxGeneration)
	result, err := e.threeViewService.ContinueWithin(ctx, command.OwnerID, command.SessionID, run.ID,
		command.BoundTaskID, turn, includeRight, remaining)
	if err != nil {
		return chat.ToolResult{TaskID: command.BoundTaskID}, err
	}
	messages := e.threeView.Messages
	if result.Awaiting {
		// A view the person started on the card: wait for it, then go on
		// (#689, #693). It is not this call's submission.
		return chat.ToolResult{TaskID: result.TaskID, AwaitTask: true,
			WaitUntil: e.threeViewWaitUntil(command, result.Task.Offering)}, nil
	}
	if result.TaskID != "" {
		e.addGenerationUnits(scope, turn, command.Call.ID, command.PriorGenerationCalls, command.OwnGenerationUnits, 1)
		if observe != nil {
			task := result.Task
			if err := observe(chat.ToolObservation{Registered: true, TaskID: result.TaskID,
				Capability: string(task.Capability), AIModel: task.Offering.ModelID,
				NativeQuote: task.NativeQuote, EstimatedCredits: legacyThreeViewCredits(task),
				ApprovalSource: "policy"}); err != nil {
				return e.awaitThreeView(command, result.TaskID, task.Offering, err)
			}
		}
		content := ""
		if result.Retried && result.MaximumAttempts == 0 {
			// A bounded continuation redraws until the turn's checkpoint (#693).
			content = strings.NewReplacer(
				"{view}", e.threeViewSlotLabel(result, result.View),
				"{attempt}", strconv.Itoa(result.Attempt),
				"{reasons}", result.FailedReasons,
			).Replace(messages.ConsistencyRedraw)
		} else if result.Retried {
			content = strings.NewReplacer(
				"{view}", e.threeViewSlotLabel(result, result.View),
				"{attempt}", strconv.Itoa(result.Attempt),
				"{maximum}", strconv.Itoa(result.MaximumAttempts),
				"{reasons}", result.FailedReasons,
			).Replace(messages.ConsistencyRetry)
		}
		return chat.ToolResult{TaskID: result.TaskID, AgentContent: content,
			AwaitTask: true, WaitUntil: e.threeViewWaitUntil(command, result.Task.Offering)}, nil
	}
	pictures := e.threeViewPictures(result)
	if result.Paused {
		content := ""
		if result.Reason == threeview.ReasonConsistencyExhausted {
			content = strings.NewReplacer("{view}", e.threeViewSlotLabel(result, result.View),
				"{reasons}", result.FailedReasons).Replace(messages.ConsistencyFailed)
		} else {
			reason := e.threeView.Continuation.Reasons[result.Reason]
			if reason == "" {
				reason = result.Reason
			}
			template := messages.AgentPaused
			if (result.Reason == threeview.ReasonViewAlreadyStarted ||
				result.Reason == threeview.ReasonViewAwaitingApproval) && messages.AgentPausedStarted != "" {
				// Not a failure: the view was started and waits for its
				// approval or its turn (#689); who started it is not said.
				template = messages.AgentPausedStarted
			}
			content = strings.NewReplacer("{run}", run.ID, "{view}", e.threeViewSlotLabel(result, result.View),
				"{reason}", reason).Replace(template)
		}
		return chat.ToolResult{AgentContent: content, Pictures: pictures}, nil
	}
	content := strings.NewReplacer("{run}", run.ID, "{views}", e.threeViewViewLabels(result),
		"{group}", run.ID, "{model_source}", e.threeView.ModelSourceStepKey,
		"{checklist}", e.threeViewChecklist(result)).Replace(messages.AgentCompleted)
	return chat.ToolResult{AgentContent: content, Pictures: pictures}, nil
}

// threeViewChecklist is the Agent's visual checklist for the finished views
// (#283), worded by Schema three_view.review. The automatic check compares
// silhouettes and colours; the Agent compares each view other than the front
// view with the front view, item by item, in the pictures it receives with
// the receipt. Empty when the Schema declares none or only the front view
// finished.
func (e *Executor) threeViewChecklist(result threeview.ContinuationResult) string {
	review := e.threeView.Review
	if len(review.Items) == 0 {
		return ""
	}
	views := make([]string, 0, len(result.Completed))
	notes := make([]string, 0, len(result.Completed))
	for _, view := range result.Completed {
		if view.Key == e.threeView.ModelSourceStepKey {
			continue // the front view is what the others are compared with
		}
		label := e.threeViewSlotLabel(result, view.Key)
		views = append(views, label)
		if note := review.ViewNotes[view.Key]; note != "" {
			notes = append(notes, strings.NewReplacer("{view}", label, "{note}", note).Replace(review.ViewNoteTemplate))
		}
	}
	if len(views) == 0 {
		return ""
	}
	items := make([]string, 0, len(review.Items))
	for _, item := range review.Items {
		items = append(items, strings.ReplaceAll(review.ItemTemplate, "{text}", item.Text))
	}
	separator := e.threeView.Multiview.ViewSeparator
	if separator == "" {
		separator = " / "
	}
	return strings.NewReplacer("{views}", strings.Join(views, separator),
		"{items}", strings.Join(items, "\n"), "{view_notes}", strings.Join(notes, "")).Replace(review.ChecklistTemplate)
}

func (e *Executor) threeViewSlotLabel(result threeview.ContinuationResult, key string) string {
	for _, slot := range result.Group.Slots {
		if slot.Key == key && slot.Label != "" {
			return slot.Label
		}
	}
	return key
}

func (e *Executor) threeViewViewLabels(result threeview.ContinuationResult) string {
	labels := make([]string, 0, len(result.Completed))
	for _, view := range result.Completed {
		labels = append(labels, e.threeViewSlotLabel(result, view.Key))
	}
	separator := e.threeView.Multiview.ViewSeparator
	if separator == "" {
		separator = " / "
	}
	return strings.Join(labels, separator)
}

func (e *Executor) threeViewPictures(result threeview.ContinuationResult) []chat.ToolPicture {
	template := e.context.AssetURLTemplate
	if template == "" {
		return nil
	}
	pictures := make([]chat.ToolPicture, 0, len(result.Completed))
	for _, view := range result.Completed {
		if view.TaskID == "" || view.AssetID == "" {
			continue
		}
		handle := e.threeViewSlotLabel(result, view.Key)
		url := strings.NewReplacer("{task_id}", view.TaskID, "{asset_id}", view.AssetID).Replace(template)
		pictures = append(pictures, chat.ToolPicture{Handle: handle, URL: url, Fresh: true})
	}
	return pictures
}

// observeThreeView returns the started-view receipt. It deliberately does not
// return AwaitTask: a lone create_three_view call ends its chat turn instead of
// waiting for the front view. The Task joins a turn's wait only when the call
// shares one assistant batch with a generation step that does wait (#189).
func (e *Executor) observeThreeView(ctx context.Context, command chat.ToolCommand, run skillrun.Run,
	execution skillrun.Execution, grant executiongrant.Grant, observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if grant.Status != executiongrant.Bound || grant.TaskID == "" {
		return chat.ToolResult{}, chat.ErrTurnCheckpointConflict
	}
	task, err := e.skillRuntime.Generation.VerifyPreparedTaskReplay(ctx, generation.SubmitPreparedCommand{
		OwnerID: execution.OwnerID, SessionID: execution.SessionID,
		PreparedInputID: execution.Step.PreparedInputID, IdempotencyKey: execution.IdempotencyKey,
		Harness:     execution.Harness,
		OperationID: execution.OperationID, PlanStepID: execution.PlanStepID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		return e.awaitThreeView(command, grant.TaskID, generation.Offering{}, err)
	}
	if task.ID != generation.TaskID(grant.TaskID) || task.OwnerID != execution.OwnerID || task.SessionID != execution.SessionID {
		return chat.ToolResult{TaskID: grant.TaskID}, chat.ErrTurnCheckpointConflict
	}
	if execution.Step.TaskID == "" {
		_, err = e.skillRuntime.Runs.BindTask(ctx, skillrun.TaskCommand{StepCommand: skillrun.StepCommand{
			OwnerID: execution.OwnerID, RunID: run.ID, StepKey: execution.Step.Spec.Key,
			OperationID: execution.OperationID, IdempotencyKey: execution.IdempotencyKey}, TaskID: task.ID})
		if err != nil {
			return e.awaitThreeView(command, grant.TaskID, task.Offering, err)
		}
	}
	if observe != nil {
		source := "human"
		// A production plan's approval is the session policy's too (#135).
		if grant.ApprovedByPolicy() || grant.ApprovedByBudget() {
			source = "policy"
		}
		if err := observe(chat.ToolObservation{Registered: true, TaskID: grant.TaskID,
			Capability: string(task.Capability), AIModel: task.Offering.ModelID,
			NativeQuote: task.NativeQuote, EstimatedCredits: legacyThreeViewCredits(task), ApprovalSource: source}); err != nil {
			return e.awaitThreeView(command, grant.TaskID, task.Offering, err)
		}
	}
	messages := e.threeView.Messages
	approval := messages.ApprovalHuman
	if grant.ApprovedByPolicy() || grant.ApprovedByBudget() {
		approval = messages.ApprovalPolicy
	}
	// #665: a run the server continues (the same predicate as continuationRun
	// in createThreeView) says so; the other receipt tells the Agent the views
	// wait on the card.
	template := messages.AgentStarted
	if grant.BudgetEligible && messages.AgentStartedContinuing != "" &&
		e.threeViewService != nil && e.threeViewService.ContinuationEnabled() {
		template = messages.AgentStartedContinuing
	}
	return chat.ToolResult{TaskID: grant.TaskID, AgentContent: strings.NewReplacer(
		"{run}", run.ID, "{view}", execution.Step.Spec.Label, "{approval}", approval,
		"{credits}", threeViewPriceText(task), "{task}", string(task.ID),
	).Replace(template)}, nil
}

// threeViewImages picks the source picture: handles (and the selection alias)
// first, then the indices and the current turn's picture. A refusal comes back
// as the Schema's wording for the Agent.
func (e *Executor) threeViewImages(command chat.ToolCommand, indices []int, tokens []string) ([]string, string) {
	policy := chat.NewHandlePolicy(e.context)
	if len(tokens) > 0 && len(command.Options.ReferenceImageIndices) == 0 {
		resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection, tokens, chat.AttachmentKindImage)
		if err != nil || len(resolved) == 0 {
			handle, reason := tokens[0], "unknown"
			var unknown *chat.UnknownHandleError
			if errors.As(err, &unknown) {
				handle, reason = unknown.Handle, unknown.Reason
			}
			return nil, strings.ReplaceAll(chat.HandleRefusalTemplate(e.context, handle, reason), "{handle}", handle)
		}
		urls := make([]string, 0, len(resolved))
		for _, item := range resolved {
			if item.URL != "" {
				urls = append(urls, item.URL)
			}
		}
		if len(urls) == 0 {
			// The handle names an object that has no picture to send.
			return nil, strings.ReplaceAll(e.context.UnknownHandleTemplate, "{handle}", tokens[0])
		}
		return urls, ""
	}
	urls := selectedThreeViewImages(command, indices)
	if len(urls) == 0 && len(command.Options.ReferenceImageIndices) == 0 && len(indices) == 0 {
		// "This image": with nothing named, the selected picture is the source.
		for _, attachment := range command.Options.Selection {
			if attachment.Kind == chat.AttachmentKindImage && attachment.URL != "" {
				urls = append(urls, attachment.URL)
			}
		}
	}
	return urls, ""
}

func selectedThreeViewImages(command chat.ToolCommand, argsIndices []int) []string {
	all := make([]string, 0)
	for _, message := range command.ReferenceHistory() {
		all = append(all, message.ImageURLs...)
	}
	indices := argsIndices
	if len(command.Options.ReferenceImageIndices) > 0 {
		indices = command.Options.ReferenceImageIndices
	}
	if len(indices) > 0 {
		result := make([]string, 0, len(indices))
		for _, index := range indices {
			if index < 1 || index > len(all) {
				return nil
			}
			result = append(result, all[index-1])
		}
		return result
	}
	if len(command.History) == 0 {
		return nil
	}
	last := command.History[len(command.History)-1]
	if last.Role != "user" {
		return nil
	}
	return append([]string(nil), last.ImageURLs...)
}
