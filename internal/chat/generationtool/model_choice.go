package generationtool

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The purpose profile, the person's explicit pick, the Agent's own model
// argument and the plan's pin decide which model a step runs (#103, #380). The
// layers, highest first: the person's explicit pick in the session, this
// turn's structured request, the Agent's model argument, the approved plan's
// pin, the production purpose's recommendation, the workspace picker and the
// Offering defaults. chat.ResolveModel owns the order, so the production plan
// quotes a step with the same rule (#380 step 1).

// Shapes of a 3D input, the keys of model3d_usage.recommended_offerings.
const (
	shapeTextTo3D      = chat.ModelShapeTextTo3D
	shapeImageTo3D     = chat.ModelShapeImageTo3D
	shapeMultiviewTo3D = chat.ModelShapeMultiviewTo3D
)

// Shapes of a 2D input, the keys of image2d_usage.recommended_offerings. They
// are also the image Capability ids (#239).
const (
	shapeTextToImage  = chat.ModelShapeTextToImage
	shapeImageToImage = chat.ModelShapeImageToImage
)

// purposeProfile is the part of a 3D or 2D purpose the model-choice layers
// read: its identity, recommendations and public parameter overrides. Using
// one shape lets the same four layers serve both domains (#103, #239). The
// shape and the resolution of its parameter values are shared with the direct
// task API (chat.PurposeParameters, #295).
type purposeProfile = chat.PurposeProfile

// purposeParameters is the resolver of what the session brief gives an
// Offering, over the Schema tables this executor was built with.
func (executor *Executor) purposeParameters() chat.PurposeParameters {
	return chat.PurposeParameters{Model3D: executor.chat.Model3DUsage, Image2D: executor.chat.Image2DUsage}
}

