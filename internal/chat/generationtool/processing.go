package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// taskLister is the optional read side of the Gateway used to find a
// session's finished models. It is always owner scoped.
type taskLister interface {
	List(context.Context, generation.TaskListScope, generation.ListQuery) (generation.TaskPage, error)
}

const (
	sessionTaskPageSize = 50
	// sessionTaskMaxPages bounds the scan for models so a long session
	// cannot turn one tool call into an unbounded read.
	sessionTaskMaxPages = 4
)

// sessionModelTasks returns the owner's succeeded tasks of this session that
// delivered a saved model file, newest first, at most limit of them.
func (executor *Executor) sessionModelTasks(ctx context.Context, owner, session string, limit int) ([]generation.Task, error) {
	lister, ok := executor.gateway.(taskLister)
	if !ok || owner == "" || session == "" {
		return nil, errors.New("generation tool: task listing unavailable")
	}
	var found []generation.Task
	cursor := ""
	for page := 0; page < sessionTaskMaxPages && len(found) < limit; page++ {
		result, err := lister.List(ctx, generation.OwnTasks(generation.OwnerID(owner)), generation.ListQuery{
			SessionID: session, States: []generation.TaskState{generation.TaskSucceeded},
			Cursor: cursor, Limit: sessionTaskPageSize,
		})
		if err != nil {
			return nil, err
		}
		for _, task := range result.Tasks {
			if string(task.OwnerID) != owner || task.SessionID != session ||
				task.State != generation.TaskSucceeded || !hasSavedModel(task) {
				continue
			}
			found = append(found, task)
			if len(found) >= limit {
				break
			}
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return found, nil
}

func hasSavedModel(task generation.Task) bool {
	for _, asset := range task.Assets {
		if asset.Role == modelRole && asset.StorageKey != "" {
			return true
		}
	}
	return false
}

const modelRole = "model"

// ModelAssetRole is the asset role of a saved 3D model file.
const ModelAssetRole = modelRole

// sessionModelView is what list_session_models shows the Agent: platform IDs
// and public facts only. It never carries an upstream job ID or URL.
type sessionModelView struct {
	TaskID       string                 `json:"task_id"`
	Capability   string                 `json:"capability"`
	Provider     string                 `json:"provider"`
	ProviderID   string                 `json:"provider_id"`
	Model        string                 `json:"model"`
	CreatedAt    string                 `json:"created_at"`
	Formats      []string               `json:"formats"`
	ModelAssets  []sessionModelAsset    `json:"model_assets"`
	HasThumbnail bool                   `json:"has_thumbnail"`
	NextSteps    []sessionModelNextStep `json:"next_steps"`
	// ExportFormats is the format coverage the 格式 tab shows (#321): format
	// ID → available, obtainable, converting or unavailable. Only these say
	// which formats can be downloaded or converted.
	ExportFormats map[string]string `json:"export_formats,omitempty"`
	// Version is the label the version strip and the approval card show
	// for the model ("3D v5", #326 L).
	Version string `json:"version,omitempty"`
}

// ModelFormatStatus reads a finished model's format coverage, owner scoped:
// format ID → its status. It is the source of the 格式 tab.
type ModelFormatStatus func(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (map[string]string, error)

// SetModelFormats wires the format coverage into list_session_models.
func (executor *Executor) SetModelFormats(read ModelFormatStatus) { executor.modelFormats = read }

// ModelVersions reads the version numbers the page shows for a session's
// Tasks (the version strip's "3D v5"), owner scoped (#326 L).
type ModelVersions func(ctx context.Context, owner generation.OwnerID, session string) (map[generation.TaskID]int, error)

// SetModelVersions wires the page's version numbers into the tool results.
func (executor *Executor) SetModelVersions(read ModelVersions) { executor.modelVersions = read }

// versionLabels names the session's models by the label the page shows
// (chat.context.model_version_template); nil when that is not known.
func (executor *Executor) versionLabels(ctx context.Context, owner, session string) map[generation.TaskID]string {
	template := executor.chat.Context.ModelVersionTemplate
	if executor.modelVersions == nil || template == "" {
		return nil
	}
	versions, err := executor.modelVersions(ctx, generation.OwnerID(owner), session)
	if err != nil {
		executor.logger.Warn("model versions unavailable for the tool result", "session_id", session, "error", err)
		return nil
	}
	labels := make(map[generation.TaskID]string, len(versions))
	for id, version := range versions {
		if version > 0 {
			labels[id] = fill(template, "{version}", strconv.Itoa(version))
		}
	}
	return labels
}

// sessionModelAsset is one saved model file of a listed model, named by its
// public format (#326: never by the Provider's result field). Main marks the
// model's main file, the one a step binds when only the model is named.
type sessionModelAsset struct {
	AssetID   string `json:"asset_id"`
	Format    string `json:"format,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Main      bool   `json:"main,omitempty"`
}

// sessionModelNextStep names a processing tool call whose Offering input
// contract accepts this task as its source.
type sessionModelNextStep struct {
	Tool string `json:"tool"`
	Step string `json:"step,omitempty"`
	// Services are the available services that take this model for the
	// step (#321 I): only these can be offered for it.
	Services []sessionModelService `json:"services,omitempty"`
}

// sessionModelService names a service by provider and model; its Offering ID
// is for the tool call only.
type sessionModelService struct {
	OfferingID string `json:"offering_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

// ListSessionModels backs the list_session_models tool: this owner's
// succeeded 3D tasks of this session with the processing steps each can feed.
func (executor *Executor) ListSessionModels(ctx context.Context, ownerID, sessionID string) (json.RawMessage, error) {
	tasks, err := executor.sessionModelTasks(ctx, ownerID, sessionID, executor.chat.Agent.MaxListedModels)
	if err != nil {
		return nil, err
	}
	locale := catalog.LocaleFrom(ctx)
	localize := func(text string) string {
		if executor.texts == nil {
			return text
		}
		return executor.texts.Text(locale, text)
	}
	views := make([]sessionModelView, 0, len(tasks))
	labels := executor.versionLabels(ctx, ownerID, sessionID)
	for _, task := range tasks {
		view := sessionModelView{
			TaskID: string(task.ID), Capability: string(task.Capability),
			Provider: localize(task.Offering.ProviderName), ProviderID: task.Offering.ProviderID,
			Model: localize(task.Offering.ModelName), CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339),
			Formats: []string{}, ModelAssets: []sessionModelAsset{}, NextSteps: executor.nextSteps(task, localize),
			Version: labels[task.ID],
		}
		main := preferredModelAsset(task, executor.sourcePreference())
		for _, asset := range task.Assets {
			switch {
			case asset.Role == "thumbnail" && asset.StorageKey != "":
				view.HasThumbnail = true
			case asset.Role == modelRole && asset.StorageKey != "":
				format := executor.publicFormat(asset)
				view.ModelAssets = append(view.ModelAssets, sessionModelAsset{
					AssetID: string(asset.ID), Format: format, MediaType: asset.MediaType, Main: asset.ID == main})
				if format != "" && !slices.Contains(view.Formats, format) {
					view.Formats = append(view.Formats, format)
				}
			}
		}
		if executor.modelFormats != nil {
			if formats, err := executor.modelFormats(ctx, generation.OwnerID(ownerID), task.ID); err == nil && len(formats) > 0 {
				view.ExportFormats = formats
			} else if err != nil {
				executor.logger.Warn("format coverage unavailable for the model listing", "task_id", task.ID, "error", err)
			}
		}
		views = append(views, view)
	}
	return json.Marshal(map[string]any{"models": views})
}

// nextSteps lists the processing tool calls whose available Offering accepts
// the task as a source, using the same binding rule the tools apply.
func (executor *Executor) nextSteps(task generation.Task, localize func(string) string) []sessionModelNextStep {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	steps := []sessionModelNextStep{}
	for _, tool := range executor.chat.Tools {
		if tool.Processing == nil || tool.Processing.Plan != nil {
			continue
		}
		type candidate struct {
			step       string
			capability generation.Capability
		}
		var candidates []candidate
		if tool.Processing.StepArgument == "" {
			candidates = append(candidates, candidate{"", tool.Capability})
		} else {
			for step, capability := range tool.Processing.Steps {
				candidates = append(candidates, candidate{step, capability})
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].step < candidates[j].step })
		}
		for _, candidate := range candidates {
			// The main model is what the panel and the tools bind (#326): an
			// attachment that would fit is not a reason to offer a step.
			compatible := executor.compatibleOfferings(candidate.capability, task,
				preferredModelAsset(task, tool.Processing.SourceVariantPreference), tool.Processing)
			if len(compatible) == 0 {
				continue
			}
			step := sessionModelNextStep{Tool: tool.Name, Step: candidate.step}
			for _, service := range compatible {
				step.Services = append(step.Services, sessionModelService{OfferingID: string(service.offering.ID),
					Provider: text(service.offering.ProviderName), Model: text(service.offering.ModelName)})
			}
			steps = append(steps, step)
		}
	}
	return steps
}

func (executor *Executor) bindableOffering(
	capability generation.Capability, task generation.Task, asset generation.AssetID,
	processing *catalog.ChatToolProcessingSchema,
) (selectedOffering, bool) {
	for _, offering := range executor.gateway.Offerings(capability) {
		if !offering.Enabled || !executor.gateway.OfferingAvailable(offering.ID) {
			continue
		}
		binding, err := generation.BindModelSource(offering, task, asset, processing.SourceVariantPreference)
		if err == nil {
			return selectedOffering{offering: offering, binding: binding}, true
		}
	}
	return selectedOffering{}, false
}

// offeringInputValidator is the kernel's read-only input check, the one the
// format coverage applies before it offers a route.
type offeringInputValidator interface {
	ValidateOfferingInput(ctx context.Context, owner generation.OwnerID, offering generation.OfferingID,
		revision string, input json.RawMessage) error
}

// deliveryConversion picks the conversion route for a tool whose Schema names
// a delivery format argument, the way the format coverage does: the
// model_delivery.conversions of the requested format, in Schema order, whose
// Offering is available, binds the source and passes the kernel's own input
// validation (source trust, resource profile). A trusted platform-generated
// GLB therefore takes the generated-model route first, while a source no
// route accepts falls back to the ordinary Offering order and its reason.
//
// The services the person named come first (#330): when one of their routes
// can run, it is used; otherwise the Schema order decides, and the usual
// service question follows if that runs another paid service. A retired
// conversion is never a route. The second result reports a route that
// delivers every format it provides in one run (Meshy Convert): the requested
// format is then the route's own business, not an argument it declares.
func (executor *Executor) deliveryConversion(
	ctx context.Context, owner generation.OwnerID, capability generation.Capability,
	source generation.Task, asset generation.AssetID, processing *catalog.ChatToolProcessingSchema,
	arguments map[string]json.RawMessage, named map[generation.OfferingID]bool,
) (selectedOffering, bool, bool, error) {
	if len(named) > 0 {
		chosen, allFormats, found, err := executor.deliveryRoute(ctx, owner, capability, source, asset, processing, arguments, named)
		if found || err != nil {
			return chosen, allFormats, found, err
		}
	}
	return executor.deliveryRoute(ctx, owner, capability, source, asset, processing, arguments, nil)
}

func (executor *Executor) deliveryRoute(
	ctx context.Context, owner generation.OwnerID, capability generation.Capability,
	source generation.Task, asset generation.AssetID, processing *catalog.ChatToolProcessingSchema,
	arguments map[string]json.RawMessage, only map[generation.OfferingID]bool,
) (selectedOffering, bool, bool, error) {
	if processing.DeliveryFormatArgument == "" {
		return selectedOffering{}, false, false, nil
	}
	validator, validates := executor.gateway.(offeringInputValidator)
	requested := rawString(arguments[processing.DeliveryFormatArgument])
	if !validates || requested == "" {
		return selectedOffering{}, false, false, nil
	}
	listed := map[generation.OfferingID]generation.Offering{}
	for _, offering := range executor.gateway.Offerings(capability) {
		listed[offering.ID] = offering
	}
	for _, conversion := range executor.modelDelivery.Conversions {
		// A whole-run route names format IDs, so "FBX" asks for "fbx"; a
		// per-format route keeps its Offering's exact option value.
		provided := slices.ContainsFunc(conversion.Provides, func(format string) bool { return strings.EqualFold(format, requested) })
		for _, value := range conversion.FormatValues {
			provided = provided || value == requested
		}
		offering, exists := listed[generation.OfferingID(conversion.OfferingID)]
		if conversion.Retired || !provided || !exists || !offering.Enabled || !executor.gateway.OfferingAvailable(offering.ID) ||
			(only != nil && !only[offering.ID]) {
			continue
		}
		binding, err := generation.BindModelSource(offering, source, asset, processing.SourceVariantPreference)
		if err != nil {
			continue
		}
		input := map[string]json.RawMessage{}
		if len(conversion.Input) > 0 && json.Unmarshal(conversion.Input, &input) != nil {
			continue
		}
		if conversion.FormatParameter != "" && len(conversion.FormatValues) > 0 {
			input[conversion.FormatParameter], _ = json.Marshal(requested)
		}
		input[binding.Parameter], _ = json.Marshal(binding.Value)
		encoded, err := json.Marshal(input)
		if err != nil {
			continue
		}
		if err := validator.ValidateOfferingInput(ctx, owner, offering.ID, offering.Revision, encoded); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return selectedOffering{}, false, false, err
			}
			continue
		}
		allFormats := conversion.FormatParameter == "" && len(conversion.Provides) > 0
		return selectedOffering{offering: offering, binding: binding}, allFormats, true, nil
	}
	return selectedOffering{}, false, false, nil
}

