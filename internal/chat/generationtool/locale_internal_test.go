package generationtool

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// approvalGateway is the smallest prepared gateway awaitingApproval reads:
// one frozen offering and the prepared input it prices.
type approvalGateway struct {
	offering generation.Offering
	prepared generation.PreparedInput
}

func (gateway *approvalGateway) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{gateway.offering}
}
func (*approvalGateway) OfferingAvailable(generation.OfferingID) bool { return true }
func (*approvalGateway) Submit(context.Context, generation.SubmitCommand) (generation.Accepted, error) {
	return generation.Accepted{}, nil
}
func (*approvalGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{}, nil
}
func (*approvalGateway) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, nil
}
func (gateway *approvalGateway) GetPreparedInput(context.Context, generation.OwnerID,
	generation.PreparedInputID) (generation.PreparedInput, error) {
	return gateway.prepared, nil
}
func (*approvalGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	return generation.Accepted{}, nil
}

var cjk = regexp.MustCompile(`[\p{Han}]`)

// #358: a UserFacing refusal never appends a Provider SafeDetail, and an
// internal detail is not translated. Only the turn's Schema copy changes.
func TestInputRefusalKeepsSafeDetailOut(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	tool := application.Chat.Tools[0]
	const detail = "the input was refused"
	refusal := &generation.Error{
		Code: generation.ErrInvalidInput, Operation: "resolve_input_assets",
		Detail: detail, UserFacing: true,
		Cause: &generation.ProviderError{Failure: &generation.Failure{
			Class: generation.FailureInvalidInput, SafeDetail: "SECRET-UPSTREAM-123"},
		},
	}
	executor := Executor{chat: application.Chat, texts: texts}
	result, refused := executor.inputRefusal(chat.ToolCommand{
		Tool:    tool,
		Options: chat.TurnOptions{Locale: "en"},
	}, refusal)
	if !refused || result.Error != detail {
		t.Fatalf("refused=%v error=%q", refused, result.Error)
	}
	if strings.Contains(result.Error, "SECRET-UPSTREAM-123") || strings.Contains(result.AgentContent, "SECRET-UPSTREAM-123") {
		t.Fatalf("SafeDetail reached the Agent: %+v", result)
	}
}

// #358: the source a service will not take (unavailable/disabled, expired,
// too large) is explained in the turn's locale before the step/limit values
// are interpolated.
func TestSourceRefusalReasonsFollowTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	var messages *catalog.ChatToolServiceMessages
	for _, tool := range application.Chat.Tools {
		if tool.Processing != nil && tool.Processing.Messages.Service != nil {
			messages = tool.Processing.Messages.Service
			break
		}
	}
	if messages == nil {
		t.Fatal("the default Schema declares no service refusal messages")
	}
	offering := generation.Offering{ID: "configured/offering", ModelID: "configured-model",
		ProviderName: "Configured", ModelName: "Configured"}
	localize := func(text string) string { return texts.Text("en", text) }
	for _, refusal := range []sourceRefusal{
		{Kind: "unavailable"},
		{Kind: "expired"},
		{Kind: "too_large", Size: "10 MB", Limit: "5 MB"},
	} {
		t.Run(refusal.Kind, func(t *testing.T) {
			got := reasonText(messages, offering, refusal, ", ", localize)
			if got == "" || cjk.MatchString(got) {
				t.Fatalf("%s reason is not English: %q", refusal.Kind, got)
			}
		})
	}
}

// #358: the metered no-auto-retry check and the failure-class lookup stay
// canonical. Only the text written to the turn changes with its locale.
func TestMeteredFailureTextStaysCanonical(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	failure := &generation.Failure{Class: generation.FailureIntegrity}
	canonical := MeteredFailureMessage(application.Chat, true, failure)
	if canonical == "" {
		t.Fatal("the default Schema declares no metered integrity text")
	}
	if FailureMessage(application.Chat, true, failure) != canonical {
		t.Fatal("FailureMessage changed the metered text")
	}
	executor := Executor{chat: application.Chat, texts: texts}
	command := chat.ToolCommand{Options: chat.TurnOptions{Locale: "en"}}
	english := executor.localized(command, canonical)
	if english == canonical {
		t.Fatal("the English turn kept the default metered text")
	}
	if executor.failureMessage(command, true, failure) != english {
		t.Fatal("failureMessage did not use the turn locale")
	}
}

