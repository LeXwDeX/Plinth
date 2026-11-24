package openaiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Model constraints (#401). The real gateway judges every chat request by the
// rules of the model it names, and a request the model refuses is answered
// with HTTP 400 before any completion runs. constrainedModels declares the
// models whose rules the fake reproduces, so a Plinth stack that points at the
// fake notices a request shape such a model refuses. Every other model name
// keeps the unconstrained behavior.
//
// The evidence of each rule is kept apart, because the sources differ in
// strength:
//   - Observed: the gateway's upstream-error.log of 2026-11-20, where every
//     Agent turn and title request Plinth sent for gpt-astra failed. The
//     gateway had resolved the alias gpt-astra to gpt-6.2-astra, and the
//     refusal names that model. The two error bodies below are the observed
//     ones (key order included); nothing else of the log is kept: no request
//     ID, key or prompt text.
//   - Documented, not observed: OpenAI's latest-model guide and the model page
//     of gpt-6.2-astra (https://developers.openai.com/api/docs/guides/latest-model.md,
//     https://developers.openai.com/api/docs/models/gpt-6.2-astra.md, read
//     2026-11-20) say that the API rejects a request that sets the reasoning
//     effort to none, that the model supports Chat Completions but tool calling
//     requires Responses, and that a Chat Completions request must omit
//     temperature. The wording of the none refusal is constructed.
//   - Documented, not observed: DeepSeek's thinking mode requires the
//     reasoning_content of an assistant message that calls tools back in the
//     next round (https://api-docs.deepseek.com/guides/thinking_mode, its Tool
//     Calls rule), and the API answers 400 to a request that drops the field.
//     The wording of that refusal is constructed; it counts the index of the
//     message in the request, which the real API does not write (#377).
//
// The rules are those of the model, not of a wire: POST /responses judges the
// same models by the same table and answers in its own words
// (responses_constraints.go), and accepts the function tools this wire refuses.
//
// A request that breaks several rules is answered with the first one in the
// order below: none, tools, temperature. The logs pair the Agent turns, which
// broke the tools and temperature rules at once (tools, a high effort and
// temperature 0), with the tools refusal and the title requests (a high effort
// and temperature 0, no tools) with the temperature refusal; where the none
// refusal comes in that order is a choice.
type modelConstraint struct {
	// resolved is the upstream model the id stands for: the gateway resolves
	// an alias before the model judges the request, and the refusal names the
	// resolved model.
	resolved string
	// noEffortNone refuses reasoning_effort "none": documented, and observed on
	// /v1/responses (responses_constraints.go); the Chat Completions answer is
	// constructed.
	noEffortNone bool
	// noEffortMinimal refuses "minimal" as well. Observed on /v1/responses only:
	// it is not a rule of the Chat Completions wire here.
	noEffortMinimal bool
	// noFunctionTools refuses function tools on /v1/chat/completions. The
	// observed text blames the combination with a reasoning_effort other than
	// none; because the model refuses none as well (noEffortNone), no tools
	// request can pass, and a request with tools and no reasoning_effort is
	// refused too, as the model's own default effort is not none.
	noFunctionTools bool
	// defaultTemperatureOnly refuses a temperature other than the default 1
	// (observed); an absent or null temperature passes.
	defaultTemperatureOnly bool
	// reasoningContentRequired refuses an assistant message that calls tools
	// without reasoning_content: a thinking model must be given its own
	// reasoning back in every round of the tool loop, and the empty string the
	// vendor accepts for a round that produced none is a value, not a missing
	// field.
	reasoningContentRequired bool
	// allowedEfforts, when set, is the closed list of reasoning_effort values
	// the model accepts; any other value is refused as an invalid value
	// (constructed from the vendor's documents, see deepseekConstraint).
	allowedEfforts []string
	// noImageInput refuses any request that carries a picture (imageRefusal in
	// image_refusal.go): the model reads text only.
	noImageInput bool
	// minImageSide refuses a picture whose height or width is not larger than
	// it (observed: the gateway's refusal of a 1-pixel picture for a qwen model).
	minImageSide int
	// streamUsageTrailing: with stream_options.include_usage the usage rides on
	// its own last chunk with an empty choices array, after the chunk that
	// carries finish_reason (the DashScope shape, #536); false: the usage rides
	// on the finish_reason chunk (the DeepSeek shape, observed).
	streamUsageTrailing bool
}

var constrainedModels = map[string]modelConstraint{
	"gpt-astra":     astraConstraint,
	"gpt-6.2-astra": astraConstraint,
	"deepseek":      deepseekConstraint,
}

// constrainedPrefixes are the model families whose rules apply to every model
// name that starts with the prefix, the way the Catalog's chat profiles are
// declared (model_prefixes); an exact entry of constrainedModels wins.
var constrainedPrefixes = []struct {
	prefix string
	rule   modelConstraint
}{
	{"deepseek", deepseekConstraint},
	{"qwen", qwenConstraint},
	{TextOnlyModelPrefix, textOnlyConstraint},
}

// TextOnlyModelPrefix names a model of the fake that takes no picture input.
// The default stack model (deepseek) is not one: whether the real gateway's
// deepseek reads pictures is for the real round to settle, and a refusal there
// would make Plinth strip the pictures of every vision scenario of the fake
// stack (#535), so only a model named so, or ImageRefusalMarkerPrefix, gets it.
const TextOnlyModelPrefix = "fake-text-only"

var textOnlyConstraint = modelConstraint{noImageInput: true}

// constraintFor is the rule set of a model name; false for a model without one.
func constraintFor(model string) (modelConstraint, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if rule, found := constrainedModels[name]; found {
		return rule, true
	}
	for _, family := range constrainedPrefixes {
		if strings.HasPrefix(name, family.prefix) {
			return family.rule, true
		}
	}
	return modelConstraint{}, false
}

// resolvedModel is the model name an answer reports for a request's model.
func (rule modelConstraint) resolvedModel(requested string) string {
	if rule.resolved != "" {
		return rule.resolved
	}
	return requested
}

var astraConstraint = modelConstraint{
	resolved:               "gpt-6.2-astra",
	noEffortNone:           true,
	noEffortMinimal:        true,
	noFunctionTools:        true,
	defaultTemperatureOnly: true,
}

// deepseekConstraint is the thinking mode of the model the dev-test stack uses:
// its official Tool Calls guide requires reasoning_content on the assistant
// message that calls tools, and the API answers 400 to a request that drops it
// (#377).
//
// Constructed from the same documents (checked 2026-11-24): reasoning_effort takes low, high and max, plus the
// values the vendor maps onto them (minimal and low to low, medium, high and
// xhigh to high, max and ultra to max) and none, which turns thinking off; any
// other value is an invalid value (#536). The usage of a stream rides on the
// finish_reason chunk (observed 2026-10-29).
var deepseekConstraint = modelConstraint{
	reasoningContentRequired: true,
	allowedEfforts:           []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"},
}

// qwenConstraint is the DashScope compatible mode behind the dev-test gateway:
// a picture whose height or width is not larger than 32 pixels is refused
// (observed 2026-11-23: invalid_parameter_error for a 1-pixel picture), and a
// stream with include_usage ends with a chunk of empty choices that carries
// the usage (constructed from the Model Studio OpenAI-compatible documents,
// read 2026-11-24; the 2026-11-23 capture is non-stream, so no real stream of
// a qwen model is committed yet).
var qwenConstraint = modelConstraint{
	minImageSide:        32,
	streamUsageTrailing: true,
}

// constraintRefusal is the error envelope of a refusal. Its field order is the
// observed one: message, type, param, code (null when the refusal has none).
type constraintRefusal struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Param   string  `json:"param"`
		Code    *string `json:"code"`
	} `json:"error"`
}

