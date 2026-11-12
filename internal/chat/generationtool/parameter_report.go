package generationtool

// Parameter transparency (#296): the tool result tells the Agent which model
// parameters the step really froze that differ from the Offering's defaults,
// and who set each one. Without it the Agent can only repeat its own intent,
// and it once claimed a seed the frozen input never carried.
import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Sources of a reported parameter. user, agent and "profile:<id>" are the
// vocabulary the frozen parameter adjustments and model_choice already use;
// skill, forced and converged name the other layers that can set a value.
const (
	parameterSourceUser          = "user"
	parameterSourceAgent         = "agent"
	parameterSourceSkill         = "skill"
	parameterSourceForced        = "forced"
	parameterSourceConverged     = "converged"
	parameterSourceDefault       = "default"
	parameterSourceUnknown       = "unknown"
	parameterSourceProfilePrefix = "profile:"
	parameterSourceConcept       = parameterSourceProfilePrefix + "concept"
)

// RememberedParameter is one Offering parameter the session remembers and who
// last wrote it: "user" for the person's pin, "agent" for the Agent's setting.
type RememberedParameter struct {
	Value  json.RawMessage
	Source string
}

// reportedParameter is one entry of the parameters list in a tool result. Value
// is the frozen value; a long one is cut at the Schema's bound and marked.
type reportedParameter struct {
	Name           string          `json:"name"`
	Label          string          `json:"label,omitempty"`
	Value          json.RawMessage `json:"value"`
	ValueTruncated bool            `json:"value_truncated,omitempty"`
	Source         string          `json:"source"`
}

// parameterLayer is the values one layer gave the step, as that layer holds
// them now. The report names a layer only when its value is exactly the frozen
// one, so a layer that has changed since the step was frozen is never credited.
type parameterLayer struct {
	source string
	values map[string]json.RawMessage
}

// parameterOrigin tells who set a frozen value. The frozen adjustments (agent or
// purpose profile, written when the input was frozen) come first; the layers
// are the ones the step's input was built from, strongest first.
type parameterOrigin struct {
	adjustments map[string]generation.ParameterAdjustment
	layers      []parameterLayer
}

// parameterReport lists, in the Offering's declaration order, the parameters of
// a step whose frozen value differs from the Offering's default. input is the
// frozen input (the prepared input's normalized input, or the Task's own copy
// when no prepared input exists), so a result after an approval or a recovery
// reports exactly what was submitted. A parameter that is not listed is at its
// default: prompts (reported through final_prompt), reference pictures, source
// references, hidden and fixed parameters are never listed. It returns nil when
// the tool declares no result key or the input cannot be read, and an empty
// list when every parameter is at its default.
func (executor *Executor) parameterReport(
	command chat.ToolCommand,
	offering generation.Offering,
	input json.RawMessage,
	adjustments []generation.ParameterAdjustment,
) []reportedParameter {
	if command.Tool.ResultFields.Parameters == "" || len(input) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil || values == nil {
		return nil
	}
	typed := chat.DecodedValues(values)
	origin := executor.buildParameterOrigin(command, offering, values, adjustments)
	maximum := executor.chat.Agent.ParameterReport.MaximumValueCharacters
	report := []reportedParameter{}
	for _, parameter := range offering.Parameters {
		if !reportableParameter(offering, parameter) {
			continue
		}
		value, present := values[parameter.Name]
		if !present || string(value) == "null" {
			continue
		}
		if len(parameter.Default) > 0 {
			if reportSameValue(value, parameter.Default) {
				continue
			}
		} else if emptyParameterValue(value) {
			// A parameter without a default that holds nothing is unset.
			continue
		}
		shown, truncated := reportedValue(value, maximum)
		label := strings.TrimSpace(executor.localized(command, parameter.Label))
		report = append(report, reportedParameter{
			Name: parameter.Name, Label: label, Value: shown, ValueTruncated: truncated,
			Source: origin.sourceOf(parameter, value, typed),
		})
	}
	return report
}

// reportableParameter reports whether a parameter is a model setting the Agent
// can speak about: not a prompt (final_prompt and prompt_note report those), a
// reference picture, a source reference or a server-held value.
func reportableParameter(offering generation.Offering, parameter generation.Parameter) bool {
	if !schema.StepInputParameter(parameter) {
		return false
	}
	switch parameter.Type {
	case "asset", "asset-array", "task":
		return false
	}
	switch parameter.Semantic {
	case generation.SemanticPrompt, generation.SemanticNegativePrompt,
		generation.SemanticReferenceImage, generation.SemanticReferenceViews, generation.SemanticMask:
		return false
	}
	if strings.HasSuffix(strings.ToLower(parameter.Name), "_prompt") {
		// A tool with several prompts (front_prompt, left_prompt, ...) names
		// each slot by suffix instead of the single prompt semantic.
		return false
	}
	consent := offering.ExtraConsentParameter()
	return consent == "" || parameter.Name != consent
}