// #358: the whole awaiting_approval result follows the turn locale: status,
// message, reference note and every Schema component of the quote. Numbers,
// task IDs and offering IDs stay unchanged.
func TestAwaitingApprovalResultFollowsTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.ResultFields.ReferenceNote != "" {
			tool = candidate
			break
		}
	}
	if tool.ResultFields.ReferenceNote == "" {
		t.Fatal("no tool declares reference_note")
	}
	providerZH := application.Chat.Presentation.Messages.GenerationFailed
	modelZH := application.Chat.Presentation.Messages.GenerationError
	offering := generation.Offering{
		ID: "configured/offering", Revision: "rev-a", Capability: tool.Capability,
		ProviderName: providerZH, ModelName: modelZH,
		EstimatedCredits: 25,
		ExecutionMessages: generation.ExecutionMessages{
			ReferenceLimit:    application.Runtime.GenerationMessages.ReferenceLimit,
			ReferenceLimitOne: application.Runtime.GenerationMessages.ReferenceLimitOne,
		},
		Parameters: []generation.Parameter{{
			Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1,
		}},
	}
	prepared := generation.PreparedInput{
		ID: "prepared-a", OwnerID: "owner-a", SessionID: "session-a",
		Capability: tool.Capability, OfferingID: offering.ID, OfferingRevision: offering.Revision,
		NormalizedInput: json.RawMessage(`{}`),
	}
	executor := Executor{chat: application.Chat, texts: texts, gateway: &approvalGateway{offering: offering, prepared: prepared}}
	command := chat.ToolCommand{Tool: tool, Options: chat.TurnOptions{Locale: "en"}}
	op := Operation{OwnerID: "owner-a", SessionID: "session-a", PreparedInputID: prepared.ID,
		OfferingID: offering.ID, OfferingRevision: offering.Revision,
		RechargeTaskID: "task-old", ReferenceCount: 1, ReferenceTotal: 3,
		// A retained prefix of the selection: the limit text states the truth.
		ReferenceSource: ReferenceSourceFact{Version: 1, Selected: 3, Resolved: 3, Frozen: 1,
			Prefix: true, Identity: "locale-source"}}

	content := map[string]any{}
	if err := json.Unmarshal([]byte(executor.awaitingApproval(context.Background(), command, op)), &content); err != nil {
		t.Fatal(err)
	}
	status, _ := content["status_text"].(string)
	englishStatus := application.Localization.Bundles["en"].Fields["chat.agent.consent_waiting_status"]
	if status == "" || status == application.Chat.Agent.ConsentWaitingStatus || status != englishStatus {
		t.Fatalf("status_text = %q, want %q", status, englishStatus)
	}
	message, _ := content["message"].(string)
	if !strings.Contains(message, "task-old") || !strings.Contains(message, "nothing was submitted") {
		t.Fatalf("message = %q", message)
	}
	note, _ := content[tool.ResultFields.ReferenceNote].(string)
	wantNote := ReferenceLimitNote(tool, offering, 1, 3, func(text string) string { return texts.Text("en", text) })
	if note == "" || note != wantNote || cjk.MatchString(note) {
		t.Fatalf("reference note = %q, want %q", note, wantNote)
	}
	quote, _ := content["quote"].(map[string]any)
	if quote["total_credits"] != float64(25) {
		t.Fatalf("quote numbers changed: %v", quote)
	}
	if content["provider"] != texts.Text("en", providerZH) || content["model"] != texts.Text("en", modelZH) {
		t.Fatalf("provider/model = %v / %v", content["provider"], content["model"])
	}
}

// #358: extra_for is assembled from Schema components (the extra label and
// each chosen option label). Each component is translated before the join;
// the whole sentence is not looked up as one string.
func TestExtraQuoteForTranslatesComponentsBeforeJoin(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	labelZH := application.Chat.Presentation.Messages.GenerationFailed
	itemZH := application.Chat.Presentation.Messages.GenerationError
	localize := func(text string) string { return texts.Text("en", text) }
	offering := generation.Offering{
		EstimatedCredits: 20,
		Cost:             &schema.CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats", ExtraLabel: labelZH},
		Parameters: []generation.Parameter{{
			Name: "output_formats", Label: "formats",
			Options: []schema.ParameterOption{{Value: "a", Label: itemZH}, {Value: "b", Label: itemZH}},
		}},
	}
	quote, ok := quoteOf(offering, json.RawMessage(`{"output_formats":["a","b"]}`), nil, localize).(creditQuote)
	if !ok {
		t.Fatal("quote is not a credit quote")
	}
	want := localize(labelZH) + ": " + localize(itemZH) + ", " + localize(itemZH)
	if quote.ExtraFor != want || cjk.MatchString(quote.ExtraFor) {
		t.Fatalf("extra_for = %q, want %q", quote.ExtraFor, want)
	}
	if quote.Base != 20 || quote.Extra != 10 || quote.Total != 30 {
		t.Fatalf("quote numbers changed: %+v", quote)
	}
	messages := &catalog.ChatToolServiceMessages{
		CostCreditsExtra: "total {total} base {base} extra {extra} for {extra_for}",
	}
	got := costText(messages, quote, localize)
	wantText := "total 30 base 20 extra 10 for " + want
	if got != wantText {
		t.Fatalf("cost text = %q, want %q", got, wantText)
	}
}

