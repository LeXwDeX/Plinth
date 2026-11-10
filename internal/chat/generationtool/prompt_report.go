package generationtool

// Prompt transparency (#249): the tool result tells the Agent the exact text
// prompt the server submitted, every layer it applied, and any length cut.
import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	// Summary is the Schema's sentence about the report itself: submitted as
	// written, submitted after the listed layers, or rewritten without the
	// original prompt being available on a resume.
	Summary                   string            `json:"summary,omitempty"`
	Layers                    []promptNoteLayer `json:"layers,omitempty"`
	FinalLength               int               `json:"final_length"`
	MaximumLength             int               `json:"maximum_length,omitempty"`
	TruncatedCharacters       int               `json:"truncated_characters,omitempty"`
	FinalPromptOmitted        bool              `json:"final_prompt_omitted,omitempty"`
	MaximumReportedCharacters int               `json:"maximum_reported_characters,omitempty"`
}

// promptTruncationFallback is the neutral truncation sentence a Catalog
// without truncation_template gets. It says the step was submitted after the
// cut, unlike the too_long_template refusal for over_limit=refuse.
const promptTruncationFallback = "The prompt has {length} {unit}, over the limit of {maximum} {unit}; the server submitted it after cutting the last {excess} {unit}."

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
	if trace == nil {
		return "", nil
	}
	report := executor.chat.Agent.PromptReport
	changed := trace.changed()
	if !changed && report.UnchangedTemplate == "" {
		// A Catalog without the unchanged note keeps the historical result:
		// an untouched prompt reports nothing.
		return "", nil
	}
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
	if changed {
		for _, layer := range trace.Layers {
			note.Layers = append(note.Layers, promptNoteLayer{
				Layer: label(layer.ID),
				Text:  executor.localized(command, layer.Text),
			})
		}
		if trace.Truncated > 0 {
			// The prompt was cut and submitted: say so with the truncation
			// copy. The refusal copy belongs to over_limit=refuse, where
			// nothing was submitted or charged.
			template := report.TruncationTemplate
			if strings.TrimSpace(template) == "" {
				template = promptTruncationFallback
			}
			text := strings.NewReplacer(
				"{model}", model,
				"{length}", fmt.Sprint(utf8.RuneCountInString(trace.Final)+trace.Truncated),
				"{maximum}", fmt.Sprint(trace.MaxLength),
				"{excess}", fmt.Sprint(trace.Truncated),
				"{unit}", "characters",
			).Replace(executor.localized(command, template))
			note.Layers = append(note.Layers, promptNoteLayer{Layer: label(promptLayerTruncation), Text: text})
		}
		note.Summary = executor.localized(command, report.ChangedTemplate)
		if trace.Original == "" {
			note.Summary = executor.localized(command, report.ChangedWithoutPromptTemplate)
		}
	} else {
		note.Summary = executor.localized(command, report.UnchangedTemplate)
	}
	final := ""
	if changed {
		final = trace.Final
	}
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

// promptSkills is the Skill table and execution settings a call uses: the
// turn's frozen configuration when present, else the Schema's. The same table
// serves the first submission and a replay after a resume.
func (executor *Executor) promptSkills(command chat.ToolCommand) (map[string]catalog.SkillSchema, catalog.ChatSkillExecutionSchema) {
	skillExecution := executor.chat.Skills.Execution
	if command.Configuration != nil && len(command.Configuration.SkillExecution.PromptTemplates) > 0 {
		skillExecution = command.Configuration.SkillExecution
	}
	if command.Configuration == nil {
		return executor.skills, skillExecution
	}
	skills := make(map[string]catalog.SkillSchema, len(command.Configuration.Skills))
	for _, skill := range command.Configuration.Skills {
		skills[skill.ID] = skill
	}
	return skills, skillExecution
}

// promptSkillApplication is the Skill template a generation tool applies to
// the prompt: the template text, the Skill's own prompt, the placeholder
// fields and the parameter overrides it adds.
type promptSkillApplication struct {
	Template  string
	Prompt    string
	Fields    catalog.ChatSkillPromptTemplateFields
	Overrides map[generation.ParameterSemantic]json.RawMessage
}

