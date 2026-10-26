package generationtool

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// selectsOfferingByArgument reports whether the tool picks its Offering from
// an argument instead of the workspace model (#189).
func selectsOfferingByArgument(tool catalog.ChatToolSchema) bool {
	return tool.OfferingArgument != "" || tool.OfferingFromSource != ""
}

// toolOfferings are the enabled, runtime-available Offerings of the tool's
// declared capabilities, in catalog order.
func (executor *Executor) toolOfferings(tool catalog.ChatToolSchema) []generation.Offering {
	var result []generation.Offering
	for _, capability := range tool.Execution.Capabilities {
		for _, offering := range executor.gateway.Offerings(generation.Capability(capability)) {
			if offering.Capability == generation.Capability(capability) && offering.Enabled &&
				executor.gateway.OfferingAvailable(offering.ID) {
				result = append(result, offering)
			}
		}
	}
	return result
}

// selectArgumentOffering resolves the Offering named by the tool arguments.
// A refusal is a message for the Agent: nothing was submitted and the call can
// be corrected. An error means the arguments could not be read at all.
func (executor *Executor) selectArgumentOffering(
	ctx context.Context,
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
) (generation.Offering, string, error) {
	tool := command.Tool
	available := executor.toolOfferings(tool)
	if tool.OfferingArgument != "" {
		var choice string
		_ = json.Unmarshal(arguments[tool.OfferingArgument], &choice)
		declared := declaredChoices(tool)
		// Only a declared template is a choice: the tool's capabilities also hold
		// Offerings (such as build stages) that the argument never names.
		var choices []string
		for _, value := range declared {
			for _, offering := range available {
				if offering.ModelID != value {
					continue
				}
				if value == choice {
					return offering, "", nil
				}
				choices = append(choices, value)
				break
			}
		}
		return generation.Offering{}, strings.ReplaceAll(
			executor.localized(command, executor.chat.Presentation.Messages.OfferingChoiceUnavailable),
			"{choices}", strings.Join(choices, ", ")), nil
	}
	var token string
	if err := json.Unmarshal(arguments[tool.OfferingFromSource], &token); err != nil || strings.TrimSpace(token) == "" {
		return generation.Offering{}, executor.unpairedRefusal(ctx, command, available, false), nil
	}
	candidates, canvas := executor.sourceCandidates(command, token)
	// Exactly one named Task must pair: the artboard snapshot names the Tasks
	// of its picture layers (#189), and a layer without one (an upload, a
	// drawing) or another session's Task simply does not pair.
	var chosen generation.TaskID
	var matched generation.Offering
	for _, candidate := range candidates {
		if candidate == chosen {
			continue
		}
		offering, ok := executor.pairedOffering(ctx, command, available, candidate)
		if !ok {
			continue
		}
		if chosen != "" {
			return generation.Offering{}, executor.unpairedRefusal(ctx, command, available, canvas), nil
		}
		chosen, matched = candidate, offering
	}
	if chosen == "" {
		return generation.Offering{}, executor.unpairedRefusal(ctx, command, available, canvas), nil
	}
	// The resolved Task replaces the handle, so the frozen input names the Task
	// that was paired, not whatever the handle resolves to later.
	encoded, _ := json.Marshal(string(chosen))
	arguments[tool.OfferingFromSource] = encoded
	return matched, "", nil
}

// sourceCandidates are the Tasks a source argument names: the Task ID itself,
// or the Task of the image a handle (or @selected) resolves to. The turn's
// artboard snapshot has no Task of its own; it names the verified Tasks of the
// picture layers it flattens, and canvas reports that it was named.
func (executor *Executor) sourceCandidates(command chat.ToolCommand, token string) (candidates []generation.TaskID, canvas bool) {
	policy := executor.handlePolicy()
	if _, _, handle := policy.Parse(token); !handle && !policy.IsSelectionAlias(token) {
		return []generation.TaskID{generation.TaskID(token)}, false
	}
	resolved, err := policy.ResolveHandles(command.History, command.Options.Selection, []string{token}, chat.AttachmentKindImage)
	if err != nil {
		return nil, false
	}
	for _, item := range resolved {
		attachment := item.Attachment
		switch {
		case attachment.TaskID != "":
			candidates = append(candidates, generation.TaskID(attachment.TaskID))
		case attachment.Role == chat.AttachmentRoleCanvas && attachment.Canvas != nil:
			canvas = true
			for _, source := range attachment.Canvas.Sources {
				candidates = append(candidates, generation.TaskID(source))
			}
		case attachment.Role == chat.AttachmentRoleCanvas:
			canvas = true
		}
	}
	return candidates, canvas
}

// pairedOffering is the one available Offering whose source parameter accepts
// the Task: only the owner's own succeeded Task of this session pairs, and
// Generation checks the same identity again (and the frozen source) at
// Prepare and Start.
func (executor *Executor) pairedOffering(
	ctx context.Context,
	command chat.ToolCommand,
	available []generation.Offering,
	id generation.TaskID,
) (generation.Offering, bool) {
	source, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), id)
	if err != nil || source.OwnerID != generation.OwnerID(command.OwnerID) || source.SessionID != command.SessionID ||
		source.State != generation.TaskSucceeded {
		return generation.Offering{}, false
	}
	target := command.Tool.OverrideMappings[catalogSourceTaskFromImageHandle]
	var matched []generation.Offering
	for _, offering := range available {
		for _, parameter := range offering.Parameters {
			if parameter.Name == target && parameter.Type == "task" && parameter.Semantic == generation.SemanticSourceTask &&
				slices.Contains(parameter.TaskOfferings, source.Offering.ID) {
				matched = append(matched, offering)
				break
			}
		}
	}
	if len(matched) != 1 {
		return generation.Offering{}, false
	}
	return matched[0], true
}

// maximumSourceCandidates bounds the handles a refusal names.
const maximumSourceCandidates = 5

// unpairedRefusal says why nothing was submitted and, when this session has
// images whose Task would pair, names their handles so the Agent can call
// again with one of them (newest first).
func (executor *Executor) unpairedRefusal(ctx context.Context, command chat.ToolCommand, available []generation.Offering, canvas bool) string {
	messages := executor.chat.Presentation.Messages
	refusal := executor.localized(command, messages.SourceOfferingUnpaired)
	if canvas {
		refusal = executor.localized(command, messages.SourceOfferingCanvas)
	}
	var handles []string
	seen := map[string]bool{}
	for index := len(command.History) - 1; index >= 0 && len(handles) < maximumSourceCandidates; index-- {
		attachments := command.History[index].Attachments
		for position := len(attachments) - 1; position >= 0 && len(handles) < maximumSourceCandidates; position-- {
			attachment := attachments[position]
			if attachment.Kind != chat.AttachmentKindImage || attachment.TaskID == "" || attachment.Handle == "" || seen[attachment.TaskID] {
				continue
			}
			seen[attachment.TaskID] = true
			if _, ok := executor.pairedOffering(ctx, command, available, generation.TaskID(attachment.TaskID)); ok {
				handles = append(handles, attachment.Handle)
			}
		}
	}
	if len(handles) > 0 {
		refusal += strings.ReplaceAll(executor.localized(command, messages.SourceOfferingCandidates),
			"{handles}", strings.Join(handles, ", "))
	}
	return refusal
}

const catalogSourceTaskFromImageHandle = "source-task-from-image-handle"

// declaredChoices is the enum of the tool's offering argument, in order.
func declaredChoices(tool catalog.ChatToolSchema) []string {
	var parameters struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		return nil
	}
	return parameters.Properties[tool.OfferingArgument].Enum
}