// wholeRunProvides reports that an Offering chosen outside the delivery
// routes (the ordinary Offering order) is a whole-run conversion providing
// the requested format: the format argument is then not its input (#330).
func (executor *Executor) wholeRunProvides(offering generation.OfferingID, processing *catalog.ChatToolProcessingSchema,
	arguments map[string]json.RawMessage) bool {
	if processing.DeliveryFormatArgument == "" {
		return false
	}
	requested := rawString(arguments[processing.DeliveryFormatArgument])
	for _, conversion := range executor.modelDelivery.Conversions {
		if !conversion.Retired && conversion.FormatParameter == "" && generation.OfferingID(conversion.OfferingID) == offering &&
			slices.ContainsFunc(conversion.Provides, func(format string) bool { return strings.EqualFold(format, requested) }) {
			return true
		}
	}
	return false
}

type selectedOffering struct {
	offering generation.Offering
	binding  generation.ModelSourceBinding
}

// sourceIncompatibleText is the refusal of a step no available Offering
// accepts the source for. When an Offering refused only because the
// Provider no longer keeps the source's original task (#325), the Schema's
// source_expired text says so instead of the generic reason.
func sourceIncompatibleText(messages catalog.ChatToolProcessingMessages, capability generation.Capability, expired bool, localize func(string) string) string {
	template := messages.SourceIncompatible
	if expired && messages.SourceExpired != "" {
		template = messages.SourceExpired
	}
	if localize != nil {
		template = localize(template)
	}
	return fill(template, "{step}", string(capability))
}

