package generationtool

// Prompt transparency (#249): the tool result tells the Agent the exact text
// prompt the server submitted, every layer it applied, and any length cut.
import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Prompt layer ids. The Schema's prompt_report.layers labels each one.
const (
	promptLayerReferenceTemplate      = "reference_template"
	promptLayerPreviousPromptTemplate = "previous_prompt_template"
	promptLayerPurposesTemplate       = "purposes_template"
	promptLayerSkillTemplate          = "skill_template"
	promptLayerStyleLock              = "style_lock"
	promptLayerNegativePrompt         = "negative_prompt"
	promptLayerTruncation             = "truncation"
	promptLayerPreparedInput          = "prepared_input"
)

type promptLayer struct {
	// ID is one of the promptLayer* constants; the Schema labels it.
	ID string
	// Text is what the layer inserted or did (the applied template, style
	// lock text, default negative prompt, or a length description).
	Text string
}

// promptTrace records the prompt the Agent wrote, the layers applied, and the
// exact prompt submitted. It is computed in memory for one call and never
// stored, so the resume path rebuilds it from the prepared input.
type promptTrace struct {
	Original  string
	Final     string
	Layers    []promptLayer
	Truncated int // runes removed
	MaxLength int // Offering's prompt max_length; 0 when unbounded
}

func (trace *promptTrace) add(id, text string) {
	if trace == nil || strings.TrimSpace(text) == "" {
		return
	}
	trace.Layers = append(trace.Layers, promptLayer{ID: id, Text: text})
}

// changed reports whether any layer or cut changed the submitted prompt.
func (trace *promptTrace) changed() bool {
	return trace != nil && (len(trace.Layers) > 0 || trace.Truncated > 0)
}

// promptNote is the object a tool result carries under result_fields's
// prompt_note key. It never repeats a final prompt above
// maximum_reported_characters: FinalLength and the layers still say what
// happened, and the full text is omitted.
type promptNote struct {
	Layers                    []promptNoteLayer `json:"layers,omitempty"`
	FinalLength               int               `json:"final_length"`
	MaximumLength             int               `json:"maximum_length,omitempty"`
	TruncatedCharacters       int               `json:"truncated_characters,omitempty"`
	FinalPromptOmitted        bool              `json:"final_prompt_omitted,omitempty"`
	MaximumReportedCharacters int               `json:"maximum_reported_characters,omitempty"`
}

type promptNoteLayer struct {
	Layer string `json:"layer"`
	Text  string `json:"text,omitempty"`
}

// promptReport turns a trace into (finalPrompt, note) for the tool result.
// finalPrompt is "" when the prompt was not changed or its full text exceeds
// the Schema's reporting bound.
func (executor *Executor) promptReport(
	command chat.ToolCommand,
	tool catalog.ChatToolSchema,
	offering generation.Offering,
	trace *promptTrace,
) (string, map[string]any) {
	if trace == nil || !trace.changed() {
		return "", nil
	}
	report := executor.chat.Agent.PromptReport
	labels := report.Layers
	note := promptNote{
		FinalLength:         utf8.RuneCountInString(trace.Final),
		MaximumLength:       trace.MaxLength,
		TruncatedCharacters: trace.Truncated,
	}
	model := executor.localized(command, OfferingModelName(offering))
	label := func(id string) string {
		if text := labels[id]; text != "" {
			return text
		}
		return id
	}
	for _, layer := range trace.Layers {
		note.Layers = append(note.Layers, promptNoteLayer{
			Layer: label(layer.ID),
			Text:  executor.localized(command, layer.Text),
		})
	}
	if trace.Truncated > 0 {
		text := strings.NewReplacer(
			"{model}", model,
			"{length}", fmt.Sprint(utf8.RuneCountInString(trace.Final)+trace.Truncated),
			"{maximum}", fmt.Sprint(trace.MaxLength),
			"{excess}", fmt.Sprint(trace.Truncated),
			"{unit}", "characters",
		).Replace(executor.localized(command, report.TooLongTemplate))
		note.Layers = append(note.Layers, promptNoteLayer{Layer: label(promptLayerTruncation), Text: text})
	}
	final := trace.Final
	if maximum := report.MaximumReportedCharacters; maximum > 0 &&
		utf8.RuneCountInString(final) > maximum {
		final = ""
		note.FinalPromptOmitted = true
		note.MaximumReportedCharacters = maximum
	}
	if tool.ResultFields.FinalPrompt == "" {
		final = ""
	}
	if tool.ResultFields.PromptNote == "" {
		return final, nil
	}
	encoded, err := json.Marshal(note)
	if err != nil {
		return final, nil
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return final, nil
	}
	return final, value
}

