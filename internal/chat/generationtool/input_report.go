package generationtool

import (
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// maxReportedOptions bounds the allowed values listed for one field.
const maxReportedOptions = 12

// inputGaps names every required input of the chosen Offering the tool call
// did not supply, with its type, how this tool takes it and (for a short
// enumeration) the allowed values, so the Agent can correct the call itself
// instead of receiving a generic "invalid parameters" (#92). The names and
// types come from the Offering's own declaration; values are never echoed. The
// sentences are Schema copy (chat.presentation.messages.input_gap_*, in the
// turn's language, A3 #121); this code only fills their placeholders.
func (executor *Executor) inputGaps(command chat.ToolCommand, offering generation.Offering, input map[string]any) []string {
	var gaps []string
	for _, parameter := range offering.Parameters {
		if !parameter.Required || len(parameter.Default) > 0 || parameter.Name == offering.ExtraConsentParameter() ||
			!schema.StepInputParameter(parameter) {
			continue
		}
		if value, present := input[parameter.Name]; present && !emptyInput(value) {
			continue
		}
		gaps = append(gaps, fill(executor.localized(command, executor.chat.Presentation.Messages.InputGapMissing),
			"{name}", parameter.Name, "{type}", executor.describeType(command, parameter),
			"{options}", executor.describeOptions(command, parameter), "{supply}", executor.supplyHint(command, parameter)))
	}
	return gaps
}

func emptyInput(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	}
	return false
}

func (executor *Executor) describeType(command chat.ToolCommand, parameter generation.Parameter) string {
	messages := executor.chat.Presentation.Messages
	kind := parameter.Type
	switch {
	case parameter.Semantic == generation.SemanticReferenceImage:
		kind = executor.localized(command, messages.InputGapReferenceImage)
	case parameter.Type == "text" && parameter.MaxLength > 0:
		kind = fill(executor.localized(command, messages.InputGapTextLimit), "{max}", strconv.Itoa(parameter.MaxLength))
	}
	if parameter.Label != "" {
		kind = fill(executor.localized(command, messages.InputGapLabeled), "{label}", executor.localized(command, parameter.Label), "{type}", kind)
	}
	return kind
}

func (executor *Executor) describeOptions(command chat.ToolCommand, parameter generation.Parameter) string {
	if len(parameter.Options) == 0 || len(parameter.Options) > maxReportedOptions {
		return ""
	}
	values := make([]string, 0, len(parameter.Options))
	for _, option := range parameter.Options {
		values = append(values, option.Value)
	}
	return fill(executor.localized(command, executor.chat.Presentation.Messages.InputGapOptions), "{values}", strings.Join(values, ", "))
}

// supplyHint says which argument of this tool carries the input.
func (executor *Executor) supplyHint(command chat.ToolCommand, parameter generation.Parameter) string {
	messages := executor.chat.Presentation.Messages
	processing := command.Tool.Processing
	switch {
	case parameter.Semantic == generation.SemanticReferenceImage && processing != nil && processing.ReferenceArgument != "":
		return fill(executor.localized(command, messages.InputGapSupplyReference), "{argument}", executor.chat.Context.ReferenceHandlesArgument)
	case parameter.Semantic == generation.SemanticPrompt && processing != nil && processing.PromptArgument != "":
		return fill(executor.localized(command, messages.InputGapSupplyArgument), "{argument}", processing.PromptArgument)
	}
	if processing != nil {
		arguments := make([]string, 0, 1)
		for argument, name := range processing.ParameterArguments {
			if name == parameter.Name {
				arguments = append(arguments, argument)
			}
		}
		sort.Strings(arguments)
		if len(arguments) > 0 {
			return fill(executor.localized(command, messages.InputGapSupplyArgument), "{argument}", arguments[0])
		}
	}
	return executor.localized(command, messages.InputGapSupplyUnavailable)
}

// inputShape describes what was sent without any value: field names with a
// type or size only (no text, URLs or IDs). The wording is Schema copy.
func (executor *Executor) inputShape(command chat.ToolCommand, input map[string]any) string {
	messages := executor.chat.Presentation.Messages
	names := make([]string, 0, len(input))
	for name := range input {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		switch value := input[name].(type) {
		case string:
			parts = append(parts, fill(executor.localized(command, messages.InputShapeText), "{name}", name, "{count}", strconv.Itoa(len([]rune(value)))))
		case bool:
			parts = append(parts, fill(executor.localized(command, messages.InputShapeBoolean), "{name}", name))
		case float64, int, int64:
			parts = append(parts, fill(executor.localized(command, messages.InputShapeNumber), "{name}", name))
		case []any:
			parts = append(parts, fill(executor.localized(command, messages.InputShapeList), "{name}", name, "{count}", strconv.Itoa(len(value))))
		default:
			parts = append(parts, fill(executor.localized(command, messages.InputShapeValue), "{name}", name))
		}
	}
	return fill(executor.localized(command, messages.InputShape), "{fields}", strings.Join(parts, ", "))
}
