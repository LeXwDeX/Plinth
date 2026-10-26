package generationtool

import (
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// referenceLimitNote is the Schema's statement that only the first pictures
// were used because the Offering's reference parameter takes fewer than the
// turn gave (#329: Meshy Image to 3D takes one; several views belong to a
// multi-image model). The pictures themselves are cut as before
// (truncateReferences).
func referenceLimitNote(tool catalog.ChatToolSchema, offering generation.Offering, total int, text func(string) string) string {
	if limit := referenceImageLimit(offering); limit > 0 && total > limit {
		return ReferenceLimitNote(tool, offering, limit, total, text)
	}
	return ""
}

// referenceLimitExceededError is the reason a preserve-policy Offering
// refuses selected reference pictures over its limit: Schema copy with the
// public label and both counts, marked UserFacing so the Agent gets the facts
// instead of a generic failure (#329). Nil when the Schema declares no text.
func referenceLimitExceededError(offering generation.Offering, parameter generation.Parameter, actual int) error {
	detail, values := offering.ExecutionMessages.ReferenceLimitExceededText(parameter.Label, actual, parameter.MaxItems)
	if detail == "" {
		return nil
	}
	return &generation.Error{
		Code:           generation.ErrInvalidInput,
		Operation:      "reference-selection",
		Detail:         detail,
		UserFacing:     true,
		DetailTemplate: offering.ExecutionMessages.ReferenceLimitExceeded,
		DetailValues:   values,
	}
}

// referenceImageLimit is how many pictures the Offering's reference-image list
// takes; 0 when it states no limit (or has no such list).
func referenceImageLimit(offering generation.Offering) int {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticReferenceImage && parameter.Type == "asset-array" && parameter.MaxItems > 0 {
			return parameter.MaxItems
		}
	}
	return 0
}

// referenceUse is how many reference pictures a step uses of the ones the
// turn gave, set only when it uses fewer (#329 E).
type referenceUse struct{ Used, Given int }

// ReferenceLimitNote writes the reference-limit note for a step of tool that
// used count of the total pictures given on offering; "" when the tool names
// no reference_note result, nothing was left out, or the Schema has no text.
// The text is the Offering's runtime.generation_messages pair (the
// one-picture form when count is 1), the same one the task API answers with
// and the composer shows (#329 note 99647). text maps a Schema text to the
// reader's language; nil keeps the Schema's own (the Agent's tool result).
// The pending-approval service writes the approval card and the finished
// result with it (#329 E).
func ReferenceLimitNote(tool catalog.ChatToolSchema, offering generation.Offering, count, total int, text func(string) string) string {
	if tool.ResultFields.ReferenceNote == "" || count <= 0 || total <= count {
		return ""
	}
	template, model := offering.ExecutionMessages.ReferenceLimitText(count), OfferingModelName(offering)
	if template == "" {
		return ""
	}
	if text != nil {
		template, model = text(template), text(model)
	}
	return generation.ReferenceLimit{Model: model, Count: count, Total: total}.Note(template)
}

// OfferingModelName is the name an Offering's model is shown by.
func OfferingModelName(offering generation.Offering) string {
	if offering.ModelName != "" {
		return offering.ModelName
	}
	return offering.ModelID
}

// ToolDescriptionNote is what a turn adds to a tool's description: how many
// reference pictures the model picked for the tool's domain uses (#329 E'),
// from the tool's reference_capacity_note. It is "" when the tool declares
// none, the turn has no model to run it on, or the model states no limit.
func (executor *Executor) ToolDescriptionNote(tool catalog.ChatToolSchema, options chat.TurnOptions) string {
	notes := tool.ReferenceCapacityNote
	if notes == (catalog.ChatToolReferenceCapacityNote{}) || tool.Capability == "" {
		return ""
	}
	command := chat.ToolCommand{Tool: tool, Options: options}
	model := executor.userModel(command)
	if model == "" && tool.RequireUserModel && !executor.fallbackModelAllowed(command) {
		return ""
	}
	offering, err := executor.selectOffering(tool.Capability, tool, model)
	if err != nil {
		return ""
	}
	limit := referenceImageLimit(offering)
	// The Agent repeats this name in its reply, so use the turn's frozen
	// locale as the reference-limit note does.
	modelLabel := executor.localized(command, OfferingModelName(offering))
	switch {
	case limit == 1:
		return strings.NewReplacer("{model}", modelLabel).Replace(notes.Single)
	case limit > 1:
		return strings.NewReplacer("{model}", modelLabel, "{count}", strconv.Itoa(limit)).Replace(notes.Multiple)
	}
	return ""
}
