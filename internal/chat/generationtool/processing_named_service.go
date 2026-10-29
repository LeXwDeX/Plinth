package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #321 G (dev-test note 99042, user rule 98950): the person named a service
// in their latest message, or answered a choice card. A paid call that would
// run another service, or that names one the person said not to use, is not
// frozen: the tool returns why and what works, and the Agent asks. This holds
// in both approval modes and does not rely on the prompt.
//
// What counts as naming a service:
//   - the person's answer on a choice card of this tool and step, bound to
//     their latest message;
//   - a brand name of an Offering of the step written as its own word in the
//     latest message, case-insensitive: the first word of its provider name or
//     model name, or its provider short label, when it is Latin text of three
//     or more characters ("Bang", "Tripo", "Meshy", "Hyper3D"). Chinese
//     descriptive names ("格式转换") are not brand names and never match.
//   - when the message also contains the text in an Offering's model-name
//     parentheses ("本地模型" in "Bang 部件拆分与重建（本地模型）"), only those
//     Offerings of that brand count;
//   - a name preceded, in the same clause, by one of chat.choice
//     mention_negations ("不要 Tripo") is a service the person declined.

// serviceMentions are the services of one step the person named or declined.
type serviceMentions struct {
	named    map[generation.OfferingID]bool
	declined map[generation.OfferingID]bool
}

func (mentions serviceMentions) any() bool {
	return len(mentions.named) > 0 || len(mentions.declined) > 0
}

// allows reports whether running the Offering stays within what the person said.
func (mentions serviceMentions) allows(id generation.OfferingID) bool {
	if mentions.declined[id] {
		return false
	}
	return len(mentions.named) == 0 || mentions.named[id]
}

// latestUserIndex is the index of the person's latest message in the history.
func (executor *Executor) latestUserIndex(history []chat.Message, before int) int {
	for index := before - 1; index >= 0; index-- {
		if history[index].Role == executor.chat.Roles.User {
			return index
		}
	}
	return -1
}

// mentionsIn reads the services of the step that one message of the person
// names or declines, including a choice-card answer bound to it.
func (executor *Executor) mentionsIn(ctx context.Context, command chat.ToolCommand, capability generation.Capability,
	message chat.Message) serviceMentions {
	mentions := serviceMentions{named: map[generation.OfferingID]bool{}, declined: map[generation.OfferingID]bool{}}
	type matched struct {
		offering generation.Offering
		brands   []string
	}
	var named []matched
	text := asciiLower(message.Content)
	for _, offering := range executor.gateway.Offerings(capability) {
		if !offering.Enabled {
			continue
		}
		var brands []string
		negated := false
		for _, name := range brandNames(offering) {
			for _, at := range wordOffsets(text, asciiLower(name)) {
				if executor.negatedAt(text, at) {
					negated = true
				} else if !slices.Contains(brands, name) {
					brands = append(brands, name)
				}
			}
		}
		switch {
		case len(brands) > 0:
			named = append(named, matched{offering, brands})
		case negated:
			mentions.declined[offering.ID] = true
		}
	}
	for _, candidate := range named {
		// "Bang 本地模型": a qualifier written next to the brand keeps only
		// the Offerings of that brand whose qualifier it is.
		keep := true
		if qualifier := modelQualifier(candidate.offering); qualifier == "" || !strings.Contains(text, asciiLower(qualifier)) {
			for _, other := range named {
				otherQualifier := modelQualifier(other.offering)
				if other.offering.ID != candidate.offering.ID && otherQualifier != "" &&
					strings.Contains(text, asciiLower(otherQualifier)) && sharesBrand(candidate.brands, other.brands) {
					keep = false
				}
			}
		}
		if keep {
			mentions.named[candidate.offering.ID] = true
		}
	}
	if executor.choices != nil && message.ID != "" {
		if cards, err := executor.choices.ListChoices(ctx, command.OwnerID, command.SessionID, executor.chat.Choice.ListLimit); err == nil {
			for _, card := range cards {
				if card.State == chat.ChoiceAnswered && card.AnswerSource == chat.ChoiceAnsweredOnCard &&
					card.AnswerMessageID == message.ID && card.ToolName == command.Tool.Name && card.Capability == string(capability) {
					mentions.named[generation.OfferingID(card.AnswerOptionID)] = true
				}
			}
		}
	}
	return mentions
}

// serviceMentions reads the person's latest message.
func (executor *Executor) serviceMentions(ctx context.Context, command chat.ToolCommand, capability generation.Capability) serviceMentions {
	latest := executor.latestUserIndex(command.History, len(command.History))
	if latest < 0 || command.Tool.Processing == nil || command.Tool.Processing.Messages.Service == nil {
		return serviceMentions{}
	}
	return executor.mentionsIn(ctx, command, capability, command.History[latest])
}