// processingRejection is a deterministic, nothing-submitted refusal whose
// text the Agent can act on.
type processingRejection struct{ message string }

func (executor *Executor) reject(command chat.ToolCommand, message string) (chat.ToolResult, error) {
	return executor.rejectBecause(command, message, nil)
}

// rejectBecause refuses the call and logs the refusal with the call it
// belongs to and its internal cause (#321 A, #233): a refused tool call is
// otherwise invisible on the server.
func (executor *Executor) rejectBecause(command chat.ToolCommand, message string, cause error) (chat.ToolResult, error) {
	attributes := []any{"tool", command.Tool.Name, "call_id", command.Call.ID, "owner_id", command.OwnerID,
		"session_id", command.SessionID, "reason", message}
	var domain *generation.Error
	if cause != nil && errors.As(cause, &domain) {
		attributes = append(attributes, "code", domain.Code)
	}
	executor.logger.Warn("agent tool call refused", attributes...)
	// Translate the Schema template before any Offering ID is replaced: a
	// filled sentence no longer matches its bundle entry.
	message = executor.publicText(command, executor.localized(command, message))
	content, _ := json.Marshal(map[string]any{
		command.Tool.ResultFields.Success: false,
		command.Tool.ResultFields.Error:   message,
	})
	return chat.ToolResult{Error: message, AgentContent: string(content), NoSubmission: true}, nil
}