// #358: a refusal template is translated before an Offering ID in it is
// replaced by the service's name. Both halves are Schema values; the ID
// itself is an identifier and disappears from the Agent-facing text.
func TestRefusalTranslatesBeforeReplacingOfferingIDs(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	var messages *catalog.ChatToolServiceMessages
	for _, candidate := range application.Chat.Tools {
		if candidate.Processing != nil && candidate.Processing.Messages.Service != nil &&
			candidate.Processing.Messages.Service.Refused != "" {
			tool, messages = candidate, candidate.Processing.Messages.Service
			break
		}
	}
	if messages == nil {
		t.Fatal("no processing tool declares a service refusal")
	}
	providerZH := application.Chat.Presentation.Messages.GenerationFailed
	modelZH := application.Chat.Presentation.Messages.GenerationError
	offering := generation.Offering{ID: "configured/offering", Revision: "rev-a", Capability: tool.Capability,
		ProviderName: providerZH, ModelName: modelZH}
	executor := Executor{chat: application.Chat, texts: texts, gateway: &approvalGateway{offering: offering}}
	command := chat.ToolCommand{Tool: tool, Options: chat.TurnOptions{Locale: "en"}}
	template := executor.localized(command, messages.Refused)
	message := fill(template, "{requested}", string(offering.ID))
	got := executor.publicText(command, message)
	if strings.Contains(got, string(offering.ID)) || cjk.MatchString(got) {
		t.Fatalf("public text = %q", got)
	}
	if !strings.Contains(got, localizedServiceTitle(offering, executor.localizeText(command))) {
		t.Fatalf("public text = %q, want the English service name", got)
	}
}

// #358: requested_services and workable services are built from serviceView;
// provider, model and the quote's extra components are translated one by one
// while the offering ID stays an identifier.
func TestServiceViewTranslatesComponents(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	providerZH := application.Chat.Presentation.Messages.GenerationFailed
	modelZH := application.Chat.Presentation.Messages.GenerationError
	labelZH := application.Runtime.GenerationMessages.ReferenceLimit
	localize := func(text string) string { return texts.Text("en", text) }
	offering := generation.Offering{
		ID: "configured/offering", ProviderName: providerZH, ModelName: modelZH, EstimatedCredits: 10,
		Cost: &schema.CostDeclaration{ExtraCredits: 5, ExtraLabel: labelZH,
			ExtraWhen: []schema.ParameterCondition{{Parameter: "extra", Operator: "truthy"}}},
		Parameters: []generation.Parameter{{Name: "extra", Default: json.RawMessage("true")}},
	}
	view := serviceView(offering, localize)
	if view["offering_id"] != string(offering.ID) {
		t.Fatalf("offering_id changed: %v", view["offering_id"])
	}
	if view["provider"] != localize(providerZH) || view["model"] != localize(modelZH) {
		t.Fatalf("provider/model = %v / %v", view["provider"], view["model"])
	}
	quote, ok := view["quote"].(creditQuote)
	if !ok {
		t.Fatalf("quote = %T", view["quote"])
	}
	if quote.ExtraFor != localize(labelZH)+": extra" || quote.Total != 15 {
		t.Fatalf("quote = %+v", quote)
	}
}

type capturingChoiceStore struct {
	last chat.Choice
}

func (store *capturingChoiceStore) CreateChoice(_ context.Context, choice chat.Choice) (chat.Choice, error) {
	store.last = choice
	choice.ID = "choice-1"
	return choice, nil
}

func (*capturingChoiceStore) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	return nil, nil
}

func (*capturingChoiceStore) GetChoice(context.Context, string, string, string) (chat.Choice, error) {
	return chat.Choice{}, nil
}

func (*capturingChoiceStore) AnswerChoice(context.Context, string, string, string, chat.ChoiceRecord) (chat.Choice, error) {
	return chat.Choice{}, nil
}

func (*capturingChoiceStore) ReopenChoice(context.Context, string, string, string, string) error {
	return nil
}

