package generationtool

import (
	"context"
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #612, #613: a purpose recommendation that differs from the model the
// workspace picker shows is confirmed by the person (#103 review C1). The
// confirmation used to be a refusal the Agent could only answer with its own
// ask_choice, so the person had no model to click in the chat. A 2D or 3D
// generation tool with a model argument now opens an in-chat model card (the
// #321 card store): one option per model the call can run, each replaying the
// call with its own model argument, so the answer reaches the same layers as
// any other model the Agent names. Nothing is submitted or charged. Every
// shape of every such tool goes through chooseOffering, so the rule is the same
// for a text-to-image and an image-to-image call.

// askModelChoice opens (or reuses) the model card of a workspace confirmation.
// It reports false when the executor has no card store or the Schema declares
// no card text; the caller then keeps the plain refusal.
func (executor *Executor) askModelChoice(ctx context.Context, command chat.ToolCommand,
	capability generation.Capability, card *modelCard) (chat.ToolResult, bool, error) {
	template := executor.chat.Presentation.Messages.ModelChoiceCard
	if executor.choices == nil || template == "" || card == nil || len(card.Candidates) == 0 ||
		command.Tool.ModelArgument == "" {
		return chat.ToolResult{}, false, nil
	}
	message := executor.workspaceChoiceText(command, template, card.Workspace, card.Proposed, card.Profile)
	executor.logRefusal(command, refusalModelWorkspaceConflict)
	// The calls of one reply that hit the same confirmation share its card:
	// a new card would supersede the one the person is reading.
	if open, found := executor.openModelCard(ctx, command, capability); found {
		return executor.modelCardResult(command, capability, open, message), true, nil
	}
	candidates := card.Candidates
	if limit := executor.chat.Choice.MaxOptions; limit > 1 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	choice := chat.Choice{
		OwnerID: command.OwnerID, SessionID: command.SessionID, AssistantMessageID: command.AssistantMessageID,
		CallID: command.Call.ID, ToolName: command.Tool.Name, Capability: string(capability), StepIndex: -1,
		DefaultOptionID: string(candidates[0].ID), State: chat.ChoicePending, CreatedAt: executor.clock().UTC(),
	}
	for _, offering := range candidates {
		id := string(offering.ID)
		option := choiceOption(offering, withArguments(command.Call.Arguments, func(values map[string]any) {
			values[command.Tool.ModelArgument] = id
		}))
		option.Recommended = card.Recommended[offering.ID]
		choice.Options = append(choice.Options, option)
	}
	stored, err := executor.choices.CreateChoice(ctx, choice)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, true, err
	}
	return executor.modelCardResult(command, capability, stored, message), true, nil
}

// openModelCard finds the model card this reply already opened for the tool
// and capability, while it still waits for the person.
func (executor *Executor) openModelCard(ctx context.Context, command chat.ToolCommand,
	capability generation.Capability) (chat.Choice, bool) {
	choices, err := executor.choices.ListChoices(ctx, command.OwnerID, command.SessionID, executor.chat.Choice.ListLimit)
	if err != nil {
		return chat.Choice{}, false
	}
	for _, choice := range choices {
		if choice.State == chat.ChoicePending && choice.Kind == "" && choice.StepIndex == -1 &&
			choice.AssistantMessageID == command.AssistantMessageID && choice.ToolName == command.Tool.Name &&
			choice.Capability == string(capability) {
			return choice, true
		}
	}
	return chat.Choice{}, false
}

// modelCardResult is what the Agent reads about the open card: the models by
// their public names, which the purpose recommends, and the Schema's text.
func (executor *Executor) modelCardResult(command chat.ToolCommand, capability generation.Capability,
	choice chat.Choice, message string) chat.ToolResult {
	localize := executor.localizeText(command)
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	views := make([]map[string]any, 0, len(choice.Options))
	for _, option := range choice.Options {
		view := map[string]any{"offering_id": option.ID, "provider": text(option.ProviderName), "model": text(option.ModelName)}
		if option.Recommended {
			view["recommended"] = true
		}
		if option.NativeQuote != nil {
			view["native_quote"] = map[string]string{"total_amount": option.NativeQuote.TotalAmount, "unit_label": option.NativeQuote.UnitLabel}
		} else if option.CreditsMetered {
			view["estimated_credits"] = option.EstimatedCredits
		}
		views = append(views, view)
	}
	content, _ := json.Marshal(map[string]any{
		command.Tool.ResultFields.Success: false,
		"choice_required":                 true,
		"choice_id":                       choice.ID,
		"step":                            string(capability),
		"offering_choices":                views,
		"message":                         message,
	})
	return chat.ToolResult{AgentContent: string(content), NoSubmission: true}
}