func fill(template string, replacements ...string) string {
	return strings.NewReplacer(replacements...).Replace(template)
}

// fillText translates the Schema template before interpolation: a filled
// sentence no longer matches its translation entry.
func (executor *Executor) fillText(command chat.ToolCommand, template string, replacements ...string) string {
	return fill(executor.localized(command, template), replacements...)
}

// formatRefusal says an explicitly requested format conflicts with the
// converter's fixed target. The format value is an identifier; the service
// name is Schema copy translated in the turn locale.
func (executor *Executor) formatRefusal(command chat.ToolCommand, offering generation.Offering, fixedFormat string) string {
	return fmt.Sprintf("%s requires target format %q",
		localizedServiceTitle(offering, executor.localizeText(command)), fixedFormat)
}

// executeProcessing runs a tool that processes an existing 3D model of the
// session. It resolves the source and the Offering from the Offering's own
// input contract, then hands the frozen input to the same PrepareInput →
// ExecutionGrant (session approval policy) → SubmitPrepared path as every
// other paid Agent tool. Nothing here can approve or submit by itself.
func (executor *Executor) executeProcessing(
	ctx context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	processing := command.Tool.Processing
	messages := processing.Messages
	if !executor.UsesExecutionGrants() {
		// Without exact grants the session approval policy cannot apply.
		return executor.reject(command, messages.Unavailable)
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(command.Call.Arguments, &arguments); err != nil || arguments == nil {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", "arguments must be a JSON object"))
	}
	capability := command.Tool.Capability
	if processing.StepArgument != "" {
		step := rawString(arguments[processing.StepArgument])
		var known bool
		capability, known = processing.Steps[step]
		if !known {
			return executor.reject(command, executor.fillText(command, messages.StepUnknown, "{step}", step))
		}
	}
	allowed := map[string]bool{processing.SourceTaskArgument: true, processing.SourceAssetArgument: true,
		processing.StepArgument: true, processing.PromptArgument: true, processing.ReferenceArgument: true,
		executor.chat.Context.ReferenceHandlesArgument: true}
	choice := processing.Choice
	if choice != nil {
		allowed[choice.OfferingArgument], allowed[choice.DefaultArgument] = true, true
	}
	for argument := range processing.ParameterArguments {
		allowed[argument] = true
	}
	names := make([]string, 0, len(arguments))
	for argument := range arguments {
		names = append(names, argument)
	}
	sort.Strings(names)
	for _, argument := range names {
		if !allowed[argument] || argument == "" {
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", argument))
		}
	}
	owner := generation.OwnerID(command.OwnerID)
	source, asset, rejection, err := executor.resolveProcessingSource(ctx, command, arguments)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if rejection != nil {
		return executor.reject(command, rejection.message)
	}
	mentions := executor.serviceMentions(ctx, command, capability)
	chosen, allFormats, found, err := executor.deliveryConversion(ctx, owner, capability, source, asset, processing, arguments, mentions.named)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	if !found && choice != nil {
		// #321: the person's choice first, otherwise a choice card, and the
		// default only when the person accepted it (or there is one option).
		requested := generation.OfferingID(rawString(arguments[choice.OfferingArgument]))
		useDefault := rawBool(arguments[choice.DefaultArgument])
		candidates := executor.compatibleOfferings(capability, source, asset, processing)
		if service := processing.Messages.Service; service != nil {
			// #321 G: a service the person (or this call) asked for that cannot
			// take the model is refused with its reason; nothing else runs in
			// its place without the person's answer.
			if requested != "" {
				if _, exists := executor.availableOffering(capability, requested); !exists {
					return executor.refuseRequestedService(command, choice, capability, requested)
				}
			}
			wanted := executor.wantedServices(capability, requested, mentions)
			accepted := slices.DeleteFunc(slices.Clone(candidates), func(candidate selectedOffering) bool {
				return !slices.ContainsFunc(wanted, func(offering generation.Offering) bool { return offering.ID == candidate.offering.ID })
			})
			requestedFits := requested == "" || slices.ContainsFunc(accepted, func(candidate selectedOffering) bool { return candidate.offering.ID == requested })
			switch {
			case len(wanted) > 0 && (len(accepted) == 0 || !requestedFits):
				return executor.serviceQuestion(ctx, command, capability, source, asset, wanted, false, nil, candidates)
			case requested == "" && len(mentions.named) > 0:
				// The services the person named are the only candidates.
				candidates = accepted
			}
		}
		if requested == "" && len(candidates) > 1 && slices.ContainsFunc(candidates, func(c selectedOffering) bool { return bills(c.offering) }) {
			// Ask about the model before asking about the service.
			if question := executor.sourceConfirmation(ctx, command, arguments, source, capability,
				executor.wantedServices(capability, requested, mentions)); question != nil {
				return executor.confirmSource(command, question)
			}
		}
		switch {
		case requested != "":
			for _, candidate := range candidates {
				if candidate.offering.ID == requested {
					chosen, found = candidate, true
				}
			}
			if !found {
				return executor.unknownService(command, executor.fillText(command, choice.UnknownOffering, "{step}", string(capability)), requested)
			}
		case len(candidates) > 1 && !useDefault && executor.choices != nil:
			offerings := make([]generation.Offering, 0, len(candidates))
			for _, candidate := range candidates {
				offerings = append(offerings, candidate.offering)
			}
			return executor.askChoice(ctx, command, capability, -1, offerings,
				func(offering generation.Offering) json.RawMessage {
					return withArguments(command.Call.Arguments, func(values map[string]any) {
						delete(values, choice.DefaultArgument)
						values[choice.OfferingArgument] = string(offering.ID)
					})
				},
				withArguments(command.Call.Arguments, func(values map[string]any) {
					delete(values, choice.OfferingArgument)
					values[choice.DefaultArgument] = true
				}))
		case len(candidates) > 0:
			chosen, found = candidates[0], true
		}
		if found && (requested != "" || useDefault) {
			executor.settleChoice(ctx, command, capability, -1, chosen.offering.ID, useDefault)
		}
	}
	if !found && choice == nil {
		chosen, found = executor.bindableOffering(capability, source, asset, processing)
	}
	if found && !allFormats {
		allFormats = executor.wholeRunProvides(chosen.offering.ID, processing, arguments)
	}
	if !found {
		available, expired := false, false
		for _, offering := range executor.gateway.Offerings(capability) {
			if offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
				available = true
				_, bindErr := generation.BindModelSource(offering, source, asset, processing.SourceVariantPreference)
				expired = expired || errors.Is(bindErr, generation.ErrSourceExpired)
			}
		}
		if available {
			if text := executor.noServiceText(ctx, command, capability, source, asset); text != "" {
				return executor.reject(command, text)
			}
			return executor.reject(command, sourceIncompatibleText(messages, capability, expired, executor.localizeText(command)))
		}
		return executor.reject(command, executor.fillText(command, messages.NoOffering, "{step}", string(capability)))
	}
	if bills(chosen.offering) {
		if processing.Messages.Service != nil && mentions.any() && !mentions.allows(chosen.offering.ID) {
			// #321 G: the person named another service, or declined this one.
			wanted := executor.wantedServices(capability, "", mentions)
			compatible := executor.compatibleOfferings(capability, source, asset, processing)
			return executor.serviceQuestion(ctx, command, capability, source, asset, wanted, mentions.declined[chosen.offering.ID],
				&chosen.offering, compatible)
		}
		var wanted []generation.Offering
		if processing.Messages.Service != nil {
			requested := generation.OfferingID("")
			if choice != nil {
				requested = generation.OfferingID(rawString(arguments[choice.OfferingArgument]))
			}
			wanted = executor.wantedServices(capability, requested, mentions)
		}
		if question := executor.sourceConfirmation(ctx, command, arguments, source, capability, wanted); question != nil {
			return executor.confirmSource(command, question)
		}
	}
	input := map[string]any{chosen.binding.Parameter: chosen.binding.Value}
	declared := make(map[string]generation.Parameter, len(chosen.offering.Parameters))
	for _, parameter := range chosen.offering.Parameters {
		declared[parameter.Name] = parameter
	}
	if raw, present := arguments[processing.PromptArgument]; present && processing.PromptArgument != "" {
		prompt := rawString(raw)
		parameter, ok := semanticParameter(chosen.offering, generation.SemanticPrompt)
		if !ok {
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", processing.PromptArgument))
		}
		if runes := []rune(prompt); parameter.MaxLength > 0 && len(runes) > parameter.MaxLength {
			prompt = string(runes[:parameter.MaxLength])
		}
		input[parameter.Name] = prompt
	}
	// A style reference must be asked for (by index or by the UI selection):
	// an image that merely sits in the current turn is not one, because it
	// would silently turn a text-prompt step into a conflicting two-input one.
	_, referenceAsked := arguments[processing.ReferenceArgument]
	_, handlesAsked := arguments[executor.chat.Context.ReferenceHandlesArgument]
	selectedInUI := len(command.Options.ReferenceImageIndices) > 0
	if processing.StepArgument != "" && !referenceAsked && !handlesAsked {
		// A multi-step tool (process_model) takes a picture only selected in
		// the UI just for a step whose service declares a reference image
		// (#187 D); a remesh is not refused because a picture is selected.
		if _, declares := semanticParameter(chosen.offering, generation.SemanticReferenceImage); !declares {
			selectedInUI = false
		}
	}
	// A service that requires its reference image (Hyper3D texture, Bang of a
	// local model) takes the one image of this turn or of the selection even
	// when the call did not name it; with none or several it asks the person
	// instead of freezing a step the kernel would refuse (#92).
	referenceParameter, declaresReference := semanticParameter(chosen.offering, generation.SemanticReferenceImage)
	referenceRequired := declaresReference && referenceParameter.Required && processing.ReferenceArgument != ""
	if processing.ReferenceArgument != "" && (referenceAsked || handlesAsked || selectedInUI || referenceRequired) {
		urls, err := executor.selectReferencesBy(command, arguments, processing.ReferenceArgument)
		if err != nil {
			return executor.reject(command, err.Error())
		}
		if referenceRequired && (len(urls) == 0 || (len(urls) > 1 && referenceParameter.Type == "asset")) {
			return executor.reject(command, messages.ReferenceRequired)
		}
		if len(urls) > 0 {
			parameter, ok := semanticParameter(chosen.offering, generation.SemanticReferenceImage)
			if !ok || (parameter.Type == "asset" && len(urls) != 1) {
				return executor.reject(command, messages.ReferenceUnsupported)
			}
			assets, err := executor.references.Resolve(ctx, owner, urls)
			if result, refused := executor.inputRefusal(command, err); refused {
				return result, nil
			}
			if err != nil || len(assets) == 0 {
				return executor.reject(command, messages.ReferenceUnsupported)
			}
			if parameter.Type == "asset" {
				input[parameter.Name] = assets[0]
			} else {
				input[parameter.Name] = assets
			}
		}
	}
	for _, argument := range names {
		parameterName, mapped := processing.ParameterArguments[argument]
		if !mapped || (allFormats && argument == processing.DeliveryFormatArgument) {
			// A whole-run conversion route already delivers the requested
			// format among all it provides (#330).
			continue
		}
		parameter, ok := declared[parameterName]
		if !ok {
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", argument))
		}
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", argument))
		}
		// Consent to extra credits comes from the approval of the frozen step
		// (prepareAgentOperation); no tool argument may set it, whatever the
		// Schema maps.
		if parameterName == chosen.offering.ExtraConsentParameter() {
			return executor.reject(command, executor.fillText(command, messages.ArgumentUnsupported, "{argument}", argument))
		}
		var value any
		if err := json.Unmarshal(arguments[argument], &value); err != nil {
			return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", argument))
		}
		// A converter's fixed target must not replace an explicitly requested
		// format. Keep fixed quality policy elsewhere, and do not select a
		// different (potentially paid) route as a side effect of this check.
		if cost := chosen.offering.Cost; cost != nil && parameterName == cost.FormatParameter && parameter.Fixed {
			var fixedFormat string
			requestedFormat, isString := value.(string)
			if json.Unmarshal(parameter.Default, &fixedFormat) != nil || !isString || requestedFormat != fixedFormat {
				return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}",
					executor.formatRefusal(command, chosen.offering, fixedFormat)))
			}
		}
		input[parameterName] = value
	}
	if gaps := executor.inputGaps(command, chosen.offering, input); len(gaps) > 0 {
		return executor.reject(command, executor.fillText(command, messages.InvalidInput, "{detail}", strings.Join(gaps, "; ")))
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	result, err := executor.prepareAgentOperation(ctx, command, capability, chosen.offering, encoded, "",
		command.IdempotencyKey(), referenceUse{}, referenceSelection{}, nil, nil, observe)
	return executor.processingOutcome(command, result, err, executor.inputShape(command, input))
}