// promptSkillApplication resolves the prompt layer of the selected Skill. A
// tool-binding Skill has no template to apply, and a Skill the Offering does
// not feature is never applied. It repeats no refusal: the caller owns the
// "selected skill is unavailable" answer.
func (executor *Executor) promptSkillApplication(
	command chat.ToolCommand,
	offering generation.Offering,
	capability generation.Capability,
	skills map[string]catalog.SkillSchema,
	skillExecution catalog.ChatSkillExecutionSchema,
) (promptSkillApplication, bool) {
	skillID := command.Options.SkillID
	if skillID == "" {
		return promptSkillApplication{}, false
	}
	if bound, exists := skills[skillID]; exists && bound.Tool != "" {
		return promptSkillApplication{}, false
	}
	skill, exists := skills[skillID]
	if !exists || !slices.Contains(offering.Features, executor.chat.Skills.Feature) {
		return promptSkillApplication{}, false
	}
	template := skillExecution.PromptTemplates[capability]
	if capability == generation.ImageToImage && skill.ImageToImageTemplate != "" {
		template = skill.ImageToImageTemplate
	}
	if template == "" {
		return promptSkillApplication{}, false
	}
	return promptSkillApplication{
		Template:  template,
		Prompt:    skill.Prompt,
		Fields:    skillExecution.PromptTemplateFields,
		Overrides: skillExecution.ParameterOverrides,
	}, true
}

// promptTraceFromPrepared rebuilds the reported prompt after an approval or a
// turn resume, when the in-memory trace is gone. The frozen input carries the
// exact prompt that was approved and the call's arguments carry what the
// Agent wrote, so the same layer chain is replayed from the frozen Offering
// and the call's parameters. The replay reports its layers only when it ends
// on exactly the frozen prompt; when the reference history, the multiview
// group or the Skill changed since the first submission, it falls back to the
// prepared_input layer rather than inventing which template ran. A frozen
// prompt that is exactly the original cut at the Offering's length limit is
// still reported as that cut, never as an opaque prepared input.
func (executor *Executor) promptTraceFromPrepared(
	ctx context.Context,
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
	var arguments map[string]json.RawMessage
	if args := command.Call.Arguments; len(args) > 0 {
		_ = json.Unmarshal(args, &arguments)
	}
	original := ""
	if arguments != nil {
		argument := executor.chat.Context.ToolPromptArgument
		if processing := command.Tool.Processing; processing != nil && processing.PromptArgument != "" {
			// A processing tool names its own prompt argument; the generation
			// tools use the chat context's.
			argument = processing.PromptArgument
		}
		original = rawString(arguments[argument])
	}
	frozen, known := frozenReferenceCount(input, offering)
	if trace := executor.replayPromptTrace(ctx, command, offering, arguments, original, final, frozen, known); trace != nil {
		return trace
	}
	trace := &promptTrace{Original: original, Final: final, MaxLength: promptLengthLimit(offering)}
	// The default negative prompt is deterministic from the frozen call and
	// Offering, so it stays reportable even when the reference layers cannot
	// be replayed. Processing tools fill their negative prompt through a
	// parameter argument, never through the tool default.
	if command.Tool.Processing == nil {
		if value, applied := defaultNegativePromptValue(command, offering); applied {
			trace.add(promptLayerNegativePrompt, value)
		}
	}
	if final != original {
		// The frozen prompt is exactly the original cut at the Offering's
		// limit: the cut is known even though the layer chain is not. Say so
		// instead of hiding the loss behind an opaque prepared_input layer.
		if trace.Truncated = promptTruncation(original, final, trace.MaxLength); trace.Truncated == 0 {
			trace.Layers = append(trace.Layers, promptLayer{ID: promptLayerPreparedInput})
		}
	}
	return trace
}

