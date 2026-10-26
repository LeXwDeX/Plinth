package generationtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

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
		return executor.processingPlanResult(command, config, existing)
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
		SourceTaskID: source.ID, SourceAssetID: asset}
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
		found, available, expired := false, false, false
		var compatible []selectedOffering
		for _, offering := range executor.gateway.Offerings(step.Capability) {
			if !offering.Enabled || offering.Domain != generation.DomainProcessing || !executor.gateway.OfferingAvailable(offering.ID) {
				continue
			}
			binding, bindErr := generation.BindModelSource(offering, previous, chosenAsset, config.SourceVariantPreference)
			if step.OfferingID != "" && step.OfferingID != offering.ID {
				if bindErr == nil {
					compatible = append(compatible, selectedOffering{offering: offering, binding: binding})
				}
				continue
			}
			available = true
			expired = expired || errors.Is(bindErr, generation.ErrSourceExpired)
			if bindErr == nil {
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
					return executor.unknownService(command, executor.fillText(command, choice.UnknownOffering, "{step}", string(step.Capability)), step.OfferingID)
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
		create.Steps = append(create.Steps, processing.StepRequest{OfferingID: chosen.offering.ID,
			ExpectedOfferingRevision: chosen.offering.Revision, Input: parameters})
		previous = previewProcessingOutput(chosen.offering, owner, command.SessionID)
		chosenAsset = ""
	}
	plan, err := executor.plans.Create(ctx, create)
	if errors.Is(err, processing.ErrInvalid) || errors.Is(err, processing.ErrConflict) {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", config.Plan.StepsArgument))
	}
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	for _, step := range answered {
		executor.settleChoice(ctx, command, step.capability, step.index, step.offering, useDefault)
	}
	return executor.processingPlanResult(command, config, plan)
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
	return executor.processingPlanResult(command, config, plan)
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
}

func (v processingPriceStepView) MarshalJSON() ([]byte, error) {
	type plain processingPriceStepView
	return schema.MarshalPriceProjection(plain(v), v.NativeQuote != nil, "estimated_credits")
}

func (executor *Executor) processingPlanResult(command chat.ToolCommand, config *catalog.ChatToolProcessingSchema, plan processing.Plan) (chat.ToolResult, error) {
	steps := make([]processingPriceStepView, 0, len(plan.Steps))
	var prices schema.PriceAccumulator
	for _, step := range plan.Steps {
		steps = append(steps, processingPriceStepView{Capability: step.Capability, OfferingID: step.OfferingID, Revision: step.Revision, Parameters: step.Input, EstimatedCredits: step.EstimatedCredits, NativeQuote: step.NativeQuote, State: step.State, TaskID: step.TaskID, Failure: step.Failure})
		if e := prices.Add(step.EstimatedCredits, step.NativeQuote); e != nil {
			return chat.ToolResult{NoSubmission: true}, e
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
	content, err := json.Marshal(payload)
	return chat.ToolResult{AgentContent: string(content), NoSubmission: true}, err
}