// emptyParameterValue reports whether a frozen value holds nothing: an empty
// text, list or object.
func emptyParameterValue(value json.RawMessage) bool {
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return false
	}
	switch typed := decoded.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// reportedValue is a frozen value as the result shows it: as it is when it fits
// the bound, else the start of its text followed by an ellipsis, marked
// truncated. A bound of zero or less shows the value whole.
func reportedValue(value json.RawMessage, maximum int) (json.RawMessage, bool) {
	var text string
	if json.Unmarshal(value, &text) == nil {
		if maximum <= 0 || utf8.RuneCountInString(text) <= maximum {
			return value, false
		}
		cut, _ := json.Marshal(string([]rune(text)[:maximum]) + "…")
		return cut, true
	}
	if maximum <= 0 || utf8.RuneCountInString(string(value)) <= maximum {
		return value, false
	}
	cut, _ := json.Marshal(string([]rune(string(value))[:maximum]) + "…")
	return cut, true
}

// sourceOf names who set a frozen value that differs from the Offering's
// default. A frozen adjustment says it for an advised change; a forced value is
// the Schema's own ruling whatever a layer asked for; then the layers of the
// call, strongest first; a default moved into its conditional range is
// converged. A value nothing explains is "unknown", never a guess.
func (origin parameterOrigin) sourceOf(parameter generation.Parameter, value json.RawMessage, typed map[string]any) string {
	if adjustment, found := origin.adjustments[parameter.Name]; found && reportSameValue(adjustment.Value, value) {
		if adjustment.Source != "" {
			return adjustment.Source
		}
		return parameterSourceAgent
	}
	for _, forced := range parameter.ForcedWhen {
		if schema.ConditionMatches(forced.When, typed) && reportSameValue(forced.Value, value) {
			return parameterSourceForced
		}
	}
	for _, layer := range origin.layers {
		if supplied, given := layer.values[parameter.Name]; given && reportSameValue(supplied, value) {
			return layer.source
		}
	}
	if convergedDefault(parameter, value, typed) {
		return parameterSourceConverged
	}
	return parameterSourceUnknown
}

// convergedDefault reports whether a number is the Schema's default moved into
// the range its maximum_when entries give under the other frozen values (#139),
// not a value anyone gave.
func convergedDefault(parameter generation.Parameter, value json.RawMessage, typed map[string]any) bool {
	if parameter.Type != "number" || len(parameter.MaximumWhen) == 0 || len(parameter.Default) == 0 {
		return false
	}
	var base, frozen float64
	if json.Unmarshal(parameter.Default, &base) != nil || json.Unmarshal(value, &frozen) != nil {
		return false
	}
	clamped := parameter.ClampNumber(base, typed)
	return clamped != base && clamped == frozen
}