// replayPromptTrace applies the first submission's prompt chain again from
// the call's arguments and the frozen Offering. It returns nil when any input
// cannot be replayed exactly or the replayed prompt is not the frozen one. A
// processing tool's chain is the prompt parameter itself, so only the length
// cut is replayed for it.
func (executor *Executor) replayPromptTrace(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	arguments map[string]json.RawMessage,
	original, final string,
	frozenReferences int,
	frozenKnown bool,
) *promptTrace {
	if arguments == nil {
		return nil
	}
	trace := &promptTrace{Original: original, Final: original}
	prompt := original
	// A processing tool fills the Offering's prompt parameter directly and
	// never applies the reference, purpose, Skill or style-lock layers (#249).
	// Replaying them would invent a prompt the first submission never sent;
	// its only rewrite is the length cut below.
	if command.Tool.Processing == nil {
		referenceURLs, serverPurposes, ok := executor.replayReferenceURLs(ctx, command, offering, arguments, frozenReferences, frozenKnown)
		if !ok {
			return nil
		}
		purposes, refusal := executor.referencePurposes(command, arguments, referenceURLs, serverPurposes)
		if refusal != "" {
			return nil
		}
		switch {
		case purposes != "":
			before := prompt
			prompt = promptWithPurposes(command, purposes, prompt)
			if prompt != before {
				trace.add(promptLayerPurposesTemplate, command.Tool.PromptWithReferencePurposesTemplate)
			}
		case len(referenceURLs) > 0:
			before := prompt
			prompt = executor.synthesizePrompt(command, prompt, referenceURLs)
			if prompt != before {
				layer := promptLayerReferenceTemplate
				template := command.Tool.PromptWithReferenceTemplate
				if executor.previousPrompt(command, referenceURLs[0]) != "" {
					layer = promptLayerPreviousPromptTemplate
					template = command.Tool.PromptWithPreviousTemplate
				}
				trace.add(layer, template)
			}
		}
		capability := command.Tool.FallbackCapability
		if len(referenceURLs) > 0 {
			capability = command.Tool.Capability
		}
		skills, skillExecution := executor.promptSkills(command)
		if application, applied := executor.promptSkillApplication(command, offering, capability, skills, skillExecution); applied {
			prompt = strings.NewReplacer(
				application.Fields.UserInput, prompt,
				application.Fields.SkillPrompt, application.Prompt,
			).Replace(application.Template)
			trace.add(promptLayerSkillTemplate, application.Template)
		}
		beforeStyleLock := prompt
		prompt = executor.applyStyleLockPrompt(command, offering, prompt)
		if prompt != beforeStyleLock {
			trace.add(promptLayerStyleLock, strings.TrimPrefix(prompt, beforeStyleLock))
		}
		if value, applied := defaultNegativePromptValue(command, offering); applied {
			trace.add(promptLayerNegativePrompt, value)
		}
	}
	maximum := promptLengthLimit(offering)
	truncated := 0
	if runes := []rune(prompt); maximum > 0 && len(runes) > maximum {
		truncated = len(runes) - maximum
		prompt = string(runes[:maximum])
	}
	trace.Truncated = truncated
	trace.MaxLength = maximum
	trace.Final = prompt
	if prompt != final {
		return nil
	}
	return trace
}