// promptOverLimitFailure is the refusal before submission when the Schema
// refuses an over-length prompt. Nothing is created or charged.
func (executor *Executor) promptOverLimitFailure(
	command chat.ToolCommand,
	offering generation.Offering,
	length, maximum int,
) chat.ToolResult {
	template := executor.chat.Agent.PromptReport.TooLongTemplate
	message := strings.NewReplacer(
		"{model}", executor.localized(command, OfferingModelName(offering)),
		"{length}", fmt.Sprint(length),
		"{maximum}", fmt.Sprint(maximum),
		"{excess}", fmt.Sprint(length-maximum),
		"{unit}", "characters",
	).Replace(executor.localized(command, template))
	result := executor.failure(command, "", message)
	result.NoSubmission = true
	return result
}

// defaultNegativePromptValue is the default negative prompt a tool injects
// when the Agent set none: the Offering's negative-prompt parameter filled
// from the tool's default_parameters. It is a prompt layer the Agent must see.
func defaultNegativePromptValue(command chat.ToolCommand, offering generation.Offering) (string, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticNegativePrompt {
			continue
		}
		encoded, declared := command.Tool.DefaultParameters[parameter.Name]
		if !declared {
			return "", false
		}
		if _, chosen := command.Options.Parameters[parameter.Name]; chosen {
			return "", false
		}
		if _, chosen := command.ExplicitParameters[parameter.Name]; chosen {
			return "", false
		}
		var value string
		if json.Unmarshal(encoded, &value) != nil || strings.TrimSpace(value) == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

// promptTraceFromPrepared rebuilds the reported prompt after an approval or a
// turn resume, when the in-memory trace is gone. The frozen input carries the
// exact prompt that was approved; the call's arguments carry what the Agent
// wrote. No layer details are stored, so a changed prompt reports the
// prepared_input layer rather than inventing which template ran.
func (executor *Executor) promptTraceFromPrepared(
	command chat.ToolCommand,
	offering generation.Offering,
	input []byte,
) *promptTrace {
	parameter := ""
	for _, declared := range offering.Parameters {
		if declared.Semantic == generation.SemanticPrompt {
			parameter = declared.Name
			break
		}
	}
	if parameter == "" {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return nil
	}
	final := rawString(values[parameter])
	if final == "" {
		return nil
	}
	original := ""
	if args := command.Call.Arguments; len(args) > 0 {
		var arguments map[string]json.RawMessage
		if json.Unmarshal(args, &arguments) == nil {
			original = rawString(arguments[executor.chat.Context.ToolPromptArgument])
		}
	}
	trace := &promptTrace{Original: original, Final: final, MaxLength: promptLengthLimit(offering)}
	if final != original {
		trace.Layers = append(trace.Layers, promptLayer{ID: promptLayerPreparedInput})
	}
	return trace
}

// promptLengthLimit returns the Offering's prompt max_length, if it declares
// one.
func promptLengthLimit(offering generation.Offering) int {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticPrompt && parameter.MaxLength > 0 {
			return parameter.MaxLength
		}
	}
	return 0
}

// applyPromptLength applies the Schema's over-limit policy. It returns the
// submitted prompt, the number of runes cut, and a refusal result when the
// policy refuses. A Catalog without prompt_report keeps the historical
// truncation.
func (executor *Executor) applyPromptLength(
	command chat.ToolCommand,
	offering generation.Offering,
	prompt string,
) (string, int, int, chat.ToolResult, bool) {
	maximum := promptLengthLimit(offering)
	if maximum <= 0 {
		return prompt, 0, 0, chat.ToolResult{}, false
	}
	runes := []rune(prompt)
	if len(runes) <= maximum {
		return prompt, 0, maximum, chat.ToolResult{}, false
	}
	if executor.chat.Agent.PromptReport.OverLimit == "refuse" {
		return prompt, 0, maximum, executor.promptOverLimitFailure(command, offering, len(runes), maximum), true
	}
	truncated := len(runes) - maximum
	return string(runes[:maximum]), truncated, maximum, chat.ToolResult{}, false
}
