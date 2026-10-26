package generationtool

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #321 D (notes 98949, 98950): what the person selected is used silently.
// When a call would run a paid step on another model than the one selected
// on the workspace, and the person neither named that model in their latest
// message nor answered the question about it, the tool asks first instead of
// letting the Agent pay for a model of its own choosing. The rule is held here,
// not only in the prompt, because a published Agent configuration may carry
// an older prompt.

// sourceQuestion is the question a call puts to the person before running
// on another model than the selected one: the text, and both models as the
// Agent should name them (#326 K, L).
type sourceQuestion struct {
	message            string
	selected, proposed map[string]any
}

// sourceConfirmation returns the question when the call must ask the person
// before running on source; nil when it may go on. wanted are the services
// the person (or the call) asked for: when one of them cannot take the
// selected model, the question says why (#326 K), whichever model the Agent
// tried, and lists only combinations that can be submitted.
func (executor *Executor) sourceConfirmation(ctx context.Context, command chat.ToolCommand, arguments map[string]json.RawMessage,
	source generation.Task, capability generation.Capability, wanted []generation.Offering) *sourceQuestion {
	processing := command.Tool.Processing
	template := processing.Messages.SourceConfirmation
	selectedTask, selectedAsset := selectedModelTask(command)
	if template == "" || selectedTask == "" || selectedTask == string(source.ID) {
		return nil
	}
	handles := map[string]string{}
	for _, message := range command.History {
		for _, attachment := range message.Attachments {
			if attachment.Kind == chat.AttachmentKindModel && attachment.Handle != "" && attachment.TaskID != "" {
				handles[attachment.Handle] = attachment.TaskID
			}
		}
	}
	names := func(raw string) bool {
		return raw != "" && (raw == string(source.ID) || handles[raw] == string(source.ID))
	}
	userRole := executor.chat.Roles.User
	latest := -1
	for index := len(command.History) - 1; index >= 0; index-- {
		if command.History[index].Role == userRole {
			latest = index
			break
		}
	}
	if latest >= 0 {
		// The person named the model in their latest message.
		message := command.History[latest]
		for _, attachment := range message.Attachments {
			if attachment.TaskID == string(source.ID) {
				return nil
			}
		}
		if strings.Contains(message.Content, string(source.ID)) {
			return nil
		}
		for handle, task := range handles {
			if task == string(source.ID) && mentions(message.Content, handle) {
				return nil
			}
		}
		// The person answered the question this tool asked about the model in
		// the Agent's previous reply (a call on it that submitted nothing).
		for index := latest - 1; index >= 0; index-- {
			previous := command.History[index]
			if previous.Role == userRole {
				break
			}
			for _, call := range previous.ToolCalls {
				if call.TaskID != "" || call.Name != command.Tool.Name {
					continue
				}
				var earlier map[string]json.RawMessage
				if json.Unmarshal(call.Arguments, &earlier) == nil && names(rawString(earlier[processing.SourceTaskArgument])) {
					return nil
				}
			}
		}
		// #321 H: the person answered the question a call of this tool
		// proposed this very model in (the model the refused one was made
		// from): one answer is the confirmation.
		if executor.proposalAnswered(ctx, command, latest, source, capability, handles) {
			return nil
		}
	}
	selected := selectedTask
	for _, attachment := range command.Options.Selection {
		if attachment.Kind == chat.AttachmentKindModel && attachment.TaskID == selectedTask && attachment.Handle != "" {
			selected = attachment.Handle
		}
	}
	proposed := rawString(arguments[processing.SourceTaskArgument])
	if !names(proposed) {
		proposed = string(source.ID)
		for handle, task := range handles {
			if task == string(source.ID) {
				proposed = handle
			}
		}
	}
	question := &sourceQuestion{}
	labels := executor.versionLabels(ctx, command.OwnerID, command.SessionID)
	prefer := processing.SourceVariantPreference
	question.proposed = sourceView(command, executor.modelDelivery, source, "", prefer, labels, executor.localizeText(command))
	question.proposed["handle"] = proposed
	reason, options := "", ""
	localize := executor.localizeText(command)
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	selectedModel, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), generation.TaskID(selectedTask))
	if err != nil || selectedModel.SessionID != command.SessionID {
		reason = text(processing.Messages.SelectedSourceIncompatible)
	} else {
		asset := generation.AssetID(selectedAsset)
		if asset != "" && !slices.ContainsFunc(selectedModel.Assets, func(candidate generation.Asset) bool { return candidate.ID == asset }) {
			asset = ""
		}
		question.selected = sourceView(command, executor.modelDelivery, selectedModel, asset, prefer, labels, executor.localizeText(command))
		question.selected["handle"] = selected
		takesSelected := executor.compatibleOfferings(capability, selectedModel, asset, processing)
		if len(takesSelected) == 0 {
			reason = text(processing.Messages.SelectedSourceIncompatible)
		}
		if service := processing.Messages.Service; service != nil && processing.Messages.SelectedServiceRefused != "" {
			// #326 K: why each asked-for service cannot take the selected
			// model, and the combinations that can actually be submitted.
			var reasons, listed []string
			takesSource := executor.compatibleOfferings(capability, source, "", processing)
			for _, offering := range wanted {
				if slices.ContainsFunc(takesSelected, func(candidate selectedOffering) bool { return candidate.offering.ID == offering.ID }) {
					continue
				}
				reasons = append(reasons, reasonText(service, offering, executor.refusalOf(offering, selectedModel, asset, prefer), ", ", localize))
				if slices.ContainsFunc(takesSource, func(candidate selectedOffering) bool { return candidate.offering.ID == offering.ID }) {
					listed = append(listed, executor.fillText(command, service.OptionService, "{service}", localizedServiceTitle(offering, localize), "{source}", proposed,
						"{cost}", costText(service, defaultQuote(offering, localize), localize)))
				}
			}
			if len(reasons) > 0 {
				reason += executor.fillText(command, processing.Messages.SelectedServiceRefused, "{reasons}",
					strings.Join(reasons, text(service.ReasonSeparator)))
			}
			for _, candidate := range takesSelected {
				listed = append(listed, executor.fillText(command, service.OptionService, "{service}", localizedServiceTitle(candidate.offering, localize), "{source}", selected,
					"{cost}", costText(service, defaultQuote(candidate.offering, localize), localize)))
			}
			if len(listed) > 0 {
				options = executor.fillText(command, service.Options, "{options}", strings.Join(listed, text(service.OptionSeparator)))
			}
		}
	}
	question.message = executor.fillText(command, template, "{reason}", reason, "{selected}", selected, "{source}", proposed, "{step}", string(capability),
		"{options}", options)
	return question
}