// processingOutcome reports a step the kernel or the Provider refused before
// anything was submitted with its public reason, and logs it (#321 A). A
// Provider that is only temporarily unable (no room for the upload copy, a
// stored file it cannot read) says so, so the Agent neither blames the model
// nor invents a cause. A diagnostic-only kernel Detail is replaced by the
// public invalid-parameters copy.
func (executor *Executor) processingOutcome(command chat.ToolCommand, result chat.ToolResult, err error, sent string) (chat.ToolResult, error) {
	messages := command.Tool.Processing.Messages
	var domainError *generation.Error
	if err != nil && result.NoSubmission && errors.As(err, &domainError) {
		if message, ok := executor.modelInputDetail(command, err); ok {
			return executor.rejectBecause(command, message, err)
		}
		if softProcessingRejection(domainError.Code) {
			// The sent field names and types (never values) let the Agent
			// compare them with the Offering's declared inputs (#92).
			detail := executor.refusalDetail(command, err, domainError.Detail)
			if sent != "" {
				detail += " (" + sent + ")"
			}
			return executor.rejectBecause(command, executor.fillText(command, messages.InvalidInput, "{detail}", detail), err)
		}
		if domainError.Code == generation.ErrUnavailable {
			// A temporary server condition is still Schema copy when the
			// Provider supplied a refusal: say it in the turn's language. The
			// Provider diagnostic remains the fallback, never a generic text.
			if reason := generation.UserFacingError(err); reason != nil {
				return executor.rejectBecause(command, reason.LocalizedDetail(executor.localizeText(command)), err)
			}
			if reason := providerSafeDetail(err); reason != "" {
				return executor.rejectBecause(command, executor.localized(command, reason), err)
			}
		}
	}
	if err == nil && result.NoSubmission && result.Error != "" && !result.Suspend {
		executor.logger.Warn("agent tool call refused", "tool", command.Tool.Name, "call_id", command.Call.ID,
			"owner_id", command.OwnerID, "session_id", command.SessionID, "reason", result.Error)
	}
	return result, err
}