// replayReferenceURLs rebuilds the reference list the first submission used.
// It reports false whenever the result depends on state the frozen input did
// not record: a 3D source-model selection, or a reference list that no longer
// matches the frozen one.
func (executor *Executor) replayReferenceURLs(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	arguments map[string]json.RawMessage,
	frozenReferences int,
	frozenKnown bool,
) ([]string, map[string]string, bool) {
	if !frozenKnown {
		return nil, nil, false
	}
	var referenceURLs []string
	multiview, refusal, err := executor.multiviewSelection(ctx, command, arguments)
	if err != nil || refusal != "" {
		return nil, nil, false
	}
	if multiview != nil {
		for _, name := range []string{executor.chat.ThreeView.Multiview.ViewsArgument,
			executor.chat.Context.ReferenceHandlesArgument, executor.chat.Context.ToolReferenceArgument} {
			delete(arguments, name)
		}
		referenceURLs = multiview.URLs
	} else if command.Tool.AcceptReferenceImages == nil || *command.Tool.AcceptReferenceImages {
		urls, err := executor.selectReferences(command, arguments)
		if err != nil {
			return nil, nil, false
		}
		referenceURLs = urls
	}
	// #250: the reference list is rebuilt in the same priority order as the
	// first submission, with the same server-generated purposes, so the
	// replayed prompt can match the frozen one.
	identity, identityPurposes, anchorRefusal, err := executor.anchorReferenceURLs(command, arguments)
	if err != nil || anchorRefusal != "" {
		return nil, nil, false
	}
	style, stylePurposes, err := executor.styleReferenceURLs(command)
	if err != nil {
		return nil, nil, false
	}
	groups := assembleReferenceGroups(identity, style, referenceURLs)
	referenceURLs = groups.URLs
	capability := command.Tool.FallbackCapability
	if len(referenceURLs) > 0 {
		capability = command.Tool.Capability
	}
	// The image-to-3D source-model selection rewrites the reference list from
	// the chat history; the frozen input does not record that choice.
	if capability == generation.ImageTo3D && offering.ReferenceSelectionPolicy != "preserve" {
		return nil, nil, false
	}
	effective := referenceURLs
	if offering.ReferenceSelectionPolicy != "preserve" {
		effective = truncateReferences(referenceURLs, offering)
	}
	if len(effective) != frozenReferences {
		return nil, nil, false
	}
	return referenceURLs, mergeReferencePurposes(identityPurposes, stylePurposes), true
}

// frozenReferenceCount is how many reference pictures the frozen input
// carries. It reports false when the input does not have the Offering's
// reference parameter in a shape this replay understands.
func frozenReferenceCount(input []byte, offering generation.Offering) (int, bool) {
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return 0, false
	}
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceImage {
			continue
		}
		raw, present := values[parameter.Name]
		if !present || len(raw) == 0 || string(raw) == "null" {
			return 0, true
		}
		if parameter.Type == "asset-array" {
			var list []json.RawMessage
			if json.Unmarshal(raw, &list) != nil {
				return 0, false
			}
			return len(list), true
		}
		var scalar string
		if json.Unmarshal(raw, &scalar) != nil {
			return 0, false
		}
		if strings.TrimSpace(scalar) == "" {
			return 0, true
		}
		return 1, true
	}
	return 0, true
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

// promptTruncation reports the runes a frozen final prompt lost to the
// Offering's length limit, when it is exactly the original cut at that limit.
// It returns 0 when the difference comes from any other prompt layer, so the
// caller never invents a cut.
func promptTruncation(original, final string, maximum int) int {
	if maximum <= 0 {
		return 0
	}
	runes := []rune(original)
	if len(runes) <= maximum || string(runes[:maximum]) != final {
		return 0
	}
	return len(runes) - maximum
}

// applyProcessingPromptLength applies the Schema's over-limit policy to the
// prompt parameter of a processing step's built input. Every processing path
// calls it before PrepareInput: over_limit=refuse returns the refusal result
// (nothing is frozen, no Task exists and nothing is charged) while a
// truncating Catalog returns the cut prompt and the trace reporting the cut
// layer. The Offering's prompt max_length is the only length source.
func (executor *Executor) applyProcessingPromptLength(
	command chat.ToolCommand, offering generation.Offering, input map[string]any,
) (*promptTrace, chat.ToolResult, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticPrompt {
			continue
		}
		value, present := input[parameter.Name]
		if !present {
			return nil, chat.ToolResult{}, false
		}
		prompt, ok := value.(string)
		if !ok {
			return nil, chat.ToolResult{}, false
		}
		final, truncated, maximum, refusal, refused := executor.applyPromptLength(command, offering, prompt)
		if refused {
			return nil, refusal, true
		}
		if truncated == 0 && final == prompt {
			return nil, chat.ToolResult{}, false
		}
		input[parameter.Name] = final
		return &promptTrace{Original: prompt, Final: final, Truncated: truncated, MaxLength: maximum}, chat.ToolResult{}, false
	}
	return nil, chat.ToolResult{}, false
}
