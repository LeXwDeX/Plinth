package generationtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ProcessingPlanKey identifies a proposal, not a step execution. Include the
// turn identity: mock/model call IDs can repeat in a later turn. Its bounded
// size also fits the plan store when session/call IDs are long.
func ProcessingPlanKey(command chat.ToolCommand) string {
	identity, _ := json.Marshal([]string{command.SessionID, command.AssistantMessageID, command.Call.ID, command.IdempotencyKey()})
	digest := sha256.Sum256(identity)
	return "chat-plan:" + hex.EncodeToString(digest[:])
}

type proposedProcessingStep struct {
	Capability generation.Capability `json:"capability"`
	OfferingID generation.OfferingID `json:"offering_id,omitempty"`
	Parameters json.RawMessage       `json:"parameters"`
}

func (executor *Executor) executeProcessingPlan(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	config := command.Tool.Processing
	messages := config.Messages
	if executor.plans == nil || executor.planCatalog == nil {
		return executor.reject(command, messages.Unavailable)
	}
	owner := generation.OwnerID(command.OwnerID)
	key := ProcessingPlanKey(command)
	// Replay reads the persisted proposal before consulting today's source or
	// Catalog. It must not rebuild a finished chain or choose another Offering.
	existing, err := executor.plans.Get(ctx, owner, processing.PlanID(owner, key))
	if err == nil {
		if existing.Harness != turnHarnessBinding(command) {
			return executor.reject(command, executor.chat.Harness.RejectionMessage)
		}
		if existing.SessionID != command.SessionID {
			return executor.reject(command, config.Plan.NotFoundContent)
		}
		return executor.processingPlanResult(ctx, command, config, existing)
	}
	if !errors.Is(err, processing.ErrMissing) {
		return chat.ToolResult{NoSubmission: true}, err
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil || arguments == nil {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", "arguments must be an object"))
	}
	choice := config.Choice
	useDefault := false
	for name, value := range arguments {
		switch {
		case choice != nil && name == choice.DefaultArgument:
			if json.Unmarshal(value, &useDefault) != nil {
				return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", name))
			}
			continue
		}
		switch name {
		case config.SourceTaskArgument, config.SourceAssetArgument:
			var id string
			if name == "" || json.Unmarshal(value, &id) != nil {
				return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", name))
			}
		case config.Plan.StepsArgument:
		default:
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", name))
		}
	}
	var requested []proposedProcessingStep
	decoder := json.NewDecoder(strings.NewReader(string(arguments[config.Plan.StepsArgument])))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&requested) != nil || len(requested) == 0 || len(requested) > processing.MaxPlanSteps {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", config.Plan.StepsArgument))
	}
	source, asset, refusal, err := executor.resolveProcessingSource(ctx, command, arguments)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if refusal != nil {
		return executor.reject(command, refusal.message)
	}
	// A plan of processing steps is paid work: a plan on another model than
	// the selected one waits for the person (#321 D).
	var wanted []generation.Offering
	if messages.Service != nil {
		wanted = executor.wantedServices(requested[0].Capability, requested[0].OfferingID,
			executor.serviceMentions(ctx, command, requested[0].Capability))
	}
	if question := executor.sourceConfirmation(ctx, command, arguments, source, requested[0].Capability, wanted); question != nil {
		return executor.confirmSource(command, question)
	}
	if asset == "" {
		// Match the panel's source identity even for task-reference Offerings.
		// A selected model or a named file keeps its exact Asset; otherwise
		// prefer the Schema's saved model variants (GLB in the shipped schema).
		asset = preferredModelAsset(source, config.SourceVariantPreference)
	}
	create := processing.CreateCommand{Harness: turnHarnessBinding(command), OwnerID: owner, IdempotencyKey: key, SessionID: command.SessionID,
		SourceTaskID: source.ID, SourceAssetID: asset, Origin: processing.OriginAgent}
	// #251: the checkpoints and contingencies were approved in the session's
	// production plan; the plan tool only inherits them, so the Agent can
	// neither add a checkpoint nor loosen its thresholds here.
	inherited := executor.inheritedCheckpoints(ctx, command)
	connected := map[generation.Capability]bool{}
	previous, chosenAsset := source, asset
	type settled struct {
		capability generation.Capability
		index      int
		offering   generation.OfferingID
	}
	var answered []settled
	for stepIndex, step := range requested {
		if _, known := config.Steps[string(step.Capability)]; !known {
			return executor.reject(command, executor.fillText(command, messages.StepUnknown, "{step}", string(step.Capability)))
		}
		var chosen selectedOffering
		found, available, expired, incomplete := false, false, false, false
		var compatible []selectedOffering
		for _, offering := range executor.gateway.Offerings(step.Capability) {
			if !offering.Enabled || offering.Domain != generation.DomainProcessing || !executor.gateway.OfferingAvailable(offering.ID) {
				continue
			}
			binding, bindErr := generation.BindModelSource(offering, previous, chosenAsset, config.SourceVariantPreference)
			completable := bindErr == nil && planOfferingCompletable(step.Capability, offering, step.Parameters)
			incomplete = incomplete || (bindErr == nil && !completable)
			if step.OfferingID != "" && step.OfferingID != offering.ID {
				if completable {
					compatible = append(compatible, selectedOffering{offering: offering, binding: binding})
				}
				continue
			}
			available = true
			expired = expired || errors.Is(bindErr, generation.ErrSourceExpired)
			if completable {
				compatible = append(compatible, selectedOffering{offering: offering, binding: binding})
				if !found {
					chosen, found = selectedOffering{offering: offering, binding: binding}, true
				}
			}
		}
		if choice != nil && config.Messages.Service != nil {
			// #321 G: the service the person named (or this step asks for)
			// that cannot take the model is refused with its reason; no other
			// service is planned in its place without the person's answer.
			if step.OfferingID != "" {
				if _, exists := executor.availableOffering(step.Capability, step.OfferingID); !exists {
					return executor.refuseRequestedService(command, choice, step.Capability, step.OfferingID)
				}
			}
			mentions := executor.serviceMentions(ctx, command, step.Capability)
			wanted := executor.wantedServices(step.Capability, step.OfferingID, mentions)
			accepted := slices.DeleteFunc(slices.Clone(compatible), func(candidate selectedOffering) bool {
				return !slices.ContainsFunc(wanted, func(offering generation.Offering) bool { return offering.ID == candidate.offering.ID })
			})
			switch {
			case len(wanted) > 0 && (len(accepted) == 0 || (step.OfferingID != "" && !found)):
				return executor.serviceQuestion(ctx, command, step.Capability, previous, chosenAsset, wanted, false, nil, compatible)
			case step.OfferingID == "" && len(mentions.named) > 0:
				compatible, chosen, found = accepted, accepted[0], true
			}
			if found && bills(chosen.offering) && mentions.any() && !mentions.allows(chosen.offering.ID) {
				return executor.serviceQuestion(ctx, command, step.Capability, previous, chosenAsset, wanted,
					mentions.declined[chosen.offering.ID], &chosen.offering, compatible)
			}
		}
		if step.OfferingID != "" && !found && incomplete {
			// #251: a pinned Offering whose required input cannot come from the
			// plan's source and parameters (a reference-image retexture) is
			// refused with the tool that can run it instead.
			if offeringRequiresConversationInput(step.OfferingID, step.Capability, executor.gateway.Offerings(step.Capability)) {
				return executor.reject(command, messages.ReferenceUnsupported)
			}
			return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", string(step.Capability)))
		}
		if choice != nil && step.OfferingID != "" && !found {
			return executor.unknownService(command, executor.fillText(command, choice.UnknownOffering, "{step}", string(step.Capability)), step.OfferingID)
		}
		if choice != nil && step.OfferingID == "" && len(compatible) > 1 && !useDefault && executor.choices != nil {
			// #321: several services accept this step's source and the person
			// named none: ask on a choice card; no plan is created.
			offerings := make([]generation.Offering, 0, len(compatible))
			for _, candidate := range compatible {
				offerings = append(offerings, candidate.offering)
			}
			index := stepIndex
			return executor.askChoice(ctx, command, step.Capability, index, offerings,
				func(offering generation.Offering) json.RawMessage {
					return withArguments(command.Call.Arguments, func(values map[string]any) {
						delete(values, choice.DefaultArgument)
						if steps, ok := values[config.Plan.StepsArgument].([]any); ok && index < len(steps) {
							if item, ok := steps[index].(map[string]any); ok {
								item["offering_id"] = string(offering.ID)
							}
						}
					})
				},
				withArguments(command.Call.Arguments, func(values map[string]any) { values[choice.DefaultArgument] = true }))
		}
		if found && choice != nil && (step.OfferingID != "" || useDefault) {
			answered = append(answered, settled{step.Capability, stepIndex, chosen.offering.ID})
		}
		if !found {
			if available {
				if stepIndex == 0 {
					// The first step binds the real model: say why as the panel does (#326).
					if text := executor.noServiceText(ctx, command, step.Capability, source, asset); text != "" {
						return executor.reject(command, text)
					}
				}
				return executor.reject(command, sourceIncompatibleText(messages, step.Capability, expired, executor.localizeText(command)))
			}
			return executor.reject(command, executor.fillText(command, messages.NoOffering, "{step}", string(step.Capability)))
		}
		var input map[string]json.RawMessage
		if json.Unmarshal(step.Parameters, &input) != nil || input == nil {
			return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", "parameters must be an object"))
		}
		declared := make(map[string]generation.Parameter)
		for _, parameter := range chosen.offering.Parameters {
			declared[parameter.Name] = parameter
		}
		for name := range input {
			parameter, known := declared[name]
			_, isSource := schema.SourceParameterRole(parameter)
			if !known || isSource || (name == chosen.offering.ExtraConsentParameter()) {
				return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", name))
			}
		}
		// #129: the session purpose's budget fills the bounded parameters the
		// step left unset. A plan step carries no adjustment reason, so an
		// advised parameter keeps its default and the plan's note reports it.
		executor.applyPurposeValues(command, chosen.offering, input, true)
		input[chosen.binding.Parameter], _ = json.Marshal(chosen.binding.Value)
		encoded, _ := json.Marshal(input)
		encoded, consentErr := agentConsentInput(chosen.offering, encoded)
		if consentErr != nil {
			return chat.ToolResult{NoSubmission: true}, consentErr
		}
		_, normalized, normalizeErr := executor.planCatalog.Normalize(step.Capability, chosen.offering.ID, encoded)
		if normalizeErr != nil {
			var invalid *generation.Error
			if errors.As(normalizeErr, &invalid) {
				return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", invalid.Detail))
			}
			return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", string(step.Capability)))
		}
		if json.Unmarshal(normalized, &input) != nil {
			return chat.ToolResult{NoSubmission: true}, errors.New("plan input normalization failed")
		}
		for _, parameter := range chosen.offering.Parameters {
			if _, sourceParameter := schema.SourceParameterRole(parameter); sourceParameter {
				delete(input, parameter.Name)
			}
		}
		parameters, _ := json.Marshal(input)
		requestedStep := processing.StepRequest{OfferingID: chosen.offering.ID,
			ExpectedOfferingRevision: chosen.offering.Revision, Input: parameters}
		if !connected[step.Capability] {
			if checkpoint, declared := inherited[step.Capability]; declared {
				requestedStep.Checkpoint = &checkpoint
				connected[step.Capability] = true
			}
		}
		create.Steps = append(create.Steps, requestedStep)
		previous = previewProcessingOutput(chosen.offering, owner, command.SessionID)
		chosenAsset = ""
	}
	plan, err := executor.plans.Create(ctx, create)
	if refusal := generation.UserFacingError(err); refusal != nil && errors.Is(err, processing.ErrInvalid) {
		// The Schema's reason for refusing the whole plan before anything is
		// paid, for example a Remesh budget over a later step's source limit
		// with the compatible parameters (#121).
		return executor.rejectBecause(command, executor.fillText(command, messages.InvalidInput, "{detail}",
			refusal.LocalizedDetail(executor.localizeText(command))), err)
	}
	if errors.Is(err, processing.ErrInvalid) || errors.Is(err, processing.ErrConflict) {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", config.Plan.StepsArgument))
	}
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	for _, step := range answered {
		executor.settleChoice(ctx, command, step.capability, step.index, step.offering, useDefault)
	}
	return executor.processingPlanResult(ctx, command, config, plan)
}