func softProcessingRejection(code generation.ErrorCode) bool {
	return code == generation.ErrInvalidInput || code == generation.ErrInvalidCapability ||
		code == generation.ErrUnknownOffering || code == generation.ErrNoOffering
}

func semanticParameter(offering generation.Offering, semantic generation.ParameterSemantic) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == semantic {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

// resolveProcessingSource finds the owner's succeeded task of this very
// session (and, when asked, one of its saved model files). A task of another
// owner or session is reported exactly like a missing one.
func (executor *Executor) resolveProcessingSource(
	ctx context.Context, command chat.ToolCommand, arguments map[string]json.RawMessage,
) (generation.Task, generation.AssetID, *processingRejection, error) {
	processing := command.Tool.Processing
	messages := processing.Messages
	taskID := rawString(arguments[processing.SourceTaskArgument])
	assetID := generation.AssetID("")
	if processing.SourceAssetArgument != "" {
		assetID = generation.AssetID(rawString(arguments[processing.SourceAssetArgument]))
	}
	notFound := &processingRejection{message: messages.SourceNotFound}
	policy := executor.handlePolicy()
	// A chat handle names a model, not one of its files: the file it recorded
	// is whichever the result listed first (a 3MF package, say). The source
	// file is then chosen like the stage and the 加工 panel choose it (#321 B).
	handleNamesModel := false
	if policy.Enabled() {
		// A model handle (or the selection alias) stands for the Task of a
		// model the server recorded for this session. The Task is still
		// checked below like one named by ID.
		if _, _, isHandle := policy.Parse(taskID); isHandle || policy.IsSelectionAlias(taskID) {
			handleNamesModel = assetID == "" && !policy.IsSelectionAlias(taskID)
			resolved, err := policy.ResolveHandles(command.History, command.Options.Selection,
				[]string{taskID}, chat.AttachmentKindModel)
			if err != nil || len(resolved) == 0 {
				token := taskID
				reason := "unknown"
				var unknown *chat.UnknownHandleError
				if errors.As(err, &unknown) {
					reason = unknown.Reason
				}
				return generation.Task{}, "", &processingRejection{
					message: executor.handleFailure(command, &chat.UnknownHandleError{Handle: token, Reason: reason}).Error(),
				}, nil
			}
			taskID = resolved[0].Attachment.TaskID
			if assetID == "" {
				assetID = generation.AssetID(resolved[0].Attachment.AssetID)
			}
		}
		if taskID == "" && assetID == "" {
			// "This model": nothing was named, so the selected model is the source.
			selectedTask, selectedAsset := selectedModelTask(command)
			taskID, assetID = selectedTask, generation.AssetID(selectedAsset)
		}
	}
	if taskID == "" && assetID == "" {
		return generation.Task{}, "", &processingRejection{message: messages.SourceRequired}, nil
	}
	var task generation.Task
	if taskID == "" {
		tasks, err := executor.sessionModelTasks(ctx, command.OwnerID, command.SessionID,
			executor.chat.Agent.MaxListedModels)
		if err != nil {
			return generation.Task{}, "", nil, err
		}
		for _, candidate := range tasks {
			for _, asset := range candidate.Assets {
				if asset.ID == assetID {
					task = candidate
				}
			}
		}
		if task.ID == "" {
			return generation.Task{}, "", notFound, nil
		}
	} else {
		var err error
		task, err = executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), generation.TaskID(taskID))
		if generation.IsCode(err, generation.ErrNotFound) || generation.IsCode(err, generation.ErrForbidden) {
			return generation.Task{}, "", notFound, nil
		}
		if err != nil {
			return generation.Task{}, "", nil, err
		}
		if string(task.OwnerID) != command.OwnerID || task.SessionID != command.SessionID || task.ID != generation.TaskID(taskID) {
			return generation.Task{}, "", notFound, nil
		}
	}
	if task.State != generation.TaskSucceeded || !hasSavedModel(task) {
		return generation.Task{}, "", &processingRejection{message: messages.SourceNotReady}, nil
	}
	if handleNamesModel || assetID == "" {
		// A model named without one of its files is its main model (#326):
		// the file the stage shows and the 加工 panel binds, never an
		// attachment the result happened to list first.
		assetID = preferredModelAsset(task, processing.SourceVariantPreference)
	}
	if assetID != "" {
		owned := false
		for _, asset := range task.Assets {
			owned = owned || (asset.ID == assetID && asset.Role == modelRole && asset.StorageKey != "")
		}
		if !owned {
			return generation.Task{}, "", notFound, nil
		}
	}
	return task, assetID, nil, nil
}