// modelChoice is the public result object naming who chose the model of a
// step; it carries the purpose id and reason only when the purpose chose.
type modelChoice struct {
	Source  string `json:"source"`
	Profile string `json:"profile,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// offeringChoice is one selection and why it was made. A Refusal is the
// Schema's message for the Agent, and Code the refusal's name in the server
// log (#378); nothing is submitted then.
type offeringChoice struct {
	Offering generation.Offering
	Source   string
	Profile  string
	Reason   string
	Refusal  string
	Code     string
}

// modelInput is the part of a call's input that decides whether an Offering
// accepts it: how many reference pictures it gives and, for a multiview step,
// which views.
type modelInput struct {
	References int
	Views      []string
	Group      bool
}

// briefPurpose returns the 3D purpose profile the session's production brief
// selects. A session without a brief has none: nothing is recommended or
// overridden by a purpose. The profile is already the variant for the brief's
// target platform when the purpose declares one (#275), so its budget and
// parameter overrides are what every later layer reads; a session without a
// platform gets the purpose as declared.
func (executor *Executor) briefPurpose(command chat.ToolCommand) (catalog.ChatModel3DUsageProfile, bool) {
	return executor.purposeParameters().Model3DProfile(command.Options.ProductionBrief)
}

// imageBriefPurpose returns the 2D purpose the session follows (#239). The
// brief's image_profile field selects it; when that field is absent the
// Schema's default_profile applies, as the image_use description and the
// system prompt promise ("omit or leave empty to let the default 2D purpose
// apply"). A purpose a stale brief names but the Schema no longer declares is
// ignored the same way a session without one, matching the 3D usage check.
func (executor *Executor) imageBriefPurpose(command chat.ToolCommand) (purposeProfile, bool) {
	return executor.purposeParameters().Image2DProfile(command.Options.ProductionBrief, false)
}

// purposeOf is the purpose of this call's workspace domain: the 2D table for
// the image workspace, the 3D table otherwise (#239).
func (executor *Executor) purposeOf(command chat.ToolCommand) (purposeProfile, bool) {
	return executor.purposeParameters().Profile(toolDomain(command), command.Options.ProductionBrief, false)
}

// profileParameterValues are the values the brief's purpose applies to an
// Offering: its parameter overrides and the values its budget gives the
// bounded parameters (#129). The Catalog has already checked every override at
// load time; this repeats the public-parameter rule so a hand-built Executor
// cannot apply a value the Offering does not expose to the Agent.
func (executor *Executor) profileParameterValues(command chat.ToolCommand, offering generation.Offering) (purposeProfile, map[string]json.RawMessage) {
	return executor.purposeParameterValues(command, offering, nil)
}

// purposeParameterValues is profileParameterValues for a step whose other
// values are already known: current supplies them (a remesh topology) when
// the budget measures a face count. The resolution is the one the direct task
// API applies to a request (#295).
func (executor *Executor) purposeParameterValues(command chat.ToolCommand, offering generation.Offering,
	current map[string]json.RawMessage) (purposeProfile, map[string]json.RawMessage) {
	return executor.purposeParameters().Values(chat.PurposeQuery{
		Brief: command.Options.ProductionBrief, Domain: toolDomain(command), Offering: offering,
		ToolDefaults: command.Tool.DefaultParameters, Current: current,
	})
}

// modelShape is the recommended_offerings key of this call. For an image call
// it is the call's capability (text-to-image or image-to-image); for 3D it
// names the input shape (#103, #239).
func (executor *Executor) modelShape(command chat.ToolCommand, capability generation.Capability) string {
	if toolDomain(command) == string(generation.DomainImage) {
		return chat.ModelShapeOfCapability(capability)
	}
	if tool := executor.chat.ThreeView.Multiview.Tool; tool != "" && command.Tool.Name == tool {
		return shapeMultiviewTo3D
	}
	return chat.ModelShapeOfCapability(capability)
}

// modelMatches reports whether a name selects an Offering: its model ID, its
// Offering ID, or the provider and model ID pair the Catalog also shows.
func modelMatches(offering generation.Offering, name string) bool {
	return chat.ModelNameMatches(offering, name)
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
	return chat.MatchModel(candidates, name)
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
	accepted, ordered, declared := multiviewViewsOf(offering, multiview)
	if !declared {
		return false
	}
	if ordered {
		return orderedViewPrefix(views, accepted, multiview.RequiredView) &&
			len(views) >= multiview.MinimumViews
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
func (executor *Executor) profileOffering(command chat.ToolCommand, capability generation.Capability, input modelInput) (generation.Offering, purposeProfile, bool) {
	profile, found := executor.purposeOf(command)
	if !found {
		return generation.Offering{}, purposeProfile{}, false
	}
	candidates := executor.availableOfferings(capability)
	for _, name := range profile.RecommendedOfferings[executor.modelShape(command, capability)] {
		offering, matched := matchOffering(candidates, name)
		if matched && executor.acceptsModelInput(offering, input) {
			return offering, profile, true
		}
	}
	return generation.Offering{}, purposeProfile{}, false
}

// chooseOffering selects the Offering of a generation tool call through
// chat.ResolveModel: the person's explicit pick for the tool's workspace
// domain, this turn's structured multiview request, the Agent's model
// argument, the plan's pin, the purpose recommendation, the workspace pick and
// the Offering default, in that order (#103, #239, #380). The multiview tool of
// a turn that carries the person's structured multiview request takes the
// request's model as that explicit pick instead of the session lock (#373). It
// returns a Refusal when a named model conflicts with the person's explicit
// pick or is unavailable; nothing is submitted in that case.
func (executor *Executor) chooseOffering(
	command chat.ToolCommand,
	capability generation.Capability,
	arguments map[string]json.RawMessage,
	input modelInput,
	planPin string,
) (offeringChoice, error) {
	tool := command.Tool
	named := ""
	if tool.ModelArgument != "" {
		_ = json.Unmarshal(arguments[tool.ModelArgument], &named)
		named = strings.TrimSpace(named)
	}
	// Only the session marker is the person's explicit lock. The workspace's
	// per-domain model is the picker's current value, which may merely be
	// restored from another session or a previous visit, so it must not keep
	// the purpose from choosing. The lock applies to every tool of its domain,
	// the multiview tool included: ignore_user_model only keeps the workspace
	// picker out of its fallback. When the turn carries a plan, the lock is the
	// one the plan froze for the capability: a lock that names another
	// capability's model is not this step's decision (#380 step 2), and the
	// production plan reads the same value.
	lock, lockSource := executor.planModelLock(command, capability), chat.ModelSourceLock
	// The person's structured multiview request names the model of its own
	// turn: they chose it on the multiview panel or the three-view card, so it
	// is their explicit pick for this step and replaces the session lock here,
	// where the lock would refuse a model that takes the views or one the
	// session's lock cannot serve (#373). The stored lock is not touched.
	if requested := executor.requestedMultiviewModel(command); requested != "" {
		lock, lockSource = requested, chat.ModelSourceTurn
	}
	workspace := ""
	if !tool.IgnoreUserModel {
		workspace = strings.TrimSpace(executor.userModel(command))
	}
	profile, recommended := executor.purposeRecommendation(command, capability)
	// The Gateway narrows the Offerings to the capability; the executor keeps
	// whatever it returns, as the no-model fallback always has (a hand-built
	// Gateway that lists more keeps the behaviour its tests describe).
	var candidates []generation.Offering
	for _, offering := range executor.gateway.Offerings(capability) {
		if offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
			candidates = append(candidates, offering)
		}
	}
	resolution := chat.ResolveModel(chat.ModelRequest{
		Candidates:  candidates,
		Lock:        lock,
		LockSource:  lockSource,
		Named:       named,
		PlanPinned:  planPin,
		Recommended: recommended,
		Profile:     profile.ID,
		Reason:      profile.Reason,
		Workspace:   workspace,
		// A Catalog that declares the confirmation text asks the person before
		// a purpose recommendation replaces the model they see (#103 C1).
		ConfirmsWorkspace: executor.chat.Presentation.Messages.ModelChoiceWorkspaceConflict != "",
		Fits:              executor.modelFits(command, input),
		Views:             executor.modelViews(command, input),
		SupportsMask:      executor.maskBinding(command),
		Default:           executor.modelDefault(tool),
	})
	if resolution.Conflict != nil {
		return executor.modelConflictChoice(command, capability, input, profile.Label, resolution.Conflict), nil
	}
	choice := offeringChoice{Offering: resolution.Offering, Source: resolution.ChoiceSource()}
	if resolution.Source == chat.ModelSourcePurpose {
		choice.Profile, choice.Reason = resolution.Profile, resolution.Reason
	}
	// The model came from the person's structured multiview request, even when
	// another tool of the turn names it as its own argument.
	if choice.Source == "agent" && executor.namesTurnRequestModel(command, resolution.Offering) {
		choice.Source = "user"
	}
	return choice, nil
}

// ChooseThreeViewModel is the model choice of the three-view tool (#409): the
// person's session lock, the workspace pick, the production purpose's
// recommendation and the Offering default, in chat.ResolveModel's order, the
// same layers refine_image uses. The result is empty when no Offering can take
// the call; the caller words that refusal. It is a shell over chooseOffering so
// the Skill tool stops carrying a second, weaker selection rule.
func (executor *Executor) ChooseThreeViewModel(command chat.ToolCommand) (generation.Offering, string) {
	choice, _ := executor.chooseOffering(command, generation.ImageToImage, nil, modelInput{}, "")
	if choice.Offering.ID == "" {
		return generation.Offering{}, ""
	}
	return choice.Offering, choice.Source
}

// purposeRecommendation is the production purpose of the call's workspace
// domain and the models it recommends for the call's input shape (#103).
func (executor *Executor) purposeRecommendation(command chat.ToolCommand, capability generation.Capability) (purposeProfile, []string) {
	profile, found := executor.purposeOf(command)
	if !found {
		return purposeProfile{}, nil
	}
	return profile, chat.RecommendedOfferings(profile, executor.modelShape(command, capability))
}

// bindsMask reports whether this call writes the person's frozen selection
// mask into the Offering (#244).
func bindsMask(command chat.ToolCommand) bool {
	return command.Tool.SelectionMask == selectionMaskBinding && command.Options.SelectionMask != nil
}

// modelFits is the input test of a purpose recommendation and of the frozen
// mask: an Offering the call cannot give its reference pictures, its views or
// its mask to is not a recommendation.
func (executor *Executor) modelFits(command chat.ToolCommand, input modelInput) func(generation.Offering) bool {
	mask := bindsMask(command)
	return func(offering generation.Offering) bool {
		if !executor.acceptsModelInput(offering, input) {
			return false
		}
		return !mask || offeringSupportsMask(offering)
	}
}

// modelViews is the view test of the call's Offering, nil for a step without
// structured views. Only the model family substitution reads it: the step's own
// view check words a model that cannot take the views.
func (executor *Executor) modelViews(command chat.ToolCommand, input modelInput) func(generation.Offering) bool {
	if len(input.Views) == 0 {
		return nil
	}
	views := modelInput{Views: input.Views, Group: input.Group}
	return func(offering generation.Offering) bool {
		return executor.acceptsModelInput(offering, views)
	}
}

// maskBinding is the frozen-selection-mask test of the call's Offering, nil
// when the call binds no mask.
func (executor *Executor) maskBinding(command chat.ToolCommand) func(generation.Offering) bool {
	if !bindsMask(command) {
		return nil
	}
	return offeringSupportsMask
}

// modelDefault is the Offering when no layer above chose one. A tool that
// ignores the workspace picker (the multiview tool) runs the first of its
// declared model choices that fits, in Catalog order: its declared models are
// the names it can run, and there is no first-available fallback that could
// pick an Offering the input does not fit. Every other tool takes the
// Offering's default flag, then its recommended flag, then the first.
func (executor *Executor) modelDefault(tool catalog.ChatToolSchema) func([]generation.Offering) (generation.Offering, bool) {
	if !tool.IgnoreUserModel {
		return nil
	}
	declared := executor.declaredModelChoices(tool)
	return func(candidates []generation.Offering) (generation.Offering, bool) {
		for _, name := range declared {
			for _, candidate := range candidates {
				if modelMatches(candidate, name) {
					return candidate, true
				}
			}
		}
		return generation.Offering{}, false
	}
}

// namesTurnRequestModel reports whether this turn's structured multiview
// request names the Offering: the request is the person's choice of the turn.
func (executor *Executor) namesTurnRequestModel(command chat.ToolCommand, offering generation.Offering) bool {
	request := command.Options.Multiview
	return request != nil && strings.TrimSpace(request.Model) != "" && modelMatches(offering, request.Model)
}

// modelConflictChoice words a refused resolution with the Schema's copy: the
// person's pick against another named model, a frozen mask no chosen model can
// bind, or a model the call cannot use now.
func (executor *Executor) modelConflictChoice(
	command chat.ToolCommand,
	capability generation.Capability,
	input modelInput,
	profileLabel string,
	conflict *chat.ModelConflict,
) offeringChoice {
	if conflict.Kind == chat.ModelConflictLocked {
		return offeringChoice{
			Refusal: executor.modelConflictText(command, conflict.Locked, conflict.Named),
			Code:    refusalModelConflict,
		}
	}
	if conflict.Kind == chat.ModelConflictWorkspace {
		return offeringChoice{
			Refusal: executor.modelWorkspaceConflictText(command, conflict.Named, conflict.Offering, profileLabel),
			Code:    refusalModelWorkspaceConflict,
		}
	}
	if conflict.Offering.ID != "" && bindsMask(command) && !offeringSupportsMask(conflict.Offering) {
		candidates := executor.maskCapableOfferings(capability, input)
		if len(candidates) == 0 {
			return offeringChoice{
				Refusal: executor.localized(command, executor.chat.Presentation.Messages.MaskNoSupportedModel),
				Code:    refusalMask,
			}
		}
		return offeringChoice{
			Refusal: strings.NewReplacer(
				"{model}", executor.localized(command, OfferingModelName(conflict.Offering)),
				"{choices}", executor.modelChoicesText(command, candidates),
			).Replace(executor.localized(command, executor.chat.Presentation.Messages.MaskUnsupportedTemplate)),
			Code: refusalMask,
		}
	}
	return offeringChoice{
		Refusal: executor.modelUnavailableText(command, conflict.Candidates, conflict.Named),
		Code:    refusalModelUnavailable,
	}
}

// planModelLock is the person's lock as the turn's frozen plan applies it to
// the capability (#380 step 2): the session's explicit model for the tool's
// workspace domain, or empty when the plan says the capability is not the one
// the lock names. A turn stored without a plan (or a service with no Catalog)
// keeps the session's lock unchanged.
func (executor *Executor) planModelLock(command chat.ToolCommand, capability generation.Capability) string {
	if entry, ok := command.Options.TurnPlan.Domain(toolDomain(command), string(capability)); ok {
		return entry.Lock
	}
	return strings.TrimSpace(command.Options.ExplicitModelFor(toolDomain(command)))
}

// requestedMultiviewModel is the model the person's structured multiview
// request names for this call (#373), empty for any other tool or a turn
// without the request. The request binds only the multiview tool: another 3D
// tool of the same turn still follows the session's lock.
func (executor *Executor) requestedMultiviewModel(command chat.ToolCommand) string {
	request := command.Options.Multiview
	tool := executor.chat.ThreeView.Multiview.Tool
	if request == nil || tool == "" || command.Tool.Name != tool {
		return ""
	}
	return strings.TrimSpace(request.Model)
}

// declaredModelChoices is the model choices of the tool's model argument, in
// order. A tool that declares model_choices derives them from the enabled,
// runtime-available Offerings (#249), so telling the Agent what it may run and
// what it can run stay one set. A historical catalog keeps its handwritten
// enum.
func (executor *Executor) declaredModelChoices(tool catalog.ChatToolSchema) []string {
	if tool.ModelChoices != nil {
		var candidates []generation.Offering
		for _, capability := range tool.Execution.Capabilities {
			for _, offering := range executor.gateway.Offerings(capability) {
				if offering.Capability == capability && offering.Enabled &&
					executor.gateway.OfferingAvailable(offering.ID) {
					candidates = append(candidates, offering)
				}
			}
		}
		return catalog.DerivedModelChoices(tool, candidates, nil)
	}
	return declaredModelChoices(tool)
}

// declaredModelChoices reads the enum of the tool's model argument, in order.
// It serves Catalogs written before model_choices existed (#249).
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
	profileLabel string,
) string {
	template := executor.chat.Presentation.Messages.ModelChoiceWorkspaceConflict
	if template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{workspace}", executor.modelLabel(command, workspace),
		"{recommended}", executor.localized(command, OfferingModelName(recommended)),
		"{profile}", profileLabel,
	).Replace(executor.localized(command, template))
}

// modelChoiceFailure is the Schema's refusal with no submission: the Agent
// sees the reason and that the person must decide, not a generic failure. The
// server log records the refusal under code (#378).
func (executor *Executor) modelChoiceFailure(command chat.ToolCommand, code, message string) chat.ToolResult {
	executor.logRefusal(command, code)
	message = executor.localized(command, message)
	content, _ := json.Marshal(map[string]any{
		command.Tool.ResultFields.Success: false,
		command.Tool.ResultFields.Error:   message,
		"service_confirmation_required":   true,
	})
	return chat.ToolResult{Error: message, AgentContent: string(content), NoSubmission: true}
}

// modelInputFor is the input-shape test of a call being selected, before its
// input is built. A group's pictures may hold a view the Offering does not
// name; the call's own pictures must all be taken (#433).
func modelInputFor(referenceURLs []string, pictures *multiviewPictures) modelInput {
	input := modelInput{References: len(referenceURLs)}
	if pictures != nil {
		input.Views = pictures.Views
		input.Group = pictures.group
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
	if executor.lockChoseOffering(command, offering) {
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

// lockChoseOffering reports whether the person's lock names the Offering that
// ran, either itself or the same model family's shape (#417): a lock on Meshy
// 7.1 chose the multiview shape a multiview step ran.
func (executor *Executor) lockChoseOffering(command chat.ToolCommand, offering generation.Offering) bool {
	locked := executor.planModelLock(command, offering.Capability)
	if locked == "" {
		return false
	}
	if modelMatches(offering, locked) {
		return true
	}
	named, found := chat.MatchModel(executor.gateway.Offerings(offering.Capability), locked)
	return found && chat.SameModelFamily(named, offering)
}