func sharesBrand(left, right []string) bool {
	for _, name := range left {
		if slices.ContainsFunc(right, func(other string) bool { return strings.EqualFold(name, other) }) {
			return true
		}
	}
	return false
}

// brandNames are the words that name an Offering's service (see above).
func brandNames(offering generation.Offering) []string {
	var names []string
	for _, candidate := range []string{firstWord(offering.ProviderName), firstWord(offering.ModelName), offering.ProviderShortLabel} {
		if utf8.RuneCountInString(candidate) < 3 || !hasASCIILetter(candidate) {
			continue
		}
		if !slices.ContainsFunc(names, func(name string) bool { return strings.EqualFold(name, candidate) }) {
			names = append(names, candidate)
		}
	}
	return names
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimFunc(fields[0], func(r rune) bool { return !isASCIIAlnum(r) && r < utf8.RuneSelf })
}

// modelQualifier is the text in the trailing parentheses of a model name.
func modelQualifier(offering generation.Offering) string {
	name := strings.TrimSpace(offering.ModelName)
	for _, pair := range [][2]string{{"（", "）"}, {"(", ")"}} {
		if strings.HasSuffix(name, pair[1]) {
			if open := strings.LastIndex(name, pair[0]); open >= 0 {
				return strings.TrimSpace(name[open+len(pair[0]) : len(name)-len(pair[1])])
			}
		}
	}
	return ""
}

func hasASCIILetter(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// asciiLower lowers ASCII letters only, so byte offsets stay those of text.
func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, text)
}

// wordOffsets finds name in text as its own word: no ASCII letter or digit
// right before or after it.
func wordOffsets(text, name string) []int {
	var offsets []int
	if name == "" {
		return nil
	}
	for start := 0; start < len(text); {
		index := strings.Index(text[start:], name)
		if index < 0 {
			break
		}
		at := start + index
		end := at + len(name)
		before, _ := utf8.DecodeLastRuneInString(text[:at])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (at == 0 || !isASCIIAlnum(before)) && (end == len(text) || !isASCIIAlnum(after)) {
			offsets = append(offsets, at)
		}
		start = end
	}
	return offsets
}

// negationWindow bounds how far before a name a negation still applies.
const negationWindow = 10

// negatedAt reports a negation word in the same clause shortly before the
// name at offset at.
func (executor *Executor) negatedAt(text string, at int) bool {
	clause := text[:at]
	if cut := strings.LastIndexAny(clause, "，。,.;；!?！？\n、"); cut >= 0 {
		_, size := utf8.DecodeRuneInString(clause[cut:])
		clause = clause[cut+size:]
	}
	if runes := []rune(clause); len(runes) > negationWindow {
		clause = string(runes[len(runes)-negationWindow:])
	}
	for _, word := range executor.chat.Choice.MentionNegations {
		word = asciiLower(strings.TrimSpace(word))
		switch {
		case word == "":
		case hasASCIILetter(word) && len(wordOffsets(clause, word)) > 0:
			// A Latin word counts only as a word ("not", never "another").
			return true
		case !hasASCIILetter(word) && strings.Contains(clause, word):
			return true
		}
	}
	return false
}

