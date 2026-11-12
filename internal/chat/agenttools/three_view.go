package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
				return chat.ToolResult{AgentContent: "本轮已停止，三视图任务未提交。", NoSubmission: true}, nil
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
		return chat.ToolResult{AgentContent: "本轮已停止，三视图任务未提交。", NoSubmission: true}, nil
	}
	created := false
	if errors.Is(err, skillrun.ErrMissing) {
		// Mutable model/reference/recipe selection applies only to a new run.
		// Existing runs keep their original recipe and frozen source.
		if e.skillReferences == nil || e.skillOfferings == nil {
			return chat.ToolResult{Error: "three-view skill unavailable", NoSubmission: true}, nil
		}
		var args struct {
			ReferenceImageIndices []int    `json:"reference_image_indices"`
			ReferenceHandles      []string `json:"reference_handles"`
			SubjectType           string   `json:"subject_type"`
		}
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return chat.ToolResult{Error: "invalid three-view arguments", NoSubmission: true}, nil
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
		model := command.Options.ModelFor(toolDomain)
		if model == "" {
			return chat.ToolResult{AgentContent: "请先选择支持图生图的生成模型。", NoSubmission: true}, nil
		}
		var selected generation.Offering
		for _, offering := range e.skillOfferings.Offerings(generation.ImageToImage) {
			if offering.Enabled && e.skillOfferings.OfferingAvailable(offering.ID) &&
				(model == offering.ModelID || model == string(offering.ID)) {
				selected = offering
				break
			}
		}
		if selected.ID == "" {
			return chat.ToolResult{AgentContent: "所选模型没有可用的三视图图生图 Offering。", NoSubmission: true}, nil
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
			return chat.ToolResult{AgentContent: "所选图片不可用，请重新选择。", NoSubmission: true}, nil
		}
		revision := e.skillRecipes[threeViewRecipeID]
		if revision == "" {
			return chat.ToolResult{NoSubmission: true}, skillrun.ErrInvalid
		}
		// The frame of every view follows the source subject's shape instead
		// of the Offering's default square (#283). It is frozen with the run;
		// nothing here can fail the run, a source that cannot be measured keeps
		// the default frame.
		var frame map[string]json.RawMessage
		if e.threeViewService != nil {
			frame = e.threeViewService.FrameInput(ctx, generation.OwnerID(command.OwnerID), selected, subjectType, assets[0])
		}
		run, err = e.skillRuntime.Create(ctx, skillrun.CreateCommand{
			Harness: threeViewHarnessBinding(command),
			OwnerID: generation.OwnerID(command.OwnerID), SessionID: command.SessionID,
			RecipeID: threeViewRecipeID, ExpectedRevision: revision,
			SelectedOfferingID: selected.ID, SourceAssetID: assets[0],
			IdempotencyKey: threeViewRunKey(command), SubjectType: subjectType,
			StaticInput: frame,
		})
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
		created = true
	}
	if len(run.Steps) == 0 {
		return chat.ToolResult{NoSubmission: true}, skillrun.ErrConflict
	}
	canContinue := false
	if e.threeViewService != nil && e.threeViewService.ContinuationEnabled() && (created || grant.BudgetEligible) {
		canContinue, err = e.threeViewService.CanContinue(ctx, command.OwnerID, command.SessionID, run.ID)
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
	}
	stepKey := run.Steps[0].Spec.Key
	if _, _, err := e.skillRuntime.PrepareStep(ctx, generation.OwnerID(command.OwnerID), run.ID, stepKey); err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if canContinue {
		_, _, err = e.skillRuntime.RequestAgentGrantWithBudget(ctx, generation.OwnerID(command.OwnerID),
			run.ID, stepKey, command.AssistantMessageID)
	} else {
		_, _, err = e.skillRuntime.RequestAgentGrant(ctx, generation.OwnerID(command.OwnerID),
			run.ID, stepKey, command.AssistantMessageID)
	}
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	run, execution, grant, err = e.existingThreeView(ctx, command)
	if err != nil {
		return chat.ToolResult{}, err
	}
	switch grant.Status {
	case executiongrant.Pending:
		return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
	case executiongrant.Denied:
		return chat.ToolResult{AgentContent: "三视图生成未获批准；任务没有提交。", NoSubmission: true}, nil
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
		return chat.ToolResult{AgentContent: "本轮已停止，三视图任务未提交。", NoSubmission: true}, nil
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

func threeViewRunKey(command chat.ToolCommand) string {
	return "chat-skill:" + command.SessionID + ":" + command.Call.ID
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
	if run.RecipeID != threeViewRecipeID || execution.Step.Spec.Capability != generation.ImageToImage ||
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
		return chat.ToolResult{TaskID: taskID, Error: e.consentContents.reconcile}, err
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

// continueThreeView advances a budget continuation after one view's Task
// finished (#243): it settles and reviews that view, submits the next required
// one the plan covers, and ends the tool call only when the group is complete
// or paused for a person. Accepted views travel to the Agent as same-turn
// pictures so it can check clothing and colors.
func (e *Executor) continueThreeView(ctx context.Context, command chat.ToolCommand, run skillrun.Run,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	result, err := e.threeViewService.Continue(ctx, command.OwnerID, command.SessionID, run.ID,
		command.BoundTaskID, command.AssistantMessageID)
	if err != nil {
		return chat.ToolResult{TaskID: command.BoundTaskID}, err
	}
	messages := e.threeView.Messages
	if result.TaskID != "" {
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
		if result.Retried {
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
			content = strings.NewReplacer("{run}", run.ID, "{view}", e.threeViewSlotLabel(result, result.View),
				"{reason}", reason).Replace(messages.AgentPaused)
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
	return chat.ToolResult{TaskID: grant.TaskID, AgentContent: strings.NewReplacer(
		"{run}", run.ID, "{view}", execution.Step.Spec.Label, "{approval}", approval,
		"{credits}", threeViewPriceText(task), "{task}", string(task.ID),
	).Replace(messages.AgentStarted)}, nil
}

// threeViewImages picks the source picture: handles (and the selection alias)
// first, then the indices and the current turn's picture. A refusal comes back
// as the Schema's wording for the Agent.
func (e *Executor) threeViewImages(command chat.ToolCommand, indices []int, tokens []string) ([]string, string) {
	policy := chat.NewHandlePolicy(e.context)
	if len(tokens) > 0 && len(command.Options.ReferenceImageIndices) == 0 {
		resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection, tokens, chat.AttachmentKindImage)
		if err != nil || len(resolved) == 0 {
			handle, template := tokens[0], e.context.UnknownHandleTemplate
			var unknown *chat.UnknownHandleError
			if errors.As(err, &unknown) {
				handle = unknown.Handle
				if unknown.Reason == "kind" {
					template = e.context.WrongHandleKindTemplate
				}
			}
			return nil, strings.ReplaceAll(template, "{handle}", handle)
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
