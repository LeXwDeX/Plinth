package threeview

import (
	"context"
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// errViewNotFinished means the view's Task has not succeeded (still running,
// failed or canceled); it is not an inconsistency and never triggers a redraw
// by this package.
var errViewNotFinished = errors.New("threeview: view task did not succeed")

// Reason codes a continuation can stop with. The Schema words each one.
const (
	ReasonBudgetNotCovered   = "budget_not_covered"
	ReasonViewNotFinished    = "view_not_finished"
	ReasonViewAlreadyStarted = "view_already_started"
	// ReasonViewAwaitingApproval stops at a view whose grant waits for a
	// decision or for its submit: a person may have started it on the card,
	// or the continuation itself asked and the plan did not cover it. Neither
	// is a failure; the Schema words it without naming who started it (#689).
	ReasonViewAwaitingApproval = "view_awaiting_approval"
	ReasonConsistencyExhausted = "consistency_retries_exhausted"
	// ReasonGenerationCheckpoint stops a bounded continuation before it
	// submits past the turn's per-turn generation guard
	// (max_generation_calls_per_turn, #693). The name stays from #694; the
	// session's approval window (#702) pauses as ReasonViewAwaitingApproval.
	ReasonGenerationCheckpoint = "generation_checkpoint"
)

// ContinuationResult is what one continuation step did (#243): it either
// submitted the next view (TaskID), paused without submitting (Paused), or
// finished the required views (Completed). Retried says the submitted view is
// a redraw of the same rejected view. Awaiting says TaskID names a view that
// was already running when the continuation reached it (a person started it
// on the card, #689/#693): nothing was submitted, the call waits for it.
type ContinuationResult struct {
	Group           Group
	Completed       []View
	TaskID          string
	Task            generation.Task
	Retried         bool
	Attempt         int
	MaximumAttempts int
	Awaiting        bool
	Paused          bool
	Reason          string
	View            string
	FailedReasons   string
}

// ContinuationEnabled reports whether this Schema lets the server finish a
// group inside an approved budget.
func (s *Service) ContinuationEnabled() bool { return s.schema.Continuation.Enabled }

// CanContinue reports whether the server may submit this run's views by itself:
// the session approves every paid step (the auto mode, #663), or its active
// production plan explicitly covers every required view of the run (#243). The
// strict mode never continues. It never submits anything.
func (s *Service) CanContinue(ctx context.Context, owner, session, runID string) (bool, error) {
	if !s.schema.Continuation.Enabled {
		return false, nil
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" || strings.TrimSpace(runID) == "" {
		return false, ErrInvalid
	}
	run, err := s.runtime.Get(ctx, generation.OwnerID(owner), runID)
	if err != nil {
		return false, translate(err)
	}
	if run.OwnerID != generation.OwnerID(owner) || run.SessionID != session || !s.recipeKnown(run.RecipeID) {
		return false, ErrNotFound
	}
	steps := s.requiredSteps(run)
	if len(steps) < 2 {
		return false, nil
	}
	first := steps[0]
	for _, step := range steps {
		if step.Spec.Capability != first.Spec.Capability || step.Spec.OfferingID != first.Spec.OfferingID {
			return false, nil
		}
	}
	basis, err := s.runtime.Grants.Continuation(ctx, owner, session, string(first.Spec.Capability), string(first.Spec.OfferingID), len(steps))
	return basis != executiongrant.ContinuationNone, err
}

// Continue advances one group after a Task the Agent (or a person) started
// finished. It settles that view, compares a later view against the accepted
// front view, redraws only a rejected view within the declared attempts, and
// submits the next required view only when the budget plan approves it. It
// never submits a view outside the plan and never touches an accepted view.
func (s *Service) Continue(ctx context.Context, owner, session, runID, taskID, turnID string) (ContinuationResult, error) {
	return s.ContinueWith(ctx, owner, session, runID, taskID, turnID, false)
}

// ContinueWith is Continue for a four-view request: with includeRight, the
// optional right view is also made in sequence when the session approves every
// paid step (the auto mode, #663). A budget plan covers the required views only,
// so there the right view stays the person's choice on the card, as before.
func (s *Service) ContinueWith(ctx context.Context, owner, session, runID, taskID, turnID string,
	includeRight bool,
) (ContinuationResult, error) {
	return s.ContinueWithin(ctx, owner, session, runID, taskID, turnID, includeRight, -1)
}

// ContinueWithin is ContinueWith inside the turn's generation checkpoint
// (#693): remaining is how many more views the continuation may submit before
// the checkpoint, or negative for no bound. A bounded continuation (the auto
// mode, or the budget mode inside its plan) never pauses a group because a
// view stayed inconsistent: it keeps redrawing that view until the
// checkpoint, where it stops before submitting (ReasonGenerationCheckpoint).
// Without a bound a redraw stays within consistency.max_attempts_per_view.
func (s *Service) ContinueWithin(ctx context.Context, owner, session, runID, taskID, turnID string,
	includeRight bool, remaining int,
) (ContinuationResult, error) {
	if !s.schema.Continuation.Enabled {
		return ContinuationResult{}, ErrInvalid
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" ||
		strings.TrimSpace(runID) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(turnID) == "" {
		return ContinuationResult{}, ErrInvalid
	}
	run, err := s.runtime.Get(ctx, generation.OwnerID(owner), runID)
	if err != nil {
		return ContinuationResult{}, translate(err)
	}
	if run.OwnerID != generation.OwnerID(owner) || run.SessionID != session || !s.recipeKnown(run.RecipeID) {
		return ContinuationResult{}, ErrNotFound
	}
	// The Task the orchestrator replays may be an older view of the series
	// (its call keeps the first Task ID), so every finished view is settled
	// before the next decision, not only the one the call names.
	settled, err := s.settleCompleted(ctx, generation.OwnerID(owner), run)
	switch {
	case errors.Is(err, errViewNotFinished):
		result := ContinuationResult{Group: projectOrEmpty(ctx, s, run)}
		result.Paused, result.Reason = true, ReasonViewNotFinished
		return result, nil
	case err != nil:
		return ContinuationResult{}, err
	}
	steps := s.requiredSteps(settled)
	rightDropped := false
	if includeRight {
		basis, err := s.autoBasis(ctx, settled, steps)
		if err != nil {
			return ContinuationResult{}, err
		}
		if basis {
			steps = s.stepsWithOptional(settled, steps)
		} else {
			// The group started in the auto mode and the person switched away
			// from it: the right view no longer continues by itself (#665).
			rightDropped, err = s.startedOnPolicy(ctx, settled, steps)
			if err != nil {
				return ContinuationResult{}, err
			}
		}
	}
	result, err := s.advanceRequired(ctx, settled, turnID, steps, remaining)
	if err != nil || !rightDropped || result.Paused || result.TaskID != "" {
		return result, err
	}
	// The required views are done but the four-view request is not: say so
	// instead of reporting the group complete, so the Agent tells the person
	// the right view waits for them on the card (#665).
	for _, step := range s.stepsWithOptional(settled, nil) {
		if step.ReplacementAssetID == "" && !(step.Phase == skillrun.OutputReady && step.Review == skillrun.Accepted) {
			result.Paused, result.Reason, result.View = true, ReasonBudgetNotCovered, step.Spec.Key
			result.Completed = nil
			return result, nil
		}
	}
	return result, nil
}

// startedOnPolicy reports whether the group's first required view was approved
// by the auto mode's policy actor, so a four-view request was made on the auto
// basis. The strict and budget modes approve cheap views by policy too
// (policy:manual, policy:budget); such a group never had the right view on
// that basis and finishes as a three-view group (#665).
func (s *Service) startedOnPolicy(ctx context.Context, run skillrun.Run, required []skillrun.Step) (bool, error) {
	if len(required) == 0 || required[0].GrantRequestID == "" {
		return false, nil
	}
	grant, err := s.runtime.Grants.Grants.GetExecutionGrant(ctx, string(run.OwnerID), required[0].GrantRequestID)
	if err != nil {
		return false, err
	}
	return grant.Status != executiongrant.Pending &&
		grant.DecidedBy == executiongrant.PolicyActorPrefix+approvalpolicy.ModeAuto, nil
}

// autoBasis reports whether the session approves every paid step by itself, the
// one basis on which the optional right view continues with the others.
func (s *Service) autoBasis(ctx context.Context, run skillrun.Run, required []skillrun.Step) (bool, error) {
	if len(required) == 0 {
		return false, nil
	}
	first := required[0]
	basis, err := s.runtime.Grants.Continuation(ctx, string(run.OwnerID), run.SessionID,
		string(first.Spec.Capability), string(first.Spec.OfferingID), len(required))
	return basis == executiongrant.ContinuationAuto, err
}

// stepsWithOptional is the required views followed by the run's optional views
// in the multiview order (the right view).
func (s *Service) stepsWithOptional(run skillrun.Run, required []skillrun.Step) []skillrun.Step {
	steps := append([]skillrun.Step(nil), required...)
	for _, key := range s.schema.Multiview.ViewOrder {
		for _, step := range run.Steps {
			if step.Spec.Key == key && step.Spec.Optional {
				steps = append(steps, step)
				break
			}
		}
	}
	return steps
}

// advanceRequired walks the given views (the required ones, and with the auto
// mode's four-view request the right view) in order: it pauses on a view that
// did not finish, redraws the first rejected view within the attempt limit,
// submits the first planned view the active plan covers, waits for a view
// that is already running, and reports the group complete when every
// required view is accepted. remaining bounds the submissions (ContinueWithin).
func (s *Service) advanceRequired(ctx context.Context, run skillrun.Run, turnID string,
	steps []skillrun.Step, remaining int,
) (ContinuationResult, error) {
	owner := run.OwnerID
	for _, step := range steps {
		if step.ReplacementAssetID != "" {
			continue
		}
		switch step.Phase {
		case skillrun.OutputReady:
			switch step.Review {
			case skillrun.Accepted:
				continue
			case skillrun.Rejected:
				return s.continueRejected(ctx, run, step, ContinuationResult{View: step.Spec.Key, Attempt: step.Attempt}, turnID, remaining)
			default:
				updated, err := s.settleStep(ctx, owner, run, step.Spec.Key)
				if errors.Is(err, errViewNotFinished) {
					return pausedAt(ctx, s, run, step.Spec.Key, ReasonViewNotFinished)
				}
				if err != nil {
					return ContinuationResult{}, err
				}
				run = updated
				if index := stepIndexByKey(run, step.Spec.Key); index >= 0 {
					step = run.Steps[index]
				}
				if step.Review == skillrun.Rejected {
					return s.continueRejected(ctx, run, step, ContinuationResult{View: step.Spec.Key, Attempt: step.Attempt}, turnID, remaining)
				}
			}
		case skillrun.Planned:
			if remaining == 0 {
				return pausedAt(ctx, s, run, step.Spec.Key, ReasonGenerationCheckpoint)
			}
			submission, err := s.prepareAndSubmit(ctx, owner, run, step.Spec.Key, turnID)
			if err != nil {
				return ContinuationResult{}, err
			}
			result := ContinuationResult{View: step.Spec.Key}
			if submission.TaskID == "" {
				result.Paused, result.Reason = true, submission.Reason
				result.Group = projectOrEmpty(ctx, s, run)
				return result, nil
			}
			result.TaskID, result.Task = submission.TaskID, submission.Task
			result.Group = projectAfter(ctx, s, owner, run.ID, run)
			return result, nil
		case skillrun.TaskBound:
			if step.TaskID == "" {
				return pausedAt(ctx, s, run, step.Spec.Key, ReasonViewNotFinished)
			}
			task, err := s.runtime.Generation.Get(ctx, owner, step.TaskID)
			if err != nil {
				return ContinuationResult{}, err
			}
			if task.State == generation.TaskSucceeded {
				updated, err := s.settleStep(ctx, owner, run, step.Spec.Key)
				if err != nil && !errors.Is(err, errViewNotFinished) {
					return ContinuationResult{}, err
				}
				run = updated
				continue
			}
			if !task.State.Terminal() {
				// A view a person started on the card while the series ran is
				// not a failure (#689, #693): the call waits for it and goes
				// on with the views after it.
				result := ContinuationResult{View: step.Spec.Key, TaskID: string(step.TaskID), Task: task, Awaiting: true}
				result.Group = projectOrEmpty(ctx, s, run)
				return result, nil
			}
			return pausedAt(ctx, s, run, step.Spec.Key, ReasonViewNotFinished)
		case skillrun.GrantPending:
			return pausedAt(ctx, s, run, step.Spec.Key, ReasonViewAwaitingApproval)
		default:
			return pausedAt(ctx, s, run, step.Spec.Key, ReasonViewAlreadyStarted)
		}
	}
	return s.completed(ctx, run, ContinuationResult{})
}

func pausedAt(ctx context.Context, s *Service, run skillrun.Run, view, reason string) (ContinuationResult, error) {
	group, _ := s.project(ctx, run)
	return ContinuationResult{Group: group, Paused: true, Reason: reason, View: view}, nil
}

func projectOrEmpty(ctx context.Context, s *Service, run skillrun.Run) Group {
	group, _ := s.project(ctx, run)
	return group
}

func projectAfter(ctx context.Context, s *Service, owner generation.OwnerID, runID string, fallback skillrun.Run) Group {
	if current, err := s.runtime.Get(ctx, owner, runID); err == nil {
		if group, projectErr := s.project(ctx, current); projectErr == nil {
			return group
		}
	}
	return projectOrEmpty(ctx, s, fallback)
}

// continueRejected redraws a rejected view. Without a bound (remaining < 0)
// it stops at consistency.max_attempts_per_view; a bounded continuation
// redraws until the turn's generation checkpoint instead (#693).
func (s *Service) continueRejected(ctx context.Context, run skillrun.Run, step skillrun.Step,
	result ContinuationResult, turnID string, remaining int,
) (ContinuationResult, error) {
	owner := run.OwnerID
	consistency := s.consistencyForRecipe(run.RecipeID)
	maximum := consistency.MaxAttemptsPerView
	if maximum < 1 {
		maximum = 1
	}
	result.MaximumAttempts = maximum
	// The reasons describe the picture that was refused, so they are read
	// before the step is restarted. They reach the Agent in both the redraw
	// and the exhausted case (#283); a picture that cannot be read again
	// leaves them empty.
	result.FailedReasons, _ = s.rejectionReasons(ctx, owner, run, step)
	switch {
	case remaining == 0:
		result.Paused, result.Reason = true, ReasonGenerationCheckpoint
		result.Group, _ = s.project(ctx, run)
		return result, nil
	case remaining < 0 && step.Attempt+1 >= maximum:
		result.Paused, result.Reason = true, ReasonConsistencyExhausted
		result.Group, _ = s.project(ctx, run)
		return result, nil
	case remaining > 0:
		// No attempt limit here: the checkpoint bounds the redraws.
		result.MaximumAttempts = 0
	}
	if _, err := s.runtime.RestartStep(ctx, owner, run.ID, step.Spec.Key, step.Attempt); err != nil {
		return result, translate(err)
	}
	run, err := s.runtime.Get(ctx, owner, run.ID)
	if err != nil {
		return result, translate(err)
	}
	submission, err := s.prepareAndSubmit(ctx, owner, run, step.Spec.Key, turnID)
	if err != nil {
		return result, err
	}
	if submission.TaskID == "" {
		result.Paused, result.Reason = true, submission.Reason
		result.Group, _ = s.project(ctx, run)
		return result, nil
	}
	result.Retried, result.Attempt, result.TaskID, result.Task = true, step.Attempt+1, submission.TaskID, submission.Task
	if current, err := s.runtime.Get(ctx, owner, run.ID); err == nil {
		if projected, projectErr := s.project(ctx, current); projectErr == nil {
			result.Group = projected
		}
	}
	return result, nil
}

type submission struct {
	TaskID string
	Task   generation.Task
	Reason string
}

// prepareAndSubmit prepares one view's frozen input, requests its grant, and
// submits it only when the basis the series continues on approved the grant: the
// active plan (budget mode) or the session policy (auto mode, #663). A grant
// left pending for a person stops as awaiting approval; a denial or an approval
// from the other source stops as not covered. None of them submits.
func (s *Service) prepareAndSubmit(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	stepKey, turnID string,
) (submission, error) {
	index := stepIndexByKey(run, stepKey)
	if index < 0 {
		return submission{}, skillrun.ErrMissing
	}
	step := run.Steps[index]
	required := s.requiredSteps(run)
	basis, err := s.runtime.Grants.Continuation(ctx, string(owner), run.SessionID,
		string(step.Spec.Capability), string(step.Spec.OfferingID), len(required))
	if err != nil {
		return submission{}, err
	}
	if basis == executiongrant.ContinuationNone {
		return submission{Reason: ReasonBudgetNotCovered}, nil
	}
	if _, _, err := s.runtime.PrepareStep(ctx, owner, run.ID, stepKey); err != nil {
		return submission{}, translate(err)
	}
	_, grant, err := s.runtime.RequestAgentGrantWithBudget(ctx, owner, run.ID, stepKey, turnID)
	if err != nil {
		return submission{}, translate(err)
	}
	// A grant left pending for a person (the auto or budget approval window,
	// #702) is not a failure: the view waits on its card.
	if grant.Status == executiongrant.Pending {
		return submission{Reason: ReasonViewAwaitingApproval}, nil
	}
	// The grant must come from the basis the series continues on: the plan's
	// budget, or (auto mode) the session policy. A denial or an approval from
	// the other source stops here.
	approved := grant.Status == executiongrant.Approved &&
		((basis == executiongrant.ContinuationBudget && grant.ApprovedByBudget()) ||
			(basis == executiongrant.ContinuationAuto && grant.ApprovedByPolicy()))
	if !approved {
		return submission{Reason: ReasonBudgetNotCovered}, nil
	}
	_, accepted, err := s.runtime.SubmitStep(context.WithoutCancel(ctx), owner, run.ID, stepKey)
	if err != nil {
		return submission{}, translate(err)
	}
	if accepted.TaskID == "" {
		return submission{}, skillrun.ErrConflict
	}
	task, err := s.runtime.Generation.Get(ctx, owner, accepted.TaskID)
	if err != nil {
		return submission{}, err
	}
	return submission{TaskID: string(accepted.TaskID), Task: task}, nil
}

// completed projects a group whose required views are all settled and
// accepted.
func (s *Service) completed(ctx context.Context, run skillrun.Run, result ContinuationResult) (ContinuationResult, error) {
	group, err := s.project(ctx, run)
	if err != nil {
		return result, err
	}
	result.Group, result.TaskID, result.View = group, "", ""
	result.Completed = acceptedViews(group)
	return result, nil
}

// requiredSteps lists the run's required views in the multiview view order.
func (s *Service) requiredSteps(run skillrun.Run) []skillrun.Step {
	order := s.schema.Multiview.ViewOrder
	if len(order) == 0 {
		order = make([]string, 0, len(run.Steps))
		for _, step := range run.Steps {
			order = append(order, step.Spec.Key)
		}
	}
	steps := make([]skillrun.Step, 0, len(order))
	seen := map[string]bool{}
	for _, key := range order {
		if seen[key] {
			continue
		}
		seen[key] = true
		for _, step := range run.Steps {
			if step.Spec.Key == key && !step.Spec.Optional {
				steps = append(steps, step)
				break
			}
		}
	}
	return steps
}

// settleSource settles the chain of views the named view depends on and
// proves its immediate source is finally usable (#243). A source whose
// consistency check rejected it, or whose Task did not succeed, refuses the
// dependent view.
func (s *Service) settleSource(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	viewKey string,
) (skillrun.Run, error) {
	index := stepIndexByKey(run, viewKey)
	if index < 0 {
		return run, skillrun.ErrMissing
	}
	step := run.Steps[index]
	for step.Spec.SourceStepKey != "" {
		sourceIndex := stepIndexByKey(run, step.Spec.SourceStepKey)
		if sourceIndex < 0 {
			return run, skillrun.ErrConflict
		}
		source := run.Steps[sourceIndex]
		if source.ReplacementAssetID == "" {
			updated, err := s.settleStep(ctx, owner, run, source.Spec.Key)
			if errors.Is(err, errViewNotFinished) {
				return run, skillrun.ErrConflict
			}
			if err != nil {
				return run, err
			}
			run = updated
			source = run.Steps[sourceIndex]
		}
		if source.ReplacementAssetID == "" &&
			(source.Phase != skillrun.OutputReady || source.Review != skillrun.Accepted) {
			return run, skillrun.ErrConflict
		}
		step = source
	}
	return run, nil
}

// settleCompleted completes every finished Task of the run and reviews it. It
// skips Tasks that are still running, failed or canceled.
func (s *Service) settleCompleted(ctx context.Context, owner generation.OwnerID, run skillrun.Run) (skillrun.Run, error) {
	for _, step := range run.Steps {
		if step.Phase != skillrun.TaskBound || step.TaskID == "" {
			continue
		}
		updated, err := s.settleStep(ctx, owner, run, step.Spec.Key)
		if errors.Is(err, errViewNotFinished) {
			continue
		}
		if err != nil {
			return run, err
		}
		run = updated
	}
	return run, nil
}

// settleStep records a succeeded Task's output and, once, applies the automatic
// review. The front view passes when it is a usable single subject; a later
// view passes when it matches the accepted front view within the declared
// consistency bounds (#243).
func (s *Service) settleStep(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	stepKey string,
) (skillrun.Run, error) {
	index := stepIndexByKey(run, stepKey)
	if index < 0 {
		return run, skillrun.ErrMissing
	}
	step := run.Steps[index]
	if step.ReplacementAssetID != "" {
		return run, nil
	}
	if step.Phase == skillrun.TaskBound {
		if step.TaskID == "" {
			return run, skillrun.ErrConflict
		}
		task, err := s.runtime.Generation.Get(ctx, owner, step.TaskID)
		if err != nil {
			return run, err
		}
		if task.SessionID != run.SessionID {
			return run, skillrun.ErrConflict
		}
		if task.State != generation.TaskSucceeded {
			return run, errViewNotFinished
		}
		updated, err := s.runtime.SyncOutput(ctx, owner, run.ID, stepKey)
		if err != nil {
			return run, translate(err)
		}
		run = updated
		step = run.Steps[index]
	}
	if step.Phase != skillrun.OutputReady || step.Review != skillrun.Unreviewed {
		return run, nil
	}
	decision := skillrun.Accepted
	if s.assets != nil {
		var err error
		decision, err = s.judge(ctx, owner, run, step)
		if err != nil {
			return run, err
		}
	}
	reviewed, err := s.runtime.Review(ctx, skillrun.ReviewCommand{
		OwnerID: owner, RunID: run.ID, StepKey: stepKey, Decision: decision,
	})
	if err != nil {
		return run, translate(err)
	}
	return reviewed, nil
}

// judge returns the automatic decision for one finished view: rejected when
// any of its checks failed (#243, #283).
func (s *Service) judge(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	step skillrun.Step,
) (skillrun.ReviewStatus, error) {
	checks, err := s.viewChecks(ctx, owner, run, step)
	if err != nil {
		return skillrun.Unreviewed, err
	}
	for _, check := range checks {
		if check.Status == imageinspect.StatusFail {
			return skillrun.Rejected, nil
		}
	}
	return skillrun.Accepted, nil
}

// rejectionReasons words the failed checks of one rejected view with the
// Schema's reasons.
func (s *Service) rejectionReasons(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	step skillrun.Step,
) (string, error) {
	if s.assets == nil {
		return "", nil // no picture reader, so the view was rejected by hand: there is no check to word
	}
	checks, err := s.viewChecks(ctx, owner, run, step)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(checks))
	consistency := s.consistencyForRecipe(run.RecipeID)
	for _, check := range checks {
		if check.Status == imageinspect.StatusFail {
			parts = append(parts, s.reasonText(check, consistency))
		}
	}
	separator := consistency.ReasonSeparator
	if separator == "" {
		separator = "; "
	}
	return strings.Join(parts, separator), nil
}

func stepIndexByKey(run skillrun.Run, key string) int {
	for index, step := range run.Steps {
		if step.Spec.Key == key {
			return index
		}
	}
	return -1
}

func stepIndexByTask(run skillrun.Run, taskID string) int {
	for index, step := range run.Steps {
		if string(step.TaskID) == taskID {
			return index
		}
	}
	return -1
}

// acceptedViews lists the finished views in recipe order for the Agent's
// same-turn pictures: the required ones and any finished optional one.
func acceptedViews(group Group) []View {
	views := make([]View, 0, len(group.Slots))
	for _, slot := range group.Slots {
		// A finished optional view (the right view of a four-view request, #663) is a
		// finished view too: the Agent's receipt and its checking pictures include it.
		if slot.Status != Succeeded || slot.OutputAssetID == "" {
			continue
		}
		view := View{Key: slot.Key, TaskID: slot.TaskID, AssetID: slot.OutputAssetID}
		if slot.Replaced {
			view.TaskID = ""
		}
		views = append(views, view)
	}
	return views
}
