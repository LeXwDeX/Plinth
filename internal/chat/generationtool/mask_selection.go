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

// ensureMaskOffering keeps a frozen selection mask from silently becoming a
// whole-image redraw (#244). When the tool binds the mask and the turn carries
// one, the chosen Offering must declare a mask parameter:
//
//   - a model the person locked or the Agent named is never replaced: the call
//     is refused with the Schema's words and the mask-capable choices;
//   - when nobody chose a model, the first mask-capable Offering that accepts
//     the input runs instead, and the result names the switch;
//   - when no Offering supports masks, nothing is submitted.
//
// A tool that ignores the mask, and a turn without one, are left untouched.
func (executor *Executor) ensureMaskOffering(
	command chat.ToolCommand,
	capability generation.Capability,
	input modelInput,
	offering generation.Offering,
) (generation.Offering, string) {
	if command.Tool.SelectionMask != selectionMaskBinding || command.Options.SelectionMask == nil ||
		offeringSupportsMask(offering) {
		return offering, ""
	}
	candidates := executor.maskCapableOfferings(capability, input)
	named := ""
	if command.Tool.ModelArgument != "" {
		var arguments map[string]json.RawMessage
		if json.Unmarshal(command.Call.Arguments, &arguments) == nil {
			_ = json.Unmarshal(arguments[command.Tool.ModelArgument], &named)
		}
		named = strings.TrimSpace(named)
	}
	if named != "" || strings.TrimSpace(command.Options.ExplicitModelFor(toolDomain(command))) != "" ||
		strings.TrimSpace(executor.userModel(command)) != "" {
		if len(candidates) == 0 {
			return offering, executor.localized(command, executor.chat.Presentation.Messages.MaskNoSupportedModel)
		}
		return offering, strings.NewReplacer(
			"{model}", executor.localized(command, OfferingModelName(offering)),
			"{choices}", executor.modelChoicesText(command, candidates),
		).Replace(executor.localized(command, executor.chat.Presentation.Messages.MaskUnsupportedTemplate))
	}
	if len(candidates) == 0 {
		return offering, executor.localized(command, executor.chat.Presentation.Messages.MaskNoSupportedModel)
	}
	return candidates[0], ""
}

// maskModelSwitchNote says the step moved from the Offering the model-choice
// layers would otherwise have used to a mask-capable one because the person
// had not chosen a model (#244). It is derived from the frozen turn options
// and current Catalog, so recovery reports it the same way.
func (executor *Executor) maskModelSwitchNote(command chat.ToolCommand, offering generation.Offering) string {
	if command.Tool.SelectionMask != selectionMaskBinding || command.Options.SelectionMask == nil ||
		executor.userModel(command) != "" || !offeringSupportsMask(offering) {
		return ""
	}
	template := executor.localized(command, executor.chat.Presentation.Messages.MaskModelSwitchNote)
	if template == "" {
		return ""
	}
	base, found := preferredOffering(executor.gateway.Offerings(offering.Capability), executor.gateway.OfferingAvailable)
	if !found || base.ID == offering.ID || offeringSupportsMask(base) {
		return ""
	}
	return strings.NewReplacer(
		"{from}", executor.localized(command, OfferingModelName(base)),
		"{to}", executor.localized(command, OfferingModelName(offering)),
	).Replace(template)
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