// mentions reports a handle written as its own token, so "@model1" does not
// match "@model12".
func mentions(text, handle string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], handle)
		if index < 0 {
			return false
		}
		end := offset + index + len(handle)
		if end >= len(text) || !strings.ContainsRune("0123456789", rune(text[end])) {
			return true
		}
		offset = end
	}
}

// bills reports whether running the Offering costs credits or native units.
func bills(offering generation.Offering) bool {
	return offering.NativePricing != nil || offering.EstimatedCredits > 0 || offering.Cost != nil || offering.CostPolicy != nil
}

// confirmSource refuses the call with the question to put to the person.
func (executor *Executor) confirmSource(command chat.ToolCommand, question *sourceQuestion) (chat.ToolResult, error) {
	executor.logger.Info("agent tool call waits for the person to confirm its source", "tool", command.Tool.Name,
		"call_id", command.Call.ID, "owner_id", command.OwnerID, "session_id", command.SessionID)
	fields := map[string]any{
		command.Tool.ResultFields.Success: false,
		command.Tool.ResultFields.Error:   question.message,
		"source_confirmation_required":    true,
		"proposed_source":                 question.proposed,
	}
	if question.selected != nil {
		fields["selected_source"] = question.selected
	}
	content, _ := json.Marshal(fields)
	return chat.ToolResult{Error: question.message, AgentContent: string(content), NoSubmission: true}, nil
}