// #358: askChoice names each candidate's provider and model in the turn
// locale. The stored card keeps the raw Schema names; IDs, prices and the
// user's arguments are unchanged.
func TestAskChoiceFollowsTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	providerZH := "即梦 AI"
	modelZH := "即梦 Seedream 4.5 (高质量)"
	providerEN := texts.Text("en", providerZH)
	modelEN := texts.Text("en", modelZH)
	if providerEN == providerZH || modelEN == modelZH {
		t.Fatalf("need English offering_texts for the fixture: %q/%q %q/%q", providerZH, providerEN, modelZH, modelEN)
	}
	candidates := []generation.Offering{
		{ID: "jimeng/a", ProviderName: providerZH, ModelName: modelZH, CreditsMetered: true, EstimatedCredits: 10},
		{ID: "jimeng/b", ProviderName: providerZH, ModelName: modelZH, CreditsMetered: true, EstimatedCredits: 20},
	}
	for _, locale := range []string{"en", ""} {
		t.Run("locale="+locale, func(t *testing.T) {
			store := &capturingChoiceStore{}
			executor := Executor{chat: application.Chat, texts: texts, choices: store, clock: time.Now}
			tool := application.Chat.Tools[0]
			tool.Processing = &catalog.ChatToolProcessingSchema{
				Choice: &catalog.ChatToolOfferingChoiceSchema{Content: "choose {step}", DefaultArgument: "use_default"},
			}
			command := chat.ToolCommand{Tool: tool, Options: chat.TurnOptions{Locale: locale}}
			result, err := executor.askChoice(context.Background(), command, tool.Capability, 0, candidates,
				func(generation.Offering) json.RawMessage { return json.RawMessage(`{}`) }, nil)
			if err != nil || !result.NoSubmission {
				t.Fatalf("choice = %+v, %v", result, err)
			}
			var content struct {
				Offerings []struct {
					OfferingID       string `json:"offering_id"`
					Provider         string `json:"provider"`
					Model            string `json:"model"`
					EstimatedCredits *int   `json:"estimated_credits"`
				} `json:"offering_choices"`
			}
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatal(err)
			}
			wantProvider, wantModel := texts.Text(locale, providerZH), texts.Text(locale, modelZH)
			if len(content.Offerings) != 2 {
				t.Fatalf("choices = %+v", content.Offerings)
			}
			for index, view := range content.Offerings {
				if view.Provider != wantProvider || view.Model != wantModel {
					t.Fatalf("choice %d = %q / %q, want %q / %q", index, view.Provider, view.Model, wantProvider, wantModel)
				}
				if view.EstimatedCredits == nil || *view.EstimatedCredits != candidates[index].EstimatedCredits {
					t.Fatalf("choice %d price = %v", index, view.EstimatedCredits)
				}
			}
			if content.Offerings[0].OfferingID != "jimeng/a" {
				t.Fatalf("offering ID changed: %q", content.Offerings[0].OfferingID)
			}
			if store.last.Options[0].ProviderName != providerZH || store.last.Options[0].ModelName != modelZH {
				t.Fatalf("stored card was rewritten: %+v", store.last.Options[0])
			}
		})
	}
}

// #358: source views (source_confirmation, proposed_source, selected) name
// the Schema provider/model in the turn locale. User history and the source
// identity are unchanged; no arbitrary string in the view is translated.
func TestSourceViewTranslatesOnlySchemaNames(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	providerZH, modelZH := "即梦 AI", "即梦 Seedream 4.5 (高质量)"
	localize := func(text string) string { return texts.Text("en", text) }
	task := generation.Task{ID: "task-1", Capability: generation.TextTo3D,
		Offering: generation.Offering{ProviderName: providerZH, ModelName: modelZH}}
	command := chat.ToolCommand{History: []chat.Message{{Content: "private user prompt"}}}
	view := sourceView(command, catalog.ModelDeliverySchema{}, task, "", nil, nil, localize)
	if view["provider"] != localize(providerZH) || view["model"] != localize(modelZH) {
		t.Fatalf("source names = %v / %v", view["provider"], view["model"])
	}
	if view["task_id"] != "task-1" || view["capability"] != string(generation.TextTo3D) {
		t.Fatalf("source identity changed: %v", view)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "private user prompt") {
		t.Fatalf("user prompt reached the source view: %s", encoded)
	}
}

