package generationtool

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The purpose profile, the user's explicit pick and the Agent's own model
// argument decide which 3D model a step runs (#103). The layers, highest
// first: the person's explicit pick in the session, the Agent's model
// argument, the production purpose's recommendation, the Offering defaults.

// Shapes of a 3D input, the keys of model3d_usage.recommended_offerings.
const (
	shapeTextTo3D      = "text-to-3d"
	shapeImageTo3D     = "image-to-3d"
	shapeMultiviewTo3D = "multiview-to-3d"
)

// modelChoice is the public result object naming who chose the model of a
// step; it carries the purpose id and reason only when the purpose chose.
type modelChoice struct {
	Source  string `json:"source"`
	Profile string `json:"profile,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// offeringChoice is one selection and why it was made. A Refusal is the
// Schema's message for the Agent; nothing is submitted then.
type offeringChoice struct {
	Offering generation.Offering
	Source   string
	Profile  string
	Reason   string
	Refusal  string
}

// modelInput is the part of a call's input that decides whether an Offering
// accepts it: how many reference pictures it gives and, for a multiview step,
// which views.
type modelInput struct {
	References int
	Views      []string
	Group      bool
}

// briefPurpose returns the purpose profile the session's production brief
// selects. A session without a brief has none: nothing is recommended or
// overridden by a purpose.
func (executor *Executor) briefPurpose(command chat.ToolCommand) (catalog.ChatModel3DUsageProfile, bool) {
	usage := executor.chat.Model3DUsage
	if len(usage.Profiles) == 0 || command.Options.ProductionBrief.Empty() {
		return catalog.ChatModel3DUsageProfile{}, false
	}
	field := ""
	for _, declared := range usage.Brief.Fields {
		if declared.Type == "profile" {
			field = declared.Name
			break
		}
	}
	if field == "" {
		return catalog.ChatModel3DUsageProfile{}, false
	}
	id := command.Options.ProductionBrief.Text(field)
	for _, profile := range usage.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return catalog.ChatModel3DUsageProfile{}, false
}

// profileParameterValues are the values the brief's purpose applies to an
// Offering: its parameter overrides and the values its budget gives the
// bounded parameters (#129). The Catalog has already checked every override at
// load time; this repeats the public-parameter rule so a hand-built Executor
// cannot apply a value the Offering does not expose to the Agent.
func (executor *Executor) profileParameterValues(command chat.ToolCommand, offering generation.Offering) (catalog.ChatModel3DUsageProfile, map[string]json.RawMessage) {
	return executor.purposeParameterValues(command, offering, nil)
}

// purposeParameterValues is profileParameterValues for a step whose other
// values are already known: current supplies them (a remesh topology) when
// the budget measures a face count.
func (executor *Executor) purposeParameterValues(command chat.ToolCommand, offering generation.Offering,
	current map[string]json.RawMessage) (catalog.ChatModel3DUsageProfile, map[string]json.RawMessage) {
	profile, found := executor.briefPurpose(command)
	if !found {
		return catalog.ChatModel3DUsageProfile{}, nil
	}
	overrides := profile.ParameterOverrides[string(offering.ID)]
	values := make(map[string]json.RawMessage, len(overrides))
	for name, value := range overrides {
		declared := false
		for _, parameter := range offering.Parameters {
			if parameter.Name != name {
				continue
			}
			declared = offering.AgentParameterAllowed(parameter)
			break
		}
		if declared {
			values[name] = value
		}
	}
	for name, value := range executor.budgetValues(command, profile, offering, values, current) {
		values[name] = value
	}
	if len(values) == 0 {
		return profile, nil
	}
	return profile, values
}

// modelShape is the recommended_offerings key of this call.
func (executor *Executor) modelShape(command chat.ToolCommand, capability generation.Capability) string {
	if tool := executor.chat.ThreeView.Multiview.Tool; tool != "" && command.Tool.Name == tool {
		return shapeMultiviewTo3D
	}
	if capability == generation.TextTo3D {
		return shapeTextTo3D
	}
	return shapeImageTo3D
}

// modelMatches reports whether a name selects an Offering: its model ID, its
// Offering ID, or the provider and model ID pair the Catalog also shows.
func modelMatches(offering generation.Offering, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return name == offering.ModelID || name == string(offering.ID) ||
		(offering.ModelID != "" && offering.ProviderID != "" && name == offering.ProviderID+"/"+offering.ModelID)
}

// availableOfferings are the enabled, runtime-available Offerings of one
// capability, in catalog order.
func (executor *Executor) availableOfferings(capability generation.Capability) []generation.Offering {
	var result []generation.Offering
	for _, offering := range executor.gateway.Offerings(capability) {
		if offering.Capability == capability && offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
			result = append(result, offering)
		}
	}
	return result
}

// matchOffering finds the Offering a name selects among candidates.
func matchOffering(candidates []generation.Offering, name string) (generation.Offering, bool) {
	for _, offering := range candidates {
		if modelMatches(offering, name) {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// acceptsModelInput reports whether an Offering can take this call's input: a
// reference list its reference parameter would refuse (a preserve-policy
// Offering over its limit), or a multiview view set it does not declare.
func (executor *Executor) acceptsModelInput(offering generation.Offering, input modelInput) bool {
	if input.References > 0 {
		accepted := false
		for _, parameter := range offering.Parameters {
			if parameter.Semantic != generation.SemanticReferenceImage {
				continue
			}
			switch parameter.Type {
			case "asset":
				accepted = input.References == 1
			case "asset-array":
				accepted = parameter.MaxItems == 0 || input.References <= parameter.MaxItems ||
					offering.ReferenceSelectionPolicy != "preserve"
			}
			break
		}
		if !accepted {
			return false
		}
	}
	if len(input.Views) > 0 {
		return multiviewAccepts(offering, input.Views, input.Group, executor.chat.ThreeView.Multiview)
	}
	return true
}

// multiviewAccepts reports whether an Offering declares the views of a
// multiview call: every view of a structured request, the required view, and
// at least the Schema's minimum. A group leaves out a view the Offering does
// not name, as fitMultiviewViews does.
func multiviewAccepts(offering generation.Offering, views []string, group bool, multiview catalog.ChatThreeViewMultiviewSchema) bool {
	declared := false
	var accepted []string
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceViews {
			continue
		}
		declared = true
		for _, option := range parameter.Options {
			accepted = append(accepted, option.Value)
		}
	}
	if !declared {
		return false
	}
	kept := make([]string, 0, len(views))
	for _, view := range views {
		if !slices.Contains(accepted, view) {
			if group {
				continue
			}
			return false
		}
		kept = append(kept, view)
	}
	return len(kept) >= multiview.MinimumViews &&
		(multiview.RequiredView == "" || slices.Contains(kept, multiview.RequiredView))
}

// profileOffering is the first recommendation of the purpose that is
// available and accepts this input, with the profile that named it.
func (executor *Executor) profileOffering(command chat.ToolCommand, capability generation.Capability, input modelInput) (generation.Offering, catalog.ChatModel3DUsageProfile, bool) {
	profile, found := executor.briefPurpose(command)
	if !found {
		return generation.Offering{}, catalog.ChatModel3DUsageProfile{}, false
	}
	candidates := executor.availableOfferings(capability)
	for _, name := range profile.RecommendedOfferings[executor.modelShape(command, capability)] {
		offering, matched := matchOffering(candidates, name)
		if matched && executor.acceptsModelInput(offering, input) {
			return offering, profile, true
		}
	}
	return generation.Offering{}, catalog.ChatModel3DUsageProfile{}, false
}

// chooseOffering selects the Offering of a 3D tool call by the model-choice
// layers. It returns a Refusal when a named model conflicts with the person's
// explicit pick or is unavailable; nothing is submitted in that case.
func (executor *Executor) chooseOffering(
	command chat.ToolCommand,
	capability generation.Capability,
	arguments map[string]json.RawMessage,
	input modelInput,
) (offeringChoice, error) {
	tool := command.Tool
	candidates := executor.availableOfferings(capability)
	named := ""
	if tool.ModelArgument != "" {
		_ = json.Unmarshal(arguments[tool.ModelArgument], &named)
		named = strings.TrimSpace(named)
	}
	// Only the session marker is the person's explicit lock. The workspace's
	// per-domain model is the picker's current value, which may merely be
	// restored from another session or a previous visit, so it must not keep
	// the purpose from choosing (#103). The lock applies to every 3D tool,
	// the multiview tool included: ignore_user_model only keeps the workspace
	// picker out of its fallback.
	locked := strings.TrimSpace(command.Options.ExplicitModel3D)
	if locked != "" {
		offering, matched := matchOffering(candidates, locked)
		if !matched {
			return offeringChoice{Refusal: executor.modelUnavailableText(command, candidates, locked)}, nil
		}
		// A named or locked model is not filtered by the input shape here: the
		// ordinary reference and view checks refuse an input it cannot take
		// with their precise messages (#103).
		if named != "" && !modelMatches(offering, named) {
			return offeringChoice{Refusal: executor.modelConflictText(command, offering, named)}, nil
		}
		return offeringChoice{Offering: offering, Source: "user"}, nil
	}
	if named != "" {
		offering, matched := matchOffering(candidates, named)
		if !matched {
			return offeringChoice{Refusal: executor.modelUnavailableText(command, candidates, named)}, nil
		}
		source := "agent"
		if request := command.Options.Multiview; request != nil && request.Model != "" && modelMatches(offering, request.Model) {
			// The model came from the person's structured multiview request.
			source = "user"
		}
		return offeringChoice{Offering: offering, Source: source}, nil
	}
	if offering, profile, found := executor.profileOffering(command, capability, input); found {
		// The workspace picker shows a model the person may already consider
		// chosen, even when it is not an explicit lock. A purpose recommendation
		// that differs must not replace that visible choice silently: the Agent
		// asks the person to confirm the switch (#103 review C1).
		workspace := strings.TrimSpace(executor.userModel(command))
		if workspace != "" && !modelMatches(offering, workspace) {
			if text := executor.modelWorkspaceConflictText(command, workspace, offering, profile); text != "" {
				return offeringChoice{Refusal: text}, nil
			}
		}
		return offeringChoice{Offering: offering, Source: "profile", Profile: profile.ID, Reason: profile.Reason}, nil
	}
	model := ""
	if tool.IgnoreUserModel {
		// The tool runs models outside the workspace picker (the multiview
		// tool): its declared models are the names it can run, and only one
		// that accepts this input may run. There is no first-available
		// fallback, which could pick an Offering the input does not fit.
		declared := declaredModelChoices(tool)
		for _, name := range declared {
			offering, matched := matchOffering(candidates, name)
			if matched && executor.acceptsModelInput(offering, input) {
				return offeringChoice{Offering: offering, Source: "default"}, nil
			}
		}
		name := ""
		if len(declared) > 0 {
			name = declared[0]
		}
		return offeringChoice{Refusal: executor.modelUnavailableText(command, candidates, name)}, nil
	}
	// Without a purpose recommendation (or a brief at all), the workspace's
	// selected model is still the legacy fallback. It competes only here,
	// below the explicit lock, the Agent and the purpose.
	model = strings.TrimSpace(executor.userModel(command))
	offering, err := executor.selectOffering(capability, tool, model)
	if err != nil {
		return offeringChoice{}, err
	}
	source := "default"
	if model != "" && modelMatches(offering, model) {
		source = "user"
	}
	return offeringChoice{Offering: offering, Source: source}, nil
}

// declaredModelChoices is the enum of the tool's model argument, in order.
func declaredModelChoices(tool catalog.ChatToolSchema) []string {
	if tool.ModelArgument == "" {
		return nil
	}
	var parameters struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		return nil
	}
	return parameters.Properties[tool.ModelArgument].Enum
}

// modelLabel names a model for the Agent: the localized name of the Offering
// the name selects when this tool knows it, else the name as written.
func (executor *Executor) modelLabel(command chat.ToolCommand, name string) string {
	for _, offering := range executor.toolOfferings(command.Tool) {
		if modelMatches(offering, name) {
			return executor.localized(command, OfferingModelName(offering))
		}
	}
	return name
}

// modelChoicesText lists the models a call could run instead.
func (executor *Executor) modelChoicesText(command chat.ToolCommand, candidates []generation.Offering) string {
	labels := make([]string, 0, len(candidates))
	for _, offering := range candidates {
		label := executor.localized(command, OfferingModelName(offering))
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, ", ")
}

func (executor *Executor) modelUnavailableText(command chat.ToolCommand, candidates []generation.Offering, name string) string {
	template := executor.chat.Presentation.Messages.ModelChoiceUnavailable
	if template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{model}", executor.modelLabel(command, name),
		"{choices}", executor.modelChoicesText(command, candidates),
	).Replace(executor.localized(command, template))
}

func (executor *Executor) modelConflictText(command chat.ToolCommand, locked generation.Offering, requested string) string {
	template := executor.chat.Presentation.Messages.ModelChoiceConflict
	if template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{locked}", executor.localized(command, OfferingModelName(locked)),
		"{requested}", executor.modelLabel(command, requested),
	).Replace(executor.localized(command, template))
}

// modelWorkspaceConflictText asks the person to confirm a purpose
// recommendation that differs from the model still selected in the workspace,
// instead of replacing the visible choice silently (#103 review C1).
func (executor *Executor) modelWorkspaceConflictText(
	command chat.ToolCommand,
	workspace string,
	recommended generation.Offering,
	profile catalog.ChatModel3DUsageProfile,
) string {
	template := executor.chat.Presentation.Messages.ModelChoiceWorkspaceConflict
	if template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{workspace}", executor.modelLabel(command, workspace),
		"{recommended}", executor.localized(command, OfferingModelName(recommended)),
		"{profile}", profile.Label,
	).Replace(executor.localized(command, template))
}

// modelChoiceFailure is the Schema's refusal with no submission: the Agent
// sees the reason and that the person must decide, not a generic failure.
func (executor *Executor) modelChoiceFailure(command chat.ToolCommand, message string) chat.ToolResult {
	message = executor.localized(command, message)
	content, _ := json.Marshal(map[string]any{
		command.Tool.ResultFields.Success: false,
		command.Tool.ResultFields.Error:   message,
		"service_confirmation_required":   true,
	})
	return chat.ToolResult{Error: message, AgentContent: string(content), NoSubmission: true}
}

// modelInputFor is the input-shape test of a call being selected, before its
// input is built.
func modelInputFor(command chat.ToolCommand, referenceURLs []string, pictures *multiviewPictures) modelInput {
	input := modelInput{References: len(referenceURLs)}
	if pictures != nil {
		input.Views = pictures.Views
		input.Group = !pictures.fromRequest || (command.Options.Multiview != nil && command.Options.Multiview.Group != "")
	}
	return input
}

// modelInputOf is the input-shape test of a frozen Task input, used to name
// who chose the model when the Task is read back.
func modelInputOf(command chat.ToolCommand, offering generation.Offering, input json.RawMessage) modelInput {
	result := modelInput{Group: command.Options.Multiview == nil || command.Options.Multiview.Group != ""}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return result
	}
	for _, parameter := range offering.Parameters {
		switch parameter.Semantic {
		case generation.SemanticReferenceImage:
			if parameter.Type == "asset-array" {
				var list []json.RawMessage
				if json.Unmarshal(values[parameter.Name], &list) == nil {
					result.References = len(list)
				}
			} else if _, exists := values[parameter.Name]; exists {
				result.References = 1
			}
		case generation.SemanticReferenceViews:
			var views []string
			if json.Unmarshal(values[parameter.Name], &views) == nil {
				result.Views = views
			}
		}
	}
	return result
}

// modelChoiceOf names who chose the model of a step that ran on offering. It
// is derived from the frozen turn options and the task input, so a resumed
// turn classifies it the same way as the first attempt.
func (executor *Executor) modelChoiceOf(command chat.ToolCommand, offering generation.Offering, input json.RawMessage) *modelChoice {
	if command.Tool.ResultFields.ModelChoice == "" {
		return nil
	}
	if request := command.Options.Multiview; request != nil && request.Model != "" && modelMatches(offering, request.Model) {
		return &modelChoice{Source: "user"}
	}
	locked := strings.TrimSpace(command.Options.ExplicitModel3D)
	if locked != "" && modelMatches(offering, locked) {
		return &modelChoice{Source: "user"}
	}
	named := ""
	if command.Tool.ModelArgument != "" && len(command.Call.Arguments) > 0 {
		var arguments map[string]json.RawMessage
		if json.Unmarshal(command.Call.Arguments, &arguments) == nil {
			_ = json.Unmarshal(arguments[command.Tool.ModelArgument], &named)
		}
	}
	if strings.TrimSpace(named) != "" && modelMatches(offering, named) {
		return &modelChoice{Source: "agent"}
	}
	if chosen, profile, found := executor.profileOffering(command, offering.Capability, modelInputOf(command, offering, input)); found &&
		chosen.ID == offering.ID {
		return &modelChoice{Source: "profile", Profile: profile.ID, Reason: profile.Reason}
	}
	// The workspace's selected model is the legacy fallback below the purpose;
	// it is not the session's explicit lock, but it did choose this step.
	if !command.Tool.IgnoreUserModel {
		if model := strings.TrimSpace(executor.userModel(command)); model != "" && modelMatches(offering, model) {
			return &modelChoice{Source: "user"}
		}
	}
	return &modelChoice{Source: "default"}
}
