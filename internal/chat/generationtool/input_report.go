package generationtool

import (
	"fmt"
	"sort"
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
// types come from the Offering's own declaration; values are never echoed.
func (executor *Executor) inputGaps(command chat.ToolCommand, offering generation.Offering, input map[string]any) []string {
	var gaps []string
	for _, parameter := range offering.Parameters {
		if !parameter.Required || len(parameter.Default) > 0 || parameter.Fixed || parameter.Name == offering.ExtraConsentParameter() {
			continue
		}
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			continue
		}
		if parameter.Presentation != nil && parameter.Presentation.Surface == schema.SurfaceHidden {
			continue
		}
		if value, present := input[parameter.Name]; present && !emptyInput(value) {
			continue
		}
		gaps = append(gaps, fmt.Sprintf("missing required input %q (%s)%s: %s", parameter.Name, describeType(parameter),
			describeOptions(parameter), executor.supplyHint(command, parameter)))
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

func describeType(parameter generation.Parameter) string {
	kind := parameter.Type
	switch {
	case parameter.Semantic == generation.SemanticReferenceImage:
		kind = "image from this chat"
	case parameter.Type == "text" && parameter.MaxLength > 0:
		kind = fmt.Sprintf("text, at most %d characters", parameter.MaxLength)
	}
	if parameter.Label != "" {
		kind = parameter.Label + ", " + kind
	}
	return kind
}

func describeOptions(parameter generation.Parameter) string {
	if len(parameter.Options) == 0 || len(parameter.Options) > maxReportedOptions {
		return ""
	}
	values := make([]string, 0, len(parameter.Options))
	for _, option := range parameter.Options {
		values = append(values, option.Value)
	}
	return " allowed values: " + strings.Join(values, ", ")
}

// supplyHint says which argument of this tool carries the input.
func (executor *Executor) supplyHint(command chat.ToolCommand, parameter generation.Parameter) string {
	processing := command.Tool.Processing
	switch {
	case parameter.Semantic == generation.SemanticReferenceImage && processing != nil && processing.ReferenceArgument != "":
		return fmt.Sprintf("pass one image of this chat in %s (a handle such as @img1); if the person gave none, ask them to attach one", executor.chat.Context.ReferenceHandlesArgument)
	case parameter.Semantic == generation.SemanticPrompt && processing != nil && processing.PromptArgument != "":
		return "pass it in " + processing.PromptArgument
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
			return "pass it in " + arguments[0]
		}
	}
	return "this tool cannot set it; tell the person this service needs it"
}

// inputShape describes what was sent without any value: field names with a
// type or size only (no text, URLs or IDs).
func inputShape(input map[string]any) string {
	names := make([]string, 0, len(input))
	for name := range input {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		switch value := input[name].(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s=text(%d chars)", name, len([]rune(value))))
		case bool:
			parts = append(parts, name+"=boolean")
		case float64, int, int64:
			parts = append(parts, name+"=number")
		case []any:
			parts = append(parts, fmt.Sprintf("%s=list(%d)", name, len(value)))
		default:
			parts = append(parts, name+"=value")
		}
	}
	return "fields sent: " + strings.Join(parts, ", ")
}
