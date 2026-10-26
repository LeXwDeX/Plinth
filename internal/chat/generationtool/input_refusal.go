package generationtool

import (
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
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
	result := executor.failure(command, "", executor.refusalDetail(command, err, refusal.Detail))
	result.NoSubmission = true
	return result, true
}

// refusalDetail is what the Agent is told about a refused step (#321 A). A
// kernel Detail is diagnostic, not public copy: it is returned only when its
// source marked it UserFacing. Otherwise an explicitly safe Provider
// SafeDetail wins, then a model error.* Schema message, then the public
// invalid-parameters fallback. Upstream excerpts, IDs, parameter names and
// paths never reach the Agent, and the kernel detail is never appended.
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