// #358: a task-source refusal names the Schema provider in both the refusal
// text and the structured requested_services view. A CJK provider with an
// explicit Schema translation becomes English; IDs stay unchanged.
func TestSourceRefusalProviderFollowsTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	providerZH := "即梦 AI"
	providerEN := texts.Text("en", providerZH)
	if providerEN == providerZH {
		t.Fatalf("no English mapping for %q", providerZH)
	}
	localize := func(text string) string { return texts.Text("en", text) }
	offering := generation.Offering{ProviderName: providerZH, ModelName: "即梦 Seedream 4.5 (高质量)"}
	refusal := sourceRefusal{Kind: "task", Provider: providerZH, Capabilities: []string{"text-to-3d"}}
	messages := &catalog.ChatToolServiceMessages{Task: "task {service} {provider} {capabilities}"}
	got := reasonText(messages, offering, refusal, ", ", localize)
	if !strings.Contains(got, providerEN) || strings.Contains(got, providerZH) {
		t.Fatalf("reason = %q, want provider %q", got, providerEN)
	}
	structured := localizedSourceRefusal(refusal, localize)
	if structured.Provider != providerEN || structured.Kind != "task" ||
		len(structured.Capabilities) != 1 || structured.Capabilities[0] != "text-to-3d" {
		t.Fatalf("structured refusal = %+v", structured)
	}
	if refusal.Provider != providerZH {
		t.Fatalf("the source refusal was mutated: %+v", refusal)
	}
}

// #358: a native quote's unit label is Provider-returned data. Even when it
// happens to equal a Schema text that has a translation, it stays as
// returned; only Schema unit labels with a proven mapping may change.
func TestNativeUnitLabelStaysRaw(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	rawUnit := "即梦 AI"
	translated := texts.Text("en", rawUnit)
	if translated == rawUnit {
		t.Fatalf("no English mapping for %q", rawUnit)
	}
	localize := func(text string) string { return texts.Text("en", text) }
	native := &generation.NativeQuote{TotalAmount: "10", UnitLabel: rawUnit}
	quote, ok := quoteOf(generation.Offering{}, nil, native, localize).(map[string]string)
	if !ok || quote["unit_label"] != rawUnit {
		t.Fatalf("native unit label = %v, want raw %q", quote, rawUnit)
	}
	messages := &catalog.ChatToolServiceMessages{CostNative: "amount {amount} unit {unit}"}
	got := costText(messages, map[string]string{"total_amount": "10", "unit_label": rawUnit}, localize)
	if !strings.Contains(got, rawUnit) || strings.Contains(got, translated) {
		t.Fatalf("cost text = %q, want raw unit %q", got, rawUnit)
	}
}

// #358: the fixed-format refusal names the service in the turn locale. The
// target format itself is an identifier and stays as requested.
func TestFormatRefusalLocalizesServiceName(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	providerZH, modelZH := "即梦 AI", "即梦 Seedream 4.5 (高质量)"
	offering := generation.Offering{ProviderName: providerZH, ModelName: modelZH}
	executor := Executor{chat: application.Chat, texts: texts}
	english := executor.formatRefusal(chat.ToolCommand{Options: chat.TurnOptions{Locale: "en"}}, offering, "stl")
	wantEnglish := texts.Text("en", providerZH) + " · " + texts.Text("en", modelZH) + ` requires target format "stl"`
	if english != wantEnglish || strings.Contains(english, providerZH) {
		t.Fatalf("English refusal = %q, want %q", english, wantEnglish)
	}
	defaultRefusal := executor.formatRefusal(chat.ToolCommand{}, offering, "stl")
	if !strings.Contains(defaultRefusal, providerZH) || !strings.Contains(defaultRefusal, `"stl"`) {
		t.Fatalf("default refusal = %q", defaultRefusal)
	}
}

// A parameter name is an identifier, not Schema copy: an empty label keeps it
// as is in every locale, and a nil localizer keeps the default separator.
func TestHarnessParameterLabelKeepsTheIdentifierFallback(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{texts: texts}
	command := chat.ToolCommand{Options: chat.TurnOptions{Locale: "en"}}
	fallback := generation.Parameter{Name: "reference_image"}
	if got := executor.harnessParameterLabel(command, fallback); got != "reference_image" {
		t.Fatalf("identifier fallback = %q", got)
	}
	if got := executor.harnessParameterLabel(command, generation.Parameter{Name: "材质参考图"}); got != "材质参考图" {
		t.Fatalf("identifier matching Schema copy was translated: %q", got)
	}
	if got := executor.harnessParameterLabel(command, generation.Parameter{Name: "reference_image", Label: "材质参考图"}); got != "Material reference image" {
		t.Fatalf("Schema label = %q", got)
	}
	if got := executor.harnessParameterSeparator(command); got != ", " {
		t.Fatalf("English separator = %q", got)
	}
	if got := (&Executor{}).harnessParameterSeparator(chat.ToolCommand{}); got != "、" {
		t.Fatalf("nil-localizer separator = %q", got)
	}
}