// buildParameterOrigin rebuilds the layers a step's input was built from.
// Everything it reads is frozen with the call or the turn (the call's
// arguments, the turn's panel values, Skill, brief and harness snapshot) except
// the session's remembered settings, which are read now and credited only when
// they still hold the frozen value. A processing step's input comes from its
// parameter arguments and the purpose, a harness step's from the harness
// snapshot.
func (executor *Executor) buildParameterOrigin(
	command chat.ToolCommand,
	offering generation.Offering,
	values map[string]json.RawMessage,
	adjustments []generation.ParameterAdjustment,
) parameterOrigin {
	origin := parameterOrigin{adjustments: make(map[string]generation.ParameterAdjustment, len(adjustments))}
	for _, adjustment := range adjustments {
		origin.adjustments[adjustment.Name] = adjustment
	}
	add := func(source string, layer map[string]json.RawMessage) {
		if len(layer) > 0 {
			origin.layers = append(origin.layers, parameterLayer{source: source, values: layer})
		}
	}
	declared := make(map[string]generation.Parameter, len(offering.Parameters))
	for _, parameter := range offering.Parameters {
		declared[parameter.Name] = parameter
	}
	var arguments map[string]json.RawMessage
	if len(command.Call.Arguments) > 0 {
		_ = json.Unmarshal(command.Call.Arguments, &arguments)
	}
	if activeHarness(command) {
		add(parameterSourceUser, command.Options.Harness.Selection.FixedParameters)
		var given map[string]json.RawMessage
		if json.Unmarshal(arguments["parameters"], &given) == nil {
			add(parameterSourceAgent, given)
		}
		return origin
	}
	processing := command.Tool.Processing
	if processing == nil {
		executor.addGenerationLayers(command, offering, declared, arguments, add)
	} else {
		fromCall := map[string]json.RawMessage{}
		for argument, parameter := range processing.ParameterArguments {
			if raw, given := arguments[argument]; given {
				fromCall[parameter] = raw
			}
		}
		add(parameterSourceAgent, fromCall)
	}
	if profile, overrides := executor.purposeParameterValues(command, offering, values); profile.ID != "" {
		add(parameterSourceProfilePrefix+profile.ID, overrides)
	}
	if processing == nil {
		toolDefaults := map[string]json.RawMessage{}
		for name, raw := range command.Tool.DefaultParameters {
			if _, exists := declared[name]; exists {
				toolDefaults[name] = raw
			}
		}
		add(parameterSourceDefault, toolDefaults)
	}
	return origin
}

// addGenerationLayers adds the layers a generation tool's input is built from
// above the purpose, strongest first: the selected Skill, the Agent's mapped
// arguments, the person's panel for the turn, the session's remembered settings
// (the person's pin over the Agent's) and the concept sketch's cheap values. It
// follows buildInput's order and its rule for which parameters a layer may set.
func (executor *Executor) addGenerationLayers(
	command chat.ToolCommand,
	offering generation.Offering,
	declared map[string]generation.Parameter,
	arguments map[string]json.RawMessage,
	add func(string, map[string]json.RawMessage),
) {
	if command.Options.SkillID != "" {
		skills, execution := executor.promptSkills(command)
		if application, applied := executor.promptSkillApplication(command, offering, offering.Capability, skills, execution); applied {
			fromSkill := map[string]json.RawMessage{}
			for semantic, encoded := range application.Overrides {
				if parameter, found := semanticParameter(offering, semantic); found {
					fromSkill[parameter.Name] = encoded
				}
			}
			add(parameterSourceSkill, fromSkill)
		}
	}
	fromCall := map[string]json.RawMessage{}
	for argument, semantic := range command.Tool.ArgumentMappings {
		if semantic == string(generation.SemanticPrompt) || semantic == "reference-indices" {
			continue
		}
		parameter, mapped := command.Tool.OverrideMappings[semantic]
		if raw, given := arguments[argument]; given && mapped {
			fromCall[parameter] = raw
		}
	}
	add(parameterSourceAgent, fromCall)
	panel := map[string]json.RawMessage{}
	explicit := command.ExplicitParameters
	if explicit == nil {
		explicit = command.Options.Parameters
	}
	for name, raw := range explicit {
		if parameter, exists := declared[name]; exists && turnParameterAllowed(offering, parameter) {
			panel[name] = raw
		}
	}
	overrideValues := map[string]string{
		"aspect_ratio": command.Options.AspectRatio,
		"pose_mode":    command.Options.PoseMode,
		"size":         command.Options.Size,
		"image_size":   command.Options.ImageSize,
	}
	for semantic, parameter := range command.Tool.OverrideMappings {
		if value := overrideValues[semantic]; value != "" {
			if encoded, err := json.Marshal(value); err == nil {
				panel[parameter] = encoded
			}
		}
	}
	add(parameterSourceUser, panel)
	if executor.offeringParameterStates != nil {
		pinned := map[string]json.RawMessage{}
		suggested := map[string]json.RawMessage{}
		for name, state := range executor.offeringParameterStates(command.OwnerID, command.SessionID, string(offering.ID)) {
			parameter, exists := declared[name]
			switch {
			case !exists:
			case state.Source == parameterSourceUser && offering.UserParameterAllowed(parameter):
				pinned[name] = state.Value
			case state.Source == parameterSourceAgent && offering.AgentParameterAllowed(parameter):
				suggested[name] = state.Value
			}
		}
		add(parameterSourceUser, pinned)
		add(parameterSourceAgent, suggested)
	}
	add(parameterSourceConcept, executor.conceptSketchParameterValues(command, offering))
}

// reportSameValue compares two JSON values by meaning, not by bytes.
func reportSameValue(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