// preferredModelAsset is the main model file of a task
// (generation.PrimaryModelAsset): what a source binds when only the model is
// named, the file the stage shows and the 加工 panel binds.
func preferredModelAsset(task generation.Task, prefer []string) generation.AssetID {
	primary, _ := generation.PrimaryModelAsset(task, prefer)
	return primary.ID
}

// publicModelFormats lists the distinct public formats of the model files a task
// delivered (#326: never the Provider's result field names).
func (executor *Executor) publicModelFormats(task generation.Task) []string {
	formats := []string{}
	for _, asset := range task.Assets {
		if asset.Role != modelRole || asset.StorageKey == "" {
			continue
		}
		if format := executor.publicFormat(asset); format != "" && !slices.Contains(formats, format) {
			formats = append(formats, format)
		}
	}
	return formats
}

// publicFormat names a saved model file by the Schema's public format
// (model_delivery formats); "" when it has none.
func (executor *Executor) publicFormat(asset generation.Asset) string {
	format, _ := executor.modelDelivery.FormatOf(asset.Role, asset.Variant, asset.MediaType)
	return format
}

// sourcePreference is the Schema's saved-model variant preference of the
// processing tools (GLB first in the shipped Schema).
func (executor *Executor) sourcePreference() []string {
	for _, tool := range executor.chat.Tools {
		if tool.Processing != nil && len(tool.Processing.SourceVariantPreference) > 0 {
			return tool.Processing.SourceVariantPreference
		}
	}
	return nil
}
