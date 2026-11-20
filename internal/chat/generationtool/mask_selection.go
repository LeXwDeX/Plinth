package generationtool

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// selectionMaskBinding is "bind" when the tool writes the frozen selection
// mask into the Offering's mask-semantic parameter (#244).
const selectionMaskBinding = "bind"

// offeringSupportsMask reports whether the Offering declares a mask-semantic
// parameter (the parameter the frozen mask can be written into).
func offeringSupportsMask(offering generation.Offering) bool {
	_, supported := semanticParameter(offering, generation.SemanticMask)
	return supported
}

// maskCapableOfferings lists the enabled, runtime-available Offerings of a
// capability that declare a mask parameter and accept this call's input shape.
func (executor *Executor) maskCapableOfferings(capability generation.Capability, input modelInput) []generation.Offering {
	var result []generation.Offering
	for _, offering := range executor.availableOfferings(capability) {
		if offeringSupportsMask(offering) && executor.acceptsModelInput(offering, input) {
			result = append(result, offering)
		}
	}
	return result
}

// maskModelSwitchNote says the step moved from the Offering the model-choice
// layers would otherwise have used to a mask-capable one because neither the
// person nor the Agent named a model (#244). It is derived from the frozen turn
// options and current Catalog, so recovery reports it the same way.
func (executor *Executor) maskModelSwitchNote(command chat.ToolCommand, offering generation.Offering) string {
	if !bindsMask(command) || !offeringSupportsMask(offering) {
		return ""
	}
	// A lock or the Agent's own model is a decision: the resolver refuses a
	// model that cannot bind the mask instead of switching, so a switch only
	// happens below them.
	if namedModel(command) != "" || strings.TrimSpace(command.Options.ExplicitModelFor(toolDomain(command))) != "" {
		return ""
	}
	template := executor.localized(command, executor.chat.Presentation.Messages.MaskModelSwitchNote)
	if template == "" {
		return ""
	}
	var available []generation.Offering
	for _, candidate := range executor.gateway.Offerings(offering.Capability) {
		if candidate.Enabled && executor.gateway.OfferingAvailable(candidate.ID) {
			available = append(available, candidate)
		}
	}
	base, found := chat.PreferredOffering(available)
	if !found || base.ID == offering.ID || offeringSupportsMask(base) {
		return ""
	}
	return strings.NewReplacer(
		"{from}", executor.localized(command, OfferingModelName(base)),
		"{to}", executor.localized(command, OfferingModelName(offering)),
	).Replace(template)
}

// namedModel is the Agent's own model argument of this call, "" when it named
// none.
func namedModel(command chat.ToolCommand) string {
	if command.Tool.ModelArgument == "" {
		return ""
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil {
		return ""
	}
	var named string
	_ = json.Unmarshal(arguments[command.Tool.ModelArgument], &named)
	return strings.TrimSpace(named)
}

// maskNotAppliedNote explains that the Agent's own mask argument was dropped
// because the turn carried no frozen selection mask (#244, keeping R1-7). It
// only applies to a tool that binds masks; a tool that ignores masks has
// nothing to explain.
func (executor *Executor) maskNotAppliedNote(command chat.ToolCommand, offering generation.Offering) string {
	if command.Tool.SelectionMask != selectionMaskBinding || command.Options.SelectionMask != nil {
		return ""
	}
	if agentMaskArgument(command) == "" || offeringSupportsMask(offering) {
		// The Agent's mask was applied (the Offering declares the parameter),
		// or there was none to drop.
		return ""
	}
	return executor.localized(command, executor.chat.Presentation.Messages.MaskNotAppliedNote)
}

// agentMaskArgument returns the Agent's own mask argument, or an empty string.
// The mask argument is the tool argument mapped to the mask semantic.
func agentMaskArgument(command chat.ToolCommand) string {
	argument := ""
	for candidate, semantic := range command.Tool.ArgumentMappings {
		if semantic == "mask" {
			argument = candidate
			break
		}
	}
	if argument == "" {
		return ""
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil {
		return ""
	}
	var value string
	if json.Unmarshal(arguments[argument], &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// bindSelectionMask writes the frozen selection mask into the Offering's mask
// parameter (#244). The Agent's own mask argument wins when it was applied; a
// tool that ignores masks never receives it.
func bindSelectionMask(
	command chat.ToolCommand,
	offering generation.Offering,
	input map[string]any,
	chosen map[string]bool,
) {
	if command.Tool.SelectionMask != selectionMaskBinding || command.Options.SelectionMask == nil {
		return
	}
	parameter, supported := semanticParameter(offering, generation.SemanticMask)
	if !supported {
		return
	}
	if _, set := input[parameter.Name]; set {
		return
	}
	input[parameter.Name] = command.Options.SelectionMask.MaskAssetID
	chosen[parameter.Name] = true
}
