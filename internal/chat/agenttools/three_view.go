package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
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
	if err == nil && grant.Status == executiongrant.Bound {
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
	if errors.Is(err, skillrun.ErrMissing) {
		// Mutable model/reference/recipe selection applies only to a new run.
		// Existing runs keep their original recipe and frozen source.
		if e.skillReferences == nil || e.skillOfferings == nil {
			return chat.ToolResult{Error: "three-view skill unavailable", NoSubmission: true}, nil
		}
		var args struct {
			ReferenceImageIndices []int    `json:"reference_image_indices"`
			ReferenceHandles      []string `json:"reference_handles"`
		}
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return chat.ToolResult{Error: "invalid three-view arguments", NoSubmission: true}, nil
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
		assets, err := e.skillReferences.Resolve(ctx, generation.OwnerID(command.OwnerID), urls)
		if err != nil || len(assets) != 1 || assets[0] == "" {
			return chat.ToolResult{AgentContent: "所选图片不可用，请重新选择。", NoSubmission: true}, nil
		}
		revision := e.skillRecipes[threeViewRecipeID]
		if revision == "" {
			return chat.ToolResult{NoSubmission: true}, skillrun.ErrInvalid
		}
		run, err = e.skillRuntime.Create(ctx, skillrun.CreateCommand{
			Harness: threeViewHarnessBinding(command),
			OwnerID: generation.OwnerID(command.OwnerID), SessionID: command.SessionID,
			RecipeID: threeViewRecipeID, ExpectedRevision: revision,
			SelectedOfferingID: selected.ID, SourceAssetID: assets[0],
			IdempotencyKey: threeViewRunKey(command),
		})
		if err != nil {
			return chat.ToolResult{NoSubmission: true}, err
		}
	}
	if len(run.Steps) == 0 {
		return chat.ToolResult{NoSubmission: true}, skillrun.ErrConflict
	}
	stepKey := run.Steps[0].Spec.Key
	if _, _, err := e.skillRuntime.PrepareStep(ctx, generation.OwnerID(command.OwnerID), run.ID, stepKey); err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	_, _, err = e.skillRuntime.RequestAgentGrant(ctx, generation.OwnerID(command.OwnerID),
		run.ID, stepKey, command.AssistantMessageID)
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
		return chat.ToolResult{}, err
	}
	if accepted.TaskID == "" || len(run.Steps) == 0 {
		return chat.ToolResult{}, skillrun.ErrConflict
	}
	run, execution, grant, err = e.existingThreeView(ctx, command)
	if err != nil {
		return e.awaitThreeView(command, string(accepted.TaskID), generation.Offering{}, err)
	}
	return e.observeThreeView(ctx, command, run, execution, grant, observe)
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

// Read-only identity lookup is used by Stop before executable tool resolution.
// ResolveAgentStep still verifies the stored Run, Grant, turn and step identity.
func (e *Executor) readThreeView(ctx context.Context, command chat.ToolCommand) (skillrun.Run, skillrun.Execution, executiongrant.Grant, error) {
	run, execution, grant, err := e.skillRuntime.ResolveAgentStep(ctx, generation.OwnerID(command.OwnerID),
		command.SessionID, threeViewRunKey(command), command.AssistantMessageID)
	if err != nil {
		if errors.Is(err, skillrun.ErrConflict) {
			err = errors.Join(chat.ErrTurnCheckpointConflict, err)
		}
		return run, execution, grant, err
	}
	if run.RecipeID != threeViewRecipeID || execution.Step.Spec.Capability != generation.ImageToImage ||
		(command.FrozenIdempotencyKey != "" && command.FrozenIdempotencyKey != execution.IdempotencyKey) ||
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
	_, execution, grant, err := e.readThreeView(ctx, command)
	if errors.Is(err, skillrun.ErrMissing) {
		return chat.ToolTaskIdentity{}, nil
	}
	if err != nil {
		return chat.ToolTaskIdentity{}, err
	}
	identity := chat.ToolTaskIdentity{IdempotencyKey: execution.IdempotencyKey}
	if grant.Status == executiongrant.Bound {
		identity.TaskID = grant.TaskID
	}
	return identity, nil
}

func (e *Executor) awaitThreeView(command chat.ToolCommand, taskID string, offering generation.Offering, err error) (chat.ToolResult, error) {
	deadline := command.TaskWaitDeadline
	if deadline.IsZero() {
		timeout := offering.Timeout
		if timeout <= 0 {
			timeout = e.taskObservationTimeout
		}
		deadline = time.Now().Add(timeout)
	}
	if !time.Now().Before(deadline) {
		return chat.ToolResult{TaskID: taskID, Error: e.consentContents.reconcile}, err
	}
	return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: deadline}, err
}

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
		if grant.ApprovedByPolicy() {
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
	if grant.ApprovedByPolicy() {
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
		resolved, err := policy.ResolveHandles(command.History, command.Options.Selection, tokens, chat.AttachmentKindImage)
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
	for _, message := range command.History {
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