func newConstraintRefusal(message, param, code string) constraintRefusal {
	var refusal constraintRefusal
	refusal.Error.Message, refusal.Error.Type, refusal.Error.Param = message, "invalid_request_error", param
	if code != "" {
		refusal.Error.Code = &code
	}
	return refusal
}

// modelRefusal is the 400 the request earns from the rules of its model, if
// any.
func (r chatRequest) modelRefusal() (constraintRefusal, bool) {
	rule, constrained := constraintFor(r.Model)
	if !constrained {
		return constraintRefusal{}, false
	}
	none := r.effortIsNone()
	if len(rule.allowedEfforts) > 0 {
		if effort, listed := r.effortValue(); listed && !slices.Contains(rule.allowedEfforts, effort) {
			return newConstraintRefusal(fmt.Sprintf(
				"Invalid value: '%s'. Supported values are: 'low', 'high', and 'max'.", effort),
				"reasoning_effort", "invalid_value"), true
		}
	}
	if rule.reasoningContentRequired {
		if index := r.toolCallWithoutReasoning(); index >= 0 {
			return newConstraintRefusal(fmt.Sprintf(
				"The reasoning_content of the assistant message at index %d that calls tools is required "+
					"in the thinking mode. Pass the reasoning of the previous round back, or set "+
					"reasoning_effort to none.", index), "messages", ""), true
		}
	}
	if rule.noEffortNone && none {
		return newConstraintRefusal(
			"Unsupported value: 'reasoning_effort' does not support 'none' with this model.",
			"reasoning_effort", "unsupported_value"), true
	}
	if rule.noFunctionTools && len(r.Tools) > 0 && !none {
		return newConstraintRefusal(fmt.Sprintf(
			"Function tools with reasoning_effort are not supported for %s in %s/chat/completions. "+
				"To use function tools, use %s/responses or set reasoning_effort to 'none'.",
			rule.resolved, samplePrefix, samplePrefix), "reasoning_effort", ""), true
	}
	if rule.defaultTemperatureOnly {
		if literal, other := r.temperatureOtherThanDefault(); other {
			return newConstraintRefusal(fmt.Sprintf(
				"Unsupported value: 'temperature' does not support %s with this model. "+
					"Only the default (1) value is supported.", literal),
				"temperature", "unsupported_value"), true
		}
	}
	return constraintRefusal{}, false
}