// Only input-contract preview data. No task is created here.
func previewProcessingOutput(offering generation.Offering, owner generation.OwnerID, session string) generation.Task {
	asset := generation.Asset{ID: "preview", Role: modelRole, StorageKey: "preview", MediaType: "application/octet-stream"}
	for _, output := range offering.Outputs {
		if output.Role == modelRole && len(output.MediaTypes) > 0 {
			asset.MediaType = output.MediaTypes[0]
			break
		}
	}
	return generation.Task{ID: "preview", OwnerID: owner, SessionID: session, State: generation.TaskSucceeded,
		Capability: offering.Capability, Offering: offering, Assets: []generation.Asset{asset}}
}

// ReadProcessingPlan is owner AND session scoped. It returns no grant IDs,
// prepared hashes, provider checkpoints or upstream data. It never advances.
func (executor *Executor) ReadProcessingPlan(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	var tool catalog.ChatToolSchema
	for _, candidate := range executor.chat.Tools {
		if candidate.Processing != nil && candidate.Processing.Plan != nil {
			tool = candidate
			break
		}
	}
	if tool.Processing == nil {
		return chat.ToolResult{NoSubmission: true}, errors.New("plan tool schema unavailable")
	}
	config := tool.Processing
	command.Tool.ResultFields = tool.ResultFields
	if executor.plans == nil {
		return executor.reject(command, config.Messages.Unavailable)
	}
	var args struct {
		PlanID string `json:"plan_id"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if !json.Valid(command.Call.Arguments) || decoder.Decode(&args) != nil || args.PlanID == "" {
		return executor.reject(command, config.Plan.NotFoundContent)
	}
	plan, err := executor.plans.Get(ctx, generation.OwnerID(command.OwnerID), args.PlanID)
	if errors.Is(err, processing.ErrMissing) || (err == nil && plan.SessionID != command.SessionID) {
		return executor.reject(command, config.Plan.NotFoundContent)
	}
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	return executor.processingPlanResult(ctx, command, config, plan)
}

type processingPriceStepView struct {
	Capability       generation.Capability   `json:"capability"`
	OfferingID       generation.OfferingID   `json:"offering_id"`
	Revision         string                  `json:"revision"`
	Parameters       json.RawMessage         `json:"parameters"`
	EstimatedCredits int                     `json:"estimated_credits"`
	NativeQuote      *generation.NativeQuote `json:"native_quote,omitempty"`
	State            string                  `json:"state"`
	TaskID           generation.TaskID       `json:"task_id,omitempty"`
	Failure          string                  `json:"failure,omitempty"`
	// #251: the server-evaluated checkpoint, the revision provenance and the
	// actionable failure classification.
	Checkpoint        *processing.Checkpoint `json:"checkpoint,omitempty"`
	ContingencyID     string                 `json:"contingency_id,omitempty"`
	RevisionOperation string                 `json:"revision_operation,omitempty"`
	RevisionReason    string                 `json:"revision_reason,omitempty"`
	FailureCategory   string                 `json:"failure_category,omitempty"`
	NextAction        string                 `json:"next_action,omitempty"`
	FailureAdvice     string                 `json:"failure_advice,omitempty"`
}

func (v processingPriceStepView) MarshalJSON() ([]byte, error) {
	type plain processingPriceStepView
	return schema.MarshalPriceProjection(plain(v), v.NativeQuote != nil, "estimated_credits")
}

func (executor *Executor) processingPlanResult(ctx context.Context, command chat.ToolCommand, config *catalog.ChatToolProcessingSchema, plan processing.Plan) (chat.ToolResult, error) {
	steps := make([]processingPriceStepView, 0, len(plan.Steps))
	var prices schema.PriceAccumulator
	var overBudget []string
	for _, step := range plan.Steps {
		view := processingPriceStepView{Capability: step.Capability, OfferingID: step.OfferingID, Revision: step.Revision,
			Parameters: step.Input, EstimatedCredits: step.EstimatedCredits, NativeQuote: step.NativeQuote,
			State: step.State, TaskID: step.TaskID, Failure: step.Failure, Checkpoint: step.Checkpoint,
			ContingencyID: step.ContingencyID, RevisionOperation: step.RevisionOperation, RevisionReason: step.RevisionReason}
		if step.State == processing.Failed || step.State == processing.Paused || step.Failure != "" {
			report := executor.stepFailureReport(ctx, command, step)
			view.FailureCategory, view.NextAction = string(report.Category), string(report.Action)
			view.FailureAdvice = executor.processingFailureAdvice(report)
		}
		steps = append(steps, view)
		if e := prices.Add(step.EstimatedCredits, step.NativeQuote); e != nil {
			return chat.ToolResult{NoSubmission: true}, e
		}
		if command.Tool.ResultFields.PurposeBudgetNote != "" {
			for _, offering := range executor.gateway.Offerings(step.Capability) {
				if offering.ID == step.OfferingID {
					overBudget = append(overBudget, executor.purposeBudgetItems(command, offering, step.Input)...)
					break
				}
			}
		}
	}
	totals, e := prices.Totals()
	if e != nil {
		return chat.ToolResult{NoSubmission: true}, e
	}
	total := prices.Legacy
	note := config.Plan.CreatedContent
	if plan.Failure == processing.ReasonConsentRequired {
		note = config.Plan.WaitingContent
	}
	payload := map[string]any{command.Tool.ResultFields.Success: true,
		"plan_id": plan.ID, "session_id": plan.SessionID, "source_task_id": plan.SourceTaskID,
		"source_asset_id": plan.SourceAssetID, "state": plan.State, "failure": plan.Failure,
		"steps": steps, "estimated_credits": total, "message": note}
	if len(totals) > 0 {
		delete(payload, "estimated_credits")
		payload["price_totals"] = totals
	}
	// #129: the plan says which planned values are over the session purpose's
	// budget, as a single step's result does.
	if note := executor.purposeBudgetText(command, overBudget); note != "" {
		payload[command.Tool.ResultFields.PurposeBudgetNote] = note
	}
	// #251: the plan's failure is reported with its actionable category and a
	// suggested next action, never with internal detail or upstream text.
	if plan.Failure != "" {
		index, failed := planFailureStep(plan)
		report := executor.stepFailureReport(ctx, command, failed)
		detail := map[string]any{
			"code": plan.Failure, "step_index": index,
			"category": string(report.Category), "next_action": string(report.Action),
			"advice": executor.processingFailureAdvice(report), "retryable": report.Retryable,
			"user_decision_required": report.UserDecisionRequired,
		}
		if failed.Checkpoint != nil {
			detail["checkpoint"] = failed.Checkpoint
		}
		payload["failure_detail"] = detail
	}
	content, err := json.Marshal(payload)
	return chat.ToolResult{AgentContent: string(content), NoSubmission: true}, err
}

// inheritedCheckpoints reads the session's approved production plan and maps
// every declared checkpoint to its processing capability (#251). Without an
// active approved plan the plan tool adds no checkpoint.
func (executor *Executor) inheritedCheckpoints(ctx context.Context, command chat.ToolCommand) map[generation.Capability]processing.Checkpoint {
	if executor.grants == nil {
		return nil
	}
	summary, found, err := executor.grants.BudgetSummary(ctx, command.OwnerID, command.SessionID)
	if err != nil || !found || summary.State != budget.StateActive {
		return nil
	}
	inherited := make(map[generation.Capability]processing.Checkpoint, len(summary.Plan.Checkpoints))
	for _, checkpoint := range summary.Plan.Checkpoints {
		inherited[generation.Capability(checkpoint.AfterCapability)] = processing.Checkpoint{
			Checks: checkpoint.Checks, State: processing.CheckpointPending,
		}
	}
	return inherited
}

// planOfferingCompletable reports whether this plan tool can finish the
// Offering from the source and the step's ordinary parameters: every required
// input the Offering declares must be provided, fixed, defaulted or resolved
// by the plan (the source). A required reference picture or uploaded asset
// cannot be supplied by a processing plan.
func planOfferingCompletable(capability generation.Capability, offering generation.Offering, parameters json.RawMessage) bool {
	var input map[string]json.RawMessage
	if len(parameters) > 0 && json.Unmarshal(parameters, &input) != nil {
		return false
	}
	// The plan tool's reference image path belongs to retexture (#251): a
	// retexture that needs a picture is left to retexture_model. Other
	// capabilities keep their historical service choices.
	if capability != "retexture" {
		return true
	}
	for _, parameter := range offering.Parameters {
		if _, source := schema.SourceParameterRole(parameter); source || !parameter.Required ||
			parameter.Fixed || len(parameter.Default) > 0 {
			continue
		}
		if parameter.Semantic == schema.SemanticReferenceImage || parameter.Type == "asset" || parameter.Type == "asset-array" {
			return false
		}
		if _, provided := input[parameter.Name]; !provided {
			return false
		}
	}
	return true
}

// offeringRequiresConversationInput tells the refusal which tool can run a
// pinned Offering the plan cannot: a retexture that needs a reference picture
// goes through retexture_model.
func offeringRequiresConversationInput(id generation.OfferingID, capability generation.Capability,
	offerings []generation.Offering) bool {
	for _, offering := range offerings {
		if offering.ID != id || offering.Capability != capability {
			continue
		}
		for _, parameter := range offering.Parameters {
			if _, source := schema.SourceParameterRole(parameter); source || !parameter.Required {
				continue
			}
			if parameter.Semantic == schema.SemanticReferenceImage || parameter.Name == "style_asset" {
				return true
			}
		}
	}
	return false
}

// stepFailureReport classifies one step's failure, reading the Task's class
// when a Task exists. A read failure leaves the plan reason as the source.
func (executor *Executor) stepFailureReport(ctx context.Context, command chat.ToolCommand, step processing.Step) generation.FailureReport {
	var task *generation.Task
	if step.TaskID != "" {
		if failed, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), step.TaskID); err == nil {
			task = &failed
		}
	}
	return processing.ClassifyStepFailure(step.Failure, task)
}

func (executor *Executor) processingFailureAdvice(report generation.FailureReport) string {
	advice := executor.chat.Processing.Failures.Advice[string(report.Category)]
	if advice == "" {
		advice = string(report.Action)
	}
	if executor.texts != nil {
		// Advice is server-only Agent text; an unknown key keeps it as is.
		advice = executor.texts.Text("", advice)
	}
	return advice
}

// planFailureStep names the first step that carries the plan's failure.
func planFailureStep(plan processing.Plan) (int, processing.Step) {
	for index, step := range plan.Steps {
		if step.Failure != "" {
			return index, step
		}
	}
	for index, step := range plan.Steps {
		if step.Checkpoint != nil && step.Checkpoint.State == processing.CheckpointFailed {
			return index, step
		}
	}
	return -1, processing.Step{}
}
