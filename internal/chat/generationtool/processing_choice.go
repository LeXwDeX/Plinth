package generationtool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #321: when two or more available Offerings accept the source of a step and
// the call names none, a processing tool that declares a choice asks the
// person on an in-chat choice card instead of running the first one. Order:
// the person's choice first, otherwise the card, and the default (the first
// compatible Offering in Schema order) only when the person accepts it.

// SetChoices wires the store of the session's choice cards. Without it the
// tools keep the first compatible Offering, as before #321.
func (executor *Executor) SetChoices(store chat.ChoiceStore) { executor.choices = store }

// compatibleOfferings lists the available Offerings of the capability that
// accept the source, in Schema order, with their bindings.
func (executor *Executor) compatibleOfferings(
	capability generation.Capability, task generation.Task, asset generation.AssetID,
	processing *catalog.ChatToolProcessingSchema,
) []selectedOffering {
	var found []selectedOffering
	for _, offering := range executor.gateway.Offerings(capability) {
		if !offering.Enabled || !executor.gateway.OfferingAvailable(offering.ID) {
			continue
		}
		binding, err := generation.BindModelSource(offering, task, asset, processing.SourceVariantPreference)
		if err == nil {
			found = append(found, selectedOffering{offering: offering, binding: binding})
		}
	}
	return found
}

func rawBool(encoded json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(encoded, &value)
	return value
}

// choiceOption is one card option: the public names and Schema quote of the
// Offering and the arguments of the call that runs it.
func choiceOption(offering generation.Offering, arguments json.RawMessage) chat.ChoiceOption {
	option := chat.ChoiceOption{ID: string(offering.ID), ProviderName: offering.ProviderName, ModelName: offering.ModelName,
		CreditsMetered: offering.CreditsMetered, Arguments: arguments}
	if offering.NativePricing != nil {
		option.NativeQuote, _ = offering.DefaultNativeQuote(nil)
	} else {
		option.EstimatedCredits = offering.QuotedCredits()
	}
	return option
}

// withArguments returns the call's arguments changed by edit.
func withArguments(original json.RawMessage, edit func(map[string]any)) json.RawMessage {
	arguments := map[string]any{}
	_ = json.Unmarshal(original, &arguments)
	if arguments == nil {
		arguments = map[string]any{}
	}
	edit(arguments)
	encoded, _ := json.Marshal(arguments)
	return encoded
}

// askChoice stores the card and returns the tool result the Agent reads: it
// names the options and tells the Agent to ask, and nothing was submitted.
func (executor *Executor) askChoice(
	ctx context.Context, command chat.ToolCommand, capability generation.Capability, stepIndex int,
	candidates []generation.Offering, optionArguments func(generation.Offering) json.RawMessage, defaultArguments json.RawMessage,
) (chat.ToolResult, error) {
	config := command.Tool.Processing.Choice
	if limit := executor.chat.Choice.MaxOptions; limit > 1 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	choice := chat.Choice{
		OwnerID: command.OwnerID, SessionID: command.SessionID, AssistantMessageID: command.AssistantMessageID,
		CallID: command.Call.ID, ToolName: command.Tool.Name, Capability: string(capability), StepIndex: stepIndex,
		DefaultOptionID: string(candidates[0].ID), AllowDefault: config.DefaultArgument != "", DefaultArguments: defaultArguments,
		State: chat.ChoicePending, CreatedAt: executor.clock().UTC(),
	}
	localize := executor.localizeText(command)
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	views := make([]map[string]any, 0, len(candidates))
	for _, offering := range candidates {
		option := choiceOption(offering, optionArguments(offering))
		choice.Options = append(choice.Options, option)
		view := map[string]any{"offering_id": option.ID, "provider": text(option.ProviderName), "model": text(option.ModelName)}
		if option.NativeQuote != nil {
			// The native quote's unit label is Provider data, not Schema copy.
			view["native_quote"] = map[string]string{"total_amount": option.NativeQuote.TotalAmount, "unit_label": option.NativeQuote.UnitLabel}
		} else if option.CreditsMetered {
			view["estimated_credits"] = option.EstimatedCredits
		}
		views = append(views, view)
	}
	stored, err := executor.choices.CreateChoice(ctx, choice)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	content, _ := json.Marshal(map[string]any{
		command.Tool.ResultFields.Success: false,
		"choice_required":                 true,
		"choice_id":                       stored.ID,
		"step":                            string(capability),
		"offering_choices":                views,
		"default_offering_id":             stored.DefaultOptionID,
		"message":                         executor.fillText(command, config.Content, "{step}", string(capability)),
	})
	return chat.ToolResult{AgentContent: string(content), NoSubmission: true}, nil
}

// settleChoice records that the Agent ran an option of the session's open card
// for this tool and step after a typed reply, so the card shows the answer and
// cannot be answered again. A card answered on the card is already closed.
func (executor *Executor) settleChoice(ctx context.Context, command chat.ToolCommand, capability generation.Capability,
	stepIndex int, offering generation.OfferingID, useDefault bool) {
	if executor.choices == nil {
		return
	}
	choices, err := executor.choices.ListChoices(ctx, command.OwnerID, command.SessionID, executor.chat.Choice.ListLimit)
	if err != nil {
		return
	}
	for _, choice := range choices {
		if choice.State != chat.ChoicePending || choice.ToolName != command.Tool.Name ||
			choice.Capability != string(capability) || choice.StepIndex != stepIndex {
			continue
		}
		record := chat.ChoiceRecord{OptionID: string(offering), UseDefault: useDefault && string(offering) == choice.DefaultOptionID,
			Source: chat.ChoiceAnsweredByAgent, AnsweredAt: executor.clock().UTC()}
		if _, ok := choice.Option(record.OptionID); !ok {
			continue
		}
		if _, err := executor.choices.AnswerChoice(ctx, command.OwnerID, command.SessionID, choice.ID, record); err != nil &&
			!errors.Is(err, chat.ErrChoiceClosed) {
			return
		}
	}
}