// availableOffering is the enabled, available Offering of the step with that ID.
func (executor *Executor) availableOffering(capability generation.Capability, id generation.OfferingID) (generation.Offering, bool) {
	for _, offering := range executor.gateway.Offerings(capability) {
		if offering.ID == id && offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// offeringsByID lists the enabled Offerings of the step with these IDs, in
// Schema order.
func (executor *Executor) offeringsByID(capability generation.Capability, ids map[generation.OfferingID]bool) []generation.Offering {
	var found []generation.Offering
	for _, offering := range executor.gateway.Offerings(capability) {
		if offering.Enabled && ids[offering.ID] {
			found = append(found, offering)
		}
	}
	return found
}

// serviceTitle is how the person and the Agent name a service.
func serviceTitle(offering generation.Offering) string {
	return strings.TrimSpace(offering.ProviderName + " · " + offering.ModelName)
}

// localizedServiceTitle translates the provider and model names as their own
// Schema values before joining them. The joined title is not a Schema entry,
// so looking it up as one string would miss both.
func localizedServiceTitle(offering generation.Offering, localize func(string) string) string {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	return strings.TrimSpace(text(offering.ProviderName) + " · " + text(offering.ModelName))
}

// serviceView is a service in a result's own fields: its ID for the next
// call, kept apart from the text the Agent repeats. Provider and model names
// are translated as their own Schema values before the result is encoded.
func serviceView(offering generation.Offering, localize func(string) string) map[string]any {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	view := map[string]any{"offering_id": string(offering.ID), "provider": text(offering.ProviderName), "model": text(offering.ModelName)}
	if quote := defaultQuote(offering, localize); quote != nil {
		view["quote"] = quote
	}
	return view
}

// defaultInput is the Offering's declared defaults (fixed values included):
// the input a step without further arguments is priced on.
func defaultInput(offering generation.Offering) json.RawMessage {
	values := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 && string(parameter.Default) != "null" {
			values[parameter.Name] = parameter.Default
		}
	}
	encoded, _ := json.Marshal(values)
	return encoded
}

// creditQuote is a credit price split into its base and extra parts.
type creditQuote struct {
	Base     int    `json:"base_credits"`
	Extra    int    `json:"extra_credits,omitempty"`
	ExtraFor string `json:"extra_for,omitempty"`
	Total    int    `json:"total_credits"`
}

// quoteOf prices one input of an Offering: credits with the extra part and
// what it pays for, or the native quote. Nil when the step costs nothing.
func quoteOf(offering generation.Offering, input json.RawMessage, native *generation.NativeQuote,
	localize func(string) string) any {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	if native != nil {
		// UnitLabel is Provider-returned data, not Schema copy. It stays as
		// returned even when it happens to equal a Schema text we translate.
		return map[string]string{"total_amount": native.TotalAmount, "unit_label": native.UnitLabel}
	}
	if offering.NativePricing != nil {
		quote, err := offering.DefaultNativeQuote(nil)
		if err != nil || quote == nil {
			return nil
		}
		return map[string]string{"total_amount": quote.TotalAmount, "unit_label": quote.UnitLabel}
	}
	total := offering.EstimateTotalCredits(input)
	if total <= 0 && !offering.CreditsMetered {
		return nil
	}
	detail := offering.ExtraCreditDetail(input)
	quote := creditQuote{Base: total - detail.Credits, Extra: detail.Credits, Total: total}
	if detail.Credits > 0 {
		quote.ExtraFor = text(detail.Label)
		if len(detail.Items) > 0 {
			items := make([]string, len(detail.Items))
			for index, item := range detail.Items {
				items[index] = text(item)
			}
			quote.ExtraFor = strings.TrimSpace(quote.ExtraFor + ": " + strings.Join(items, ", "))
		}
	}
	return quote
}

func defaultQuote(offering generation.Offering, localize func(string) string) any {
	return quoteOf(offering, defaultInput(offering), nil, localize)
}

// costText words a quote with the Schema's texts. localize translates the
// template and Schema values before interpolation; nil keeps the default copy.
func costText(messages *catalog.ChatToolServiceMessages, quote any, localize func(string) string) string {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	switch value := quote.(type) {
	case creditQuote:
		if value.Extra > 0 {
			return fill(text(messages.CostCreditsExtra), "{total}", itoa(value.Total), "{base}", itoa(value.Base),
				"{extra}", itoa(value.Extra), "{extra_for}", value.ExtraFor)
		}
		return fill(text(messages.CostCredits), "{total}", itoa(value.Total))
	case map[string]string:
		return fill(text(messages.CostNative), "{amount}", value["total_amount"], "{unit}", value["unit_label"])
	}
	return fill(text(messages.CostCredits), "{total}", "0")
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// sourceRefusal says why an Offering does not take the source, the way the
// 加工 panel says it (src/lib/workspace/processing-source.ts sourceRefusals):
// a file type it does not take, over its size limit, or only a provider's
// own results of some steps.
//
// Sizes reach the Agent as text in the page's unit (schema.SizeText, #327),
// never as bytes it would have to convert.
type sourceRefusal struct {
	Kind         string   `json:"kind"`
	SizeBytes    int64    `json:"-"`
	LimitBytes   int64    `json:"-"`
	Size         string   `json:"size,omitempty"`
	Limit        string   `json:"limit,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func (executor *Executor) refusalOf(offering generation.Offering, task generation.Task, asset generation.AssetID,
	prefer []string) sourceRefusal {
	if !executor.gateway.OfferingAvailable(offering.ID) {
		return sourceRefusal{Kind: "unavailable"}
	}
	if _, err := generation.BindModelSource(offering, task, asset, prefer); errors.Is(err, generation.ErrSourceExpired) {
		return sourceRefusal{Kind: "expired"}
	}
	file := sourceFile(task, asset, prefer)
	var assetParameter, taskParameter *generation.Parameter
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		role, isSource := schema.SourceParameterRole(*parameter)
		switch {
		case !isSource:
		case role == schema.SemanticSourceAsset && parameter.Type == "asset" && assetParameter == nil &&
			(parameter.UploadedModel == nil || !parameter.UploadedModel.NativeOnly):
			assetParameter = parameter
		case role == schema.SemanticSourceTask && taskParameter == nil:
			taskParameter = parameter
		}
	}
	switch {
	case assetParameter != nil && len(assetParameter.MediaTypes) > 0 && !slices.Contains(assetParameter.MediaTypes, file.MediaType):
		return sourceRefusal{Kind: "media"}
	case assetParameter != nil && assetParameter.MaxBytes > 0 && file.Size > assetParameter.MaxBytes:
		return sourceRefusal{Kind: "too_large", SizeBytes: file.Size, LimitBytes: assetParameter.MaxBytes,
			Size: schema.SizeText(file.Size, true), Limit: schema.SizeText(assetParameter.MaxBytes, false)}
	}
	refusal := sourceRefusal{Kind: "task", Provider: offering.ProviderName}
	if taskParameter != nil {
		for _, capability := range taskParameter.TaskCapabilities {
			refusal.Capabilities = append(refusal.Capabilities, string(capability))
		}
	}
	return refusal
}

// sourceFile is the saved model file a step binds: the one named, else the
// preferred variant (the file the stage shows and the panel binds).
func sourceFile(task generation.Task, asset generation.AssetID, prefer []string) generation.Asset {
	if asset == "" {
		asset = preferredModelAsset(task, prefer)
	}
	for _, candidate := range task.Assets {
		if candidate.ID == asset {
			return candidate
		}
	}
	return generation.Asset{}
}

func reasonText(messages *catalog.ChatToolServiceMessages, offering generation.Offering, refusal sourceRefusal, capabilitySeparator string, localize func(string) string) string {
	service := localizedServiceTitle(offering, localize)
	provider := refusal.Provider
	if localize != nil {
		provider = localize(provider)
	}
	template := messages.Task
	switch refusal.Kind {
	case "unavailable":
		template = messages.Unavailable
	case "expired":
		template = messages.Expired
	case "media":
		template = messages.Media
	case "too_large":
		if localize != nil {
			return fill(localize(messages.TooLarge), "{service}", localize(service),
				"{limit}", refusal.Limit, "{size}", refusal.Size)
		}
		return fill(messages.TooLarge, "{service}", service, "{limit}", refusal.Limit, "{size}", refusal.Size)
	}
	if localize != nil {
		template, service = localize(template), localize(service)
	}
	return fill(template, "{service}", service, "{provider}", provider,
		"{capabilities}", strings.Join(refusal.Capabilities, capabilitySeparator))
}

// localizedSourceRefusal copies the refusal's structured view and translates
// only its Schema provider name. Kind, sizes and capability IDs stay unchanged.
func localizedSourceRefusal(refusal sourceRefusal, localize func(string) string) sourceRefusal {
	if localize != nil {
		refusal.Provider = localize(refusal.Provider)
	}
	return refusal
}

// handleOf is how the conversation names a model: its latest handle in the
// history or the selection, else its task ID.
func handleOf(command chat.ToolCommand, task generation.TaskID) string {
	handle := ""
	for _, message := range command.History {
		for _, attachment := range message.Attachments {
			if attachment.Kind == chat.AttachmentKindModel && attachment.TaskID == string(task) && attachment.Handle != "" {
				handle = attachment.Handle
			}
		}
	}
	for _, attachment := range command.Options.Selection {
		if attachment.Kind == chat.AttachmentKindModel && attachment.TaskID == string(task) && attachment.Handle != "" {
			handle = attachment.Handle
		}
	}
	if handle == "" {
		return string(task)
	}
	return handle
}

// sourceView describes a model for the Agent: what made it (step, provider,
// model) and the size of the file a step binds, so a reply can say what the
// model is instead of guessing (#321 I).
func sourceView(command chat.ToolCommand, modelDelivery catalog.ModelDeliverySchema, task generation.Task, asset generation.AssetID, prefer []string,
	labels map[generation.TaskID]string, localize func(string) string) map[string]any {
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	view := map[string]any{"handle": handleOf(command, task.ID), "task_id": string(task.ID), "capability": string(task.Capability),
		"provider": text(task.Offering.ProviderName), "model": text(task.Offering.ModelName)}
	// The version label the page shows (#326 L): the Agent names it so.
	if label := labels[task.ID]; label != "" {
		view["version"] = label
	}
	if file := sourceFile(task, asset, prefer); file.Size > 0 {
		// The size as the page writes it, for the Agent to quote (#327).
		view["size"] = schema.SizeText(file.Size, true)
		// The public format, never the Provider's result field (#326).
		if format, ok := modelDelivery.FormatOf(file.Role, file.Variant, file.MediaType); ok {
			view["format"] = format
		}
	}
	return view
}

// handleTokenPattern finds handle-like tokens (@model3, @img2) in text.
var handleTokenPattern = regexp.MustCompile(`[@#][A-Za-z]+[0-9]+`)

// lineageDepth bounds how far up the models a model was made from are read.
const lineageDepth = 4

// parentModel is the model of this session a processing result was made
// from, read from its frozen input; false when it has none here.
func (executor *Executor) parentModel(ctx context.Context, command chat.ToolCommand, task generation.Task) (generation.Task, bool) {
	var input map[string]json.RawMessage
	if json.Unmarshal(task.Input, &input) != nil {
		return generation.Task{}, false
	}
	for _, parameter := range task.Offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		value := rawString(input[parameter.Name])
		if !isSource || value == "" {
			continue
		}
		var parent generation.Task
		switch {
		case role == schema.SemanticSourceTask:
			found, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), generation.TaskID(value))
			if err != nil {
				continue
			}
			parent = found
		case role == schema.SemanticSourceAsset:
			tasks, err := executor.sessionModelTasks(ctx, command.OwnerID, command.SessionID, executor.chat.Agent.MaxListedModels)
			if err != nil {
				continue
			}
			for _, candidate := range tasks {
				if slices.ContainsFunc(candidate.Assets, func(asset generation.Asset) bool { return string(asset.ID) == value }) {
					parent = candidate
				}
			}
		}
		if parent.ID != "" && parent.ID != task.ID && string(parent.OwnerID) == command.OwnerID &&
			parent.SessionID == command.SessionID && parent.State == generation.TaskSucceeded && hasSavedModel(parent) {
			return parent, true
		}
	}
	return generation.Task{}, false
}

// proposedSource is the nearest model the source was made from that one of
// the services accepts, with the services that accept it.
func (executor *Executor) proposedSource(ctx context.Context, command chat.ToolCommand, source generation.Task,
	services []generation.Offering) (generation.Task, []generation.Offering, bool) {
	prefer := command.Tool.Processing.SourceVariantPreference
	current := source
	for depth := 0; depth < lineageDepth; depth++ {
		parent, ok := executor.parentModel(ctx, command, current)
		if !ok {
			break
		}
		var accepting []generation.Offering
		for _, offering := range services {
			if !executor.gateway.OfferingAvailable(offering.ID) {
				continue
			}
			if _, err := generation.BindModelSource(offering, parent, preferredModelAsset(parent, prefer), prefer); err == nil {
				accepting = append(accepting, offering)
			}
		}
		if len(accepting) > 0 {
			return parent, accepting, true
		}
		current = parent
	}
	return generation.Task{}, nil, false
}

// wantedServices are the services a call is about: the one it requests and
// those the person named, in Schema order.
func (executor *Executor) wantedServices(capability generation.Capability, requested generation.OfferingID,
	mentions serviceMentions) []generation.Offering {
	ids := map[generation.OfferingID]bool{}
	for id := range mentions.named {
		ids[id] = true
	}
	if requested != "" {
		ids[requested] = true
	}
	wanted := executor.offeringsByID(capability, ids)
	// The requested one first: it is the one this call asked for.
	slices.SortStableFunc(wanted, func(left, right generation.Offering) int {
		switch {
		case left.ID == requested && right.ID != requested:
			return -1
		case right.ID == requested && left.ID != requested:
			return 1
		}
		return 0
	})
	return wanted
}

// serviceQuestion builds the result of a call that would not run a service
// the person wants: refused (none of them takes the model; wouldRun is nil)
// or a confirmation (the call would run wouldRun instead). Nothing was
// frozen; the IDs travel in their own fields.
func (executor *Executor) serviceQuestion(ctx context.Context, command chat.ToolCommand, capability generation.Capability,
	source generation.Task, asset generation.AssetID, wanted []generation.Offering, declined bool,
	wouldRun *generation.Offering, compatible []selectedOffering) (chat.ToolResult, error) {
	processing := command.Tool.Processing
	messages := processing.Messages.Service
	prefer := processing.SourceVariantPreference
	sourceHandle := handleOf(command, source.ID)
	labels := executor.versionLabels(ctx, command.OwnerID, command.SessionID)
	localize := executor.localizeText(command)
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	separator, reasonSeparator := text(messages.OptionSeparator), text(messages.ReasonSeparator)

	var titles, reasons []string
	var requested []map[string]any
	var refusing []generation.Offering
	for _, offering := range wanted {
		view := serviceView(offering, localize)
		titles = append(titles, localizedServiceTitle(offering, localize))
		if !slices.ContainsFunc(compatible, func(candidate selectedOffering) bool { return candidate.offering.ID == offering.ID }) {
			refusal := executor.refusalOf(offering, source, asset, prefer)
			view["refusal"] = localizedSourceRefusal(refusal, localize)
			reasons = append(reasons, reasonText(messages, offering, refusal, ", ", localize))
			refusing = append(refusing, offering)
		}
		requested = append(requested, view)
	}

	var options []string
	content := map[string]any{
		command.Tool.ResultFields.Success: false,
		"service_confirmation_required":   true,
		"step":                            string(capability),
		"source":                          sourceView(command, executor.modelDelivery, source, asset, prefer, labels, localize),
		"requested_services":              requested,
	}
	if len(refusing) > 0 {
		if parent, accepting, ok := executor.proposedSource(ctx, command, source, refusing); ok {
			handle := handleOf(command, parent.ID)
			for _, offering := range accepting {
				options = append(options, executor.fillText(command, messages.OptionSource, "{service}", localizedServiceTitle(offering, localize), "{handle}", handle,
					"{selected}", sourceHandle, "{cost}", costText(messages, defaultQuote(offering, localize), localize)))
			}
			proposal := sourceView(command, executor.modelDelivery, parent, "", prefer, labels, localize)
			var by []map[string]any
			for _, offering := range accepting {
				by = append(by, serviceView(offering, localize))
			}
			proposal["accepted_by"] = by
			content["proposed_source"] = proposal
		}
	}
	var workable []map[string]any
	for _, candidate := range compatible {
		if wouldRun == nil || candidate.offering.ID != wouldRun.ID {
			options = append(options, executor.fillText(command, messages.OptionService, "{service}", localizedServiceTitle(candidate.offering, localize),
				"{source}", sourceHandle, "{cost}", costText(messages, defaultQuote(candidate.offering, localize), localize)))
		}
		workable = append(workable, serviceView(candidate.offering, localize))
	}
	content["workable_services"] = workable
	optionText := ""
	if len(options) > 0 {
		optionText = executor.fillText(command, messages.Options, "{options}", strings.Join(options, separator))
	}
	var message string
	switch {
	case wouldRun == nil:
		message = executor.fillText(command, messages.Refused, "{requested}", strings.Join(titles, separator), "{step}", string(capability),
			"{source}", sourceHandle, "{reasons}", strings.Join(reasons, reasonSeparator), "{options}", optionText)
	default:
		content["would_run"] = serviceView(*wouldRun, localize)
		reasonSentence := ""
		if len(reasons) > 0 {
			reasonSentence = executor.fillText(command, messages.ReasonsSentence, "{reasons}", strings.Join(reasons, reasonSeparator))
		}
		template := messages.Confirmation
		if declined && len(titles) == 0 {
			template = messages.Declined
		}
		message = executor.fillText(command, template, "{requested}", strings.Join(titles, separator), "{service}", localizedServiceTitle(*wouldRun, localize),
			"{step}", string(capability), "{source}", sourceHandle, "{cost}", costText(messages, defaultQuote(*wouldRun, localize), localize),
			"{reasons}", reasonSentence, "{options}", optionText)
	}
	content[command.Tool.ResultFields.Error] = message
	executor.logger.Info("agent tool call waits for the person to confirm the service", "tool", command.Tool.Name,
		"call_id", command.Call.ID, "owner_id", command.OwnerID, "session_id", command.SessionID, "step", string(capability))
	encoded, _ := json.Marshal(content)
	return chat.ToolResult{Error: message, AgentContent: string(encoded), NoSubmission: true}, nil
}

// unknownService refuses an Offering ID that is no available service of the
// step without echoing it into the text.
func (executor *Executor) unknownService(command chat.ToolCommand, text string, requested generation.OfferingID) (chat.ToolResult, error) {
	return executor.refuseService(command, text, requested, "unknown offering", nil)
}

// refuseRequestedService refuses an Offering ID the call named that is no
// available service of the step. A service the Schema registers and enables
// but whose runtime configuration is missing is told apart from an ID that
// is not registered: the Agent then says the service is currently
// unavailable instead of treating it as a wrong name (#128).
func (executor *Executor) refuseRequestedService(command chat.ToolCommand, choice *catalog.ChatToolOfferingChoiceSchema,
	capability generation.Capability, requested generation.OfferingID) (chat.ToolResult, error) {
	if executor.offeringUnavailable(capability, requested) {
		text := executor.fillText(command, choice.UnavailableOffering, "{step}", string(capability))
		return executor.refuseService(command, text, requested, "offering unavailable", map[string]any{"offering_unavailable": true})
	}
	return executor.unknownService(command, executor.fillText(command, choice.UnknownOffering, "{step}", string(capability)), requested)
}

// offeringUnavailable reports an Offering that is registered and enabled for
// the step while the runtime configuration it needs is not available.
func (executor *Executor) offeringUnavailable(capability generation.Capability, id generation.OfferingID) bool {
	for _, offering := range executor.gateway.Offerings(capability) {
		if offering.ID == id && offering.Enabled && !executor.gateway.OfferingAvailable(offering.ID) {
			return true
		}
	}
	return false
}

func (executor *Executor) refuseService(command chat.ToolCommand, text string, requested generation.OfferingID, reason string, extra map[string]any) (chat.ToolResult, error) {
	executor.logger.Warn("agent tool call refused", "tool", command.Tool.Name, "call_id", command.Call.ID,
		"owner_id", command.OwnerID, "session_id", command.SessionID, "reason", reason, "offering_id", string(requested))
	text = executor.localized(command, text)
	fields := map[string]any{
		command.Tool.ResultFields.Success: false,
		command.Tool.ResultFields.Error:   text,
		"requested_offering_id":           string(requested),
	}
	for key, value := range extra {
		fields[key] = value
	}
	content, _ := json.Marshal(fields)
	return chat.ToolResult{Error: text, AgentContent: string(content), NoSubmission: true}, nil
}

// publicText replaces the Offering IDs of the tool's steps in a refusal text
// with the services' public names: the Agent repeats these texts (#321).
func (executor *Executor) publicText(command chat.ToolCommand, text string) string {
	tool := command.Tool
	if tool.Processing == nil || !strings.Contains(text, "/") {
		return text
	}
	capabilities := []generation.Capability{tool.Capability}
	for _, capability := range tool.Processing.Steps {
		capabilities = append(capabilities, capability)
	}
	for _, capability := range capabilities {
		if capability == "" {
			continue
		}
		for _, offering := range executor.gateway.Offerings(capability) {
			name := localizedServiceTitle(offering, executor.localizeText(command))
			if offering.Revision != "" {
				text = strings.ReplaceAll(text, string(offering.ID)+"@"+offering.Revision, name)
			}
			text = strings.ReplaceAll(text, string(offering.ID), name)
		}
	}
	return text
}

// proposalAnswered reports that the person's latest message answers the
// question a call of this tool in the Agent's previous reply asked, and that
// the question proposed exactly this source (#321 H): that call submitted
// nothing, refused the model it was given for the services wanted then, and
// proposed the nearest model it was made from that one of them takes. An
// answer that names another model is not a yes to this one.
func (executor *Executor) proposalAnswered(ctx context.Context, command chat.ToolCommand, latest int,
	source generation.Task, capability generation.Capability, handles map[string]string) bool {
	processing := command.Tool.Processing
	if processing.Messages.Service == nil {
		return false
	}
	policy := executor.handlePolicy()
	for _, token := range handleTokenPattern.FindAllString(command.History[latest].Content, -1) {
		kind, _, isHandle := policy.Parse(token)
		if isHandle && kind == chat.AttachmentKindModel && !strings.EqualFold(handleOf(command, source.ID), token) &&
			handles[token] != string(source.ID) {
			return false
		}
	}
	previousUser := executor.latestUserIndex(command.History, latest)
	if previousUser < 0 {
		return false
	}
	asked := command.History[previousUser]
	for index := latest - 1; index > previousUser; index-- {
		for _, call := range command.History[index].ToolCalls {
			if call.TaskID != "" || call.Name != command.Tool.Name {
				continue
			}
			var earlier map[string]json.RawMessage
			if json.Unmarshal(call.Arguments, &earlier) != nil {
				continue
			}
			step, requested := capabilityOfCall(command.Tool, earlier)
			if step != capability {
				continue
			}
			token := rawString(earlier[processing.SourceTaskArgument])
			earlierSource := token
			switch {
			case token == "" || policy.IsSelectionAlias(token):
				earlierSource, _ = selectedModelTask(command)
			case handles[token] != "":
				earlierSource = handles[token]
			}
			if earlierSource == "" || earlierSource == string(source.ID) {
				continue
			}
			task, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), generation.TaskID(earlierSource))
			if err != nil || task.SessionID != command.SessionID {
				continue
			}
			wanted := executor.wantedServices(step, requested, executor.mentionsIn(ctx, command, step, asked))
			var refusing []generation.Offering
			for _, offering := range wanted {
				if _, err := generation.BindModelSource(offering, task, "", processing.SourceVariantPreference); err != nil {
					refusing = append(refusing, offering)
				}
			}
			if len(refusing) == 0 {
				continue
			}
			if parent, _, ok := executor.proposedSource(ctx, command, task, refusing); ok && parent.ID == source.ID {
				return true
			}
		}
	}
	return false
}