// effortValue is reasoning_effort when the request sent a string.
func (r chatRequest) effortValue() (string, bool) {
	var effort string
	if json.Unmarshal(r.ReasoningEffort, &effort) != nil {
		return "", false
	}
	return effort, true
}

// effortIsNone reports whether the request sets reasoning_effort to "none".
func (r chatRequest) effortIsNone() bool {
	var effort string
	return json.Unmarshal(r.ReasoningEffort, &effort) == nil && effort == "none"
}

// toolCallWithoutReasoning is the index of the first assistant message that
// calls tools without reasoning_content, or -1 when every such message carries
// the field (its content may well be empty, as a round without reasoning is
// sent back as "").
//
// With StrictReasoningMarker anywhere in the conversation and tools in the
// request, every assistant message must carry it, a plain text round included:
// the reading of the vendor's guide (checked 2026-11-24) that "all subsequent
// requests" of a tool-calling conversation pass the reasoning back "even for
// turns where the model did not perform a tool call". Which reading the real
// API enforces is not proven (#536, I28); the default stays the narrower one.
func (r chatRequest) toolCallWithoutReasoning() int {
	strict := len(r.Tools) > 0 && r.anyText(StrictReasoningMarker)
	for index, message := range r.Messages {
		if message.Role == "assistant" && (strict || len(message.ToolCalls) > 0) && message.ReasoningContent == nil {
			return index
		}
	}
	return -1
}

// anyText reports whether any message of the conversation contains the text.
func (r chatRequest) anyText(needle string) bool {
	for _, message := range r.Messages {
		if strings.Contains(messageText(message.Content), needle) {
			return true
		}
	}
	return false
}

// temperatureOtherThanDefault returns the temperature as the request wrote it
// when it is a number other than 1.
func (r chatRequest) temperatureOtherThanDefault() (string, bool) {
	raw := bytes.TrimSpace(r.Temperature)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || value == 1 {
		return "", false
	}
	return string(raw), true
}
