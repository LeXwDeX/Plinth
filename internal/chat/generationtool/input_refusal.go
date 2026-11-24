package generationtool

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// inputRefusal turns a refused input into a tool failure carrying the real
// reason: a Schema refusal (a reference image over the model's limit even
// after compression, dimensions outside the declared range, a count out of
// range), a UserFacing temporary source condition, or a model the Provider
// cannot use (#276, #358). A model-input code keeps its existing error.*
// message. Nothing was submitted.
func (executor *Executor) inputRefusal(command chat.ToolCommand, err error) (chat.ToolResult, bool) {
	var refusal *generation.Error
	if !errors.As(err, &refusal) || refusal.Detail == "" {
		return chat.ToolResult{}, false
	}
	switch refusal.Code {
	case generation.ErrInvalidInput, generation.ErrInvalidModelInput,
		generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
	case generation.ErrUnavailable:
		if generation.UserFacingError(err) == nil {
			return chat.ToolResult{}, false
		}
	default:
		return chat.ToolResult{}, false
	}
	return executor.refusal(command, string(refusal.Code), executor.refusalDetail(command, err, refusal.Detail)), true
}

// refusalDetail is what the Agent is told about a refused step (#321 A). A
// kernel Detail is diagnostic, not public copy: it is returned only when its
// source marked it UserFacing. Otherwise an explicitly safe Provider
// SafeDetail wins, then a model error.* Schema message, then the broken count
// input rule of the Offering in Schema copy (its declared parameter names and
// bounds only), then the public invalid-parameters fallback. Upstream
// excerpts, IDs, internal causes and paths never reach the Agent, and the
// kernel detail is never appended.
func (executor *Executor) refusalDetail(command chat.ToolCommand, err error, _ string) string {
	if reason := generation.UserFacingError(err); reason != nil {
		return reason.LocalizedDetail(executor.localizeText(command))
	}
	if reason := providerSafeDetail(err); reason != "" {
		return executor.localized(command, reason)
	}
	if message, ok := executor.modelInputDetail(command, err); ok {
		return message
	}
	if message := executor.inputRuleDetail(command, err); message != "" {
		return message
	}
	fallback := executor.chat.Presentation.Messages.InvalidParameters
	if fallback == "" {
		return ""
	}
	return executor.localized(command, fallback)
}

func providerSafeDetail(err error) string {
	var provider *generation.ProviderError
	if errors.As(err, &provider) && provider.Failure != nil {
		return strings.TrimSpace(provider.Failure.SafeDetail)
	}
	return ""
}

// modelInputDetail is the existing error.* Schema copy for a model the
// Provider cannot use (#358). The code and its error.* message are kept
// instead of rewriting the failure as a generic source mismatch.
func (executor *Executor) modelInputDetail(command chat.ToolCommand, err error) (string, bool) {
	var refusal *generation.Error
	if !errors.As(err, &refusal) {
		return "", false
	}
	switch refusal.Code {
	case generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
	default:
		return "", false
	}
	if executor.texts != nil {
		if message := executor.texts.Message(command.Options.Locale, "error."+string(refusal.Code)); message != "" {
			return message, true
		}
	}
	// No localizer or no error.* copy: return the public chat fallback rather
	// than the model's internal diagnostic.
	fallback := executor.chat.Presentation.Messages.InvalidParameters
	if fallback == "" {
		return "", true
	}
	return executor.localized(command, fallback), true
}

// inputRuleSeparator joins the members of an input rule; it reads the same in
// every language the Schema writes.
const inputRuleSeparator = " / "

// inputRuleDetail is the Schema copy for a count input rule the kernel's
// normalization refused (for example "prompt or style_asset, at most one"),
// or "" when err is not one. It names the rule's declared parameters, the
// tool argument that carries each, the bound, how many the call gave and the
// rule's own Schema message: Schema facts only, never an input value.
func (executor *Executor) inputRuleDetail(command chat.ToolCommand, err error) string {
	var violation *schema.InputRuleViolation
	if !errors.As(err, &violation) {
		return ""
	}
	messages := executor.chat.Presentation.Messages
	template := messages.InputRuleTooMany
	if len(violation.Present) < violation.Minimum {
		template = messages.InputRuleTooFew
	}
	if template == "" {
		return ""
	}
	parameters := executor.violationParameters(violation)
	members := make([]string, 0, len(violation.Parameters))
	for _, name := range violation.Parameters {
		argument := ""
		if parameter, found := parameters[name]; found {
			argument = ruleArgument(executor.chat, command, parameter)
		}
		if argument == "" || messages.InputRuleMember == "" {
			members = append(members, name)
			continue
		}
		members = append(members, executor.fillText(command, messages.InputRuleMember, "{name}", name, "{argument}", argument))
	}
	reason := ""
	if violation.Message != "" && messages.InputRuleReason != "" {
		reason = executor.fillText(command, messages.InputRuleReason, "{reason}", executor.localized(command, violation.Message))
	}
	return executor.fillText(command, template,
		"{inputs}", strings.Join(members, inputRuleSeparator),
		"{maximum}", strconv.Itoa(violation.Maximum), "{minimum}", strconv.Itoa(violation.Minimum),
		"{given}", strconv.Itoa(len(violation.Present)), "{reason}", reason)
}

// violationParameters are the declared parameters of the refused Offering, by
// name; empty when the executor has no gateway to read the Offering from.
func (executor *Executor) violationParameters(violation *schema.InputRuleViolation) map[string]generation.Parameter {
	parameters := map[string]generation.Parameter{}
	if executor.gateway == nil {
		return parameters
	}
	for _, offering := range executor.gateway.Offerings(violation.Capability) {
		if offering.ID != violation.OfferingID {
			continue
		}
		for _, parameter := range offering.Parameters {
			parameters[parameter.Name] = parameter
		}
	}
	return parameters
}

// ruleArgument is the argument of this processing tool that carries the
// parameter, or "" when the tool declares none for it.
func ruleArgument(chatSchema catalog.ChatSchema, command chat.ToolCommand, parameter generation.Parameter) string {
	processing := command.Tool.Processing
	if processing == nil {
		return ""
	}
	switch {
	case parameter.Semantic == generation.SemanticReferenceImage && processing.ReferenceArgument != "":
		return chatSchema.Context.ReferenceHandlesArgument
	case parameter.Semantic == generation.SemanticPrompt && processing.PromptArgument != "":
		return processing.PromptArgument
	}
	if role, isSource := schema.SourceParameterRole(parameter); isSource && role == schema.SemanticSourceTask && parameter.Type == "task" {
		return processing.SourceTaskArgument
	}
	arguments := make([]string, 0, 1)
	for argument, name := range processing.ParameterArguments {
		if name == parameter.Name {
			arguments = append(arguments, argument)
		}
	}
	sort.Strings(arguments)
	if len(arguments) > 0 {
		return arguments[0]
	}
	return ""
}