// capabilityOfCall is the step a processing call asked for and the service
// it named: the step argument, or a plan's first step.
func capabilityOfCall(tool catalog.ChatToolSchema, arguments map[string]json.RawMessage) (generation.Capability, generation.OfferingID) {
	processing := tool.Processing
	requested := generation.OfferingID("")
	if processing.Choice != nil && processing.Choice.OfferingArgument != "" {
		requested = generation.OfferingID(rawString(arguments[processing.Choice.OfferingArgument]))
	}
	switch {
	case processing.Plan != nil:
		var steps []proposedProcessingStep
		if json.Unmarshal(arguments[processing.Plan.StepsArgument], &steps) != nil || len(steps) == 0 {
			return "", ""
		}
		return steps[0].Capability, steps[0].OfferingID
	case processing.StepArgument != "":
		return processing.Steps[rawString(arguments[processing.StepArgument])], requested
	}
	return tool.Capability, requested
}

// noServiceText is the refusal of a step that no available service takes the
// model for (#326), in the terms the 加工 panel uses
// (src/lib/workspace/processing-source.ts sourceRefusals and
// prerequisiteRefusal): why each service refuses the model's main file, then
// the step whose results those services take (rigging for animation): it can
// run on the model first, or it cannot either and why, with the model this
// one was made from as the workable option. "" when the tool declares no such
// text.
func (executor *Executor) noServiceText(ctx context.Context, command chat.ToolCommand, capability generation.Capability,
	source generation.Task, asset generation.AssetID) string {
	processing := command.Tool.Processing
	if processing == nil || processing.Messages.Service == nil || processing.Messages.Service.NoService == "" {
		return ""
	}
	messages := processing.Messages.Service
	prefer := processing.SourceVariantPreference
	if asset == "" {
		asset = preferredModelAsset(source, prefer)
	}
	sourceHandle := handleOf(command, source.ID)
	localize := executor.localizeText(command)
	text := func(value string) string {
		if localize == nil {
			return value
		}
		return localize(value)
	}
	separator, reasonSeparator := text(messages.OptionSeparator), text(messages.ReasonSeparator)
	available := func(capability generation.Capability) []generation.Offering {
		var offerings []generation.Offering
		for _, offering := range executor.gateway.Offerings(capability) {
			if offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
				offerings = append(offerings, offering)
			}
		}
		return offerings
	}
	steps := available(capability)
	var reasons []string
	var wanted []generation.Capability
	for _, offering := range steps {
		reasons = append(reasons, reasonText(messages, offering, executor.refusalOf(offering, source, asset, prefer), ", ", localize))
		for _, parameter := range offering.Parameters {
			if role, isSource := schema.SourceParameterRole(parameter); !isSource || role != schema.SemanticSourceTask {
				continue
			}
			for _, wantedCapability := range parameter.TaskCapabilities {
				if wantedCapability != capability && !slices.Contains(wanted, wantedCapability) {
					wanted = append(wanted, wantedCapability)
				}
			}
		}
	}
	prerequisite, optionText := "", ""
	var refusedCapability generation.Capability
	var refusedFeeders []generation.Offering
	for _, candidate := range wanted {
		var feeders, runs []generation.Offering
		for _, offering := range available(candidate) {
			if offering.Domain != generation.DomainProcessing {
				continue
			}
			feeds := slices.ContainsFunc(steps, func(step generation.Offering) bool {
				_, err := generation.BindProjectedModelSource(step, offering)
				return err == nil
			})
			if !feeds {
				continue
			}
			feeders = append(feeders, offering)
			if _, err := generation.BindModelSource(offering, source, asset, prefer); err == nil {
				runs = append(runs, offering)
			}
		}
		if len(runs) > 0 {
			titles := make([]string, 0, len(runs))
			for _, offering := range runs {
				titles = append(titles, localizedServiceTitle(offering, localize))
			}
			prerequisite = executor.fillText(command, messages.PrerequisiteRuns, "{prerequisite}", string(candidate), "{source}", sourceHandle,
				"{services}", strings.Join(titles, separator), "{step}", string(capability))
			refusedFeeders = nil
			break
		}
		if len(feeders) > 0 && refusedCapability == "" {
			refusedCapability, refusedFeeders = candidate, feeders
		}
	}
	if prerequisite == "" && len(refusedFeeders) > 0 {
		var prerequisiteReasons []string
		for _, offering := range refusedFeeders {
			prerequisiteReasons = append(prerequisiteReasons,
				reasonText(messages, offering, executor.refusalOf(offering, source, asset, prefer), ", ", localize))
		}
		prerequisite = executor.fillText(command, messages.PrerequisiteRefused, "{prerequisite}", string(refusedCapability), "{source}", sourceHandle,
			"{reasons}", strings.Join(prerequisiteReasons, reasonSeparator))
		if parent, accepting, ok := executor.proposedSource(ctx, command, source, refusedFeeders); ok {
			var options []string
			for _, offering := range accepting {
				options = append(options, executor.fillText(command, messages.OptionPrerequisite, "{prerequisite}", string(refusedCapability),
					"{service}", localizedServiceTitle(offering, localize), "{handle}", handleOf(command, parent.ID), "{selected}", sourceHandle,
					"{cost}", costText(messages, defaultQuote(offering, localize), localize), "{step}", string(capability)))
			}
			optionText = executor.fillText(command, messages.Options, "{options}", strings.Join(options, separator))
		}
	}
	return executor.fillText(command, messages.NoService, "{step}", string(capability), "{source}", sourceHandle,
		"{reasons}", strings.Join(reasons, reasonSeparator), "{prerequisite}", prerequisite, "{options}", optionText)
}
