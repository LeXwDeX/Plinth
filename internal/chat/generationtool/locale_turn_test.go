package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func localeFixture(t *testing.T) (catalog.ApplicationSchema, *catalog.TextLocalizer) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	return application, texts
}

func failingTaskGateway(tool catalog.ChatToolSchema, failure generation.Failure) *fakeGateway {
	return &fakeGateway{
		offerings: []generation.Offering{{
			ID: "configured/offering", ModelID: "configured-model", Capability: tool.FallbackCapability,
			Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		}},
		tasks: []generation.Task{{
			ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a",
			State: generation.TaskFailed, Failure: &failure,
		}},
	}
}

// #358: a failed Task's public text follows the turn's frozen UI locale. The
// request context carries another locale on purpose: a recovered turn must
// use the locale frozen in TurnOptions, not the locale of the request that
// happens to wake it. An undeclared locale keeps the Schema's default text.
func TestGenerationFailureFollowsTheTurnsFrozenLocale(t *testing.T) {
	application, texts := localeFixture(t)
	tool := application.Chat.Tools[0]
	chinese := application.Chat.Presentation.Messages.GenerationFailedByClass[string(generation.FailureSourceExpired)]
	english := texts.Text("en", chinese)
	if chinese == "" || english == chinese {
		t.Fatalf("source_expired needs distinct default and English text: %q / %q", chinese, english)
	}
	for _, test := range []struct {
		name, locale, want string
	}{
		{"default", "", chinese},
		{"Chinese", "zh-CN", chinese},
		{"English", "en", english},
		{"undeclared locale", "fr", chinese},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway := failingTaskGateway(tool, generation.Failure{Class: generation.FailureSourceExpired})
			executor, err := generationtool.New(generationtool.Config{
				Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
				TaskStates: application.TaskStates, Texts: texts,
			})
			if err != nil {
				t.Fatal(err)
			}
			// The request locale differs from the frozen turn locale.
			ctx := catalog.WithLocale(context.Background(), "zh-CN")
			result, execErr := executor.Execute(ctx, chat.ToolCommand{
				OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
				Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"paint"}`)},
				Options: chat.TurnOptions{AIModel: "configured-model", Locale: test.locale},
			}, nil)
			if execErr == nil || result.Error != test.want {
				t.Fatalf("error=%v result.Error=%q want %q", execErr, result.Error, test.want)
			}
			if execErr.Error() != test.want {
				t.Fatalf("error text = %q, want %q", execErr.Error(), test.want)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatal(err)
			}
			if content[tool.ResultFields.Error] != test.want {
				t.Fatalf("agent content = %v", content)
			}
			if test.locale == "en" && strings.Contains(result.Error, chinese) {
				t.Fatalf("English tool result kept the default text: %q", result.Error)
			}
		})
	}
}

// #358: the reference-over-limit refusal fills its template in the turn's
// locale. Translating the filled sentence would miss the bundle entry, so
// the template and each Schema value are translated before interpolation.
func TestPreserveReferenceLimitFollowsTheTurnsFrozenLocale(t *testing.T) {
	application, texts := localeFixture(t)
	tool := application.Chat.Tools[0]
	label := "参考图"
	template := application.Runtime.GenerationMessages.ReferenceLimitExceeded
	if template == "" {
		t.Fatal("the default Schema declares no reference_limit_exceeded text")
	}
	detail, values := generation.ExecutionMessages{ReferenceLimitExceeded: template}.
		ReferenceLimitExceededText(label, 3, 1)
	failure := &generation.Error{
		Code: generation.ErrInvalidInput, Operation: "reference-selection",
		Detail: detail, UserFacing: true, DetailTemplate: template, DetailValues: values,
	}
	wantEnglish := failure.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if wantEnglish == detail || !strings.Contains(wantEnglish, "at most") {
		t.Fatalf("English refusal = %q (default %q)", wantEnglish, detail)
	}
	gateway := &fakeGateway{offerings: []generation.Offering{{
		ID: "configured/offering", ModelID: "configured-model", Capability: tool.Capability,
		Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		ReferenceSelectionPolicy: "preserve",
		ExecutionMessages:        generation.ExecutionMessages{ReferenceLimitExceeded: template},
		Parameters: []generation.Parameter{
			{Name: "configured-prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "configured-references", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1, Label: label},
		},
	}}}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
		TaskStates: application.TaskStates, Texts: texts,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, execErr := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name,
			Arguments: []byte(`{"prompt":"paint","reference_image_indices":[1,2,3]}`)},
		History: []chat.Message{{Role: application.Chat.Roles.User,
			ImageURLs: []string{"/references/one", "/references/two", "/references/three"}}},
		Options: chat.TurnOptions{AIModel: "configured-model", Locale: "en"},
	}, nil)
	if execErr != nil || !result.NoSubmission {
		t.Fatalf("refusal: result=%+v error=%v", result, execErr)
	}
	if result.Error != wantEnglish {
		t.Fatalf("tool error = %q, want %q", result.Error, wantEnglish)
	}
	if strings.Contains(result.Error, "最多") || strings.Contains(result.Error, "参考图") {
		t.Fatalf("English refusal kept the default text: %q", result.Error)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content[tool.ResultFields.Error] != wantEnglish {
		t.Fatalf("agent content = %v", content)
	}
}

// #358: a helper that fills a Schema template takes the locale mapper. The
// template and the model name are translated before {count}/{total} are
// filled, so the result follows the turn.
func TestReferenceLimitNoteTranslatesTheTemplateBeforeFilling(t *testing.T) {
	application, texts := localeFixture(t)
	tool := catalog.ChatToolSchema{ResultFields: catalog.ChatToolResultFields{ReferenceNote: "reference_note"}}
	offering := generation.Offering{
		ModelName: "configured-model",
		ExecutionMessages: generation.ExecutionMessages{
			ReferenceLimit:    application.Runtime.GenerationMessages.ReferenceLimit,
			ReferenceLimitOne: application.Runtime.GenerationMessages.ReferenceLimitOne,
		},
	}
	localize := func(text string) string { return texts.Text("en", text) }
	got := generationtool.ReferenceLimitNote(tool, offering, 1, 3, localize)
	want := strings.NewReplacer(
		"{model}", localize("configured-model"),
		"{count}", "1",
		"{total}", "3",
	).Replace(localize(application.Runtime.GenerationMessages.ReferenceLimitOne))
	if got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if got == generationtool.ReferenceLimitNote(tool, offering, 1, 3, nil) {
		t.Fatal("English note equals the default note")
	}
}

// #358: list_session_models names each model's provider and model in the turn
// locale. Only the Schema names change; the task ID and format IDs stay.
func TestSessionModelNamesFollowTheTurnLocale(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	providerZH := h.app.Chat.Presentation.Messages.GenerationFailed
	modelZH := h.app.Chat.Presentation.Messages.GenerationError
	for id, task := range h.gateway.tasks {
		if task.ID != "model-1" {
			continue
		}
		task.Offering.ProviderName, task.Offering.ModelName = providerZH, modelZH
		h.gateway.tasks[id] = task
	}
	texts, err := catalog.NewTextLocalizer(h.app)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.executor.ListSessionModels(catalog.WithLocale(context.Background(), "en"), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Models []struct {
			TaskID   string `json:"task_id"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, model := range listing.Models {
		if model.TaskID != "model-1" {
			continue
		}
		found = true
		if model.Provider != texts.Text("en", providerZH) || model.Model != texts.Text("en", modelZH) {
			t.Fatalf("model names = %q / %q", model.Provider, model.Model)
		}
	}
	if !found {
		t.Fatalf("model-1 missing from listing: %s", raw)
	}
}

// #358: next_steps.services names each service's provider and model in the
// turn locale. They are translated as their own Schema values before the
// JSON result is built.
func TestNextStepsServicesFollowTheTurnLocale(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	providerZH := h.app.Chat.Presentation.Messages.GenerationFailed
	modelZH := h.app.Chat.Presentation.Messages.GenerationError
	h.gateway.parameterFixture = func(offering generation.Offering) generation.Offering {
		offering.ProviderName, offering.ModelName = providerZH, modelZH
		return offering
	}
	texts, err := catalog.NewTextLocalizer(h.app)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", ""} {
		t.Run("locale="+locale, func(t *testing.T) {
			ctx := context.Background()
			if locale != "" {
				ctx = catalog.WithLocale(ctx, locale)
			}
			raw, err := h.executor.ListSessionModels(ctx, "owner-a", "session-a")
			if err != nil {
				t.Fatal(err)
			}
			var listing struct {
				Models []struct {
					NextSteps []struct {
						Services []struct {
							Provider string `json:"provider"`
							Model    string `json:"model"`
						} `json:"services"`
					} `json:"next_steps"`
				} `json:"models"`
			}
			if err := json.Unmarshal(raw, &listing); err != nil {
				t.Fatal(err)
			}
			seen := false
			wantProvider, wantModel := texts.Text(locale, providerZH), texts.Text(locale, modelZH)
			for _, model := range listing.Models {
				for _, step := range model.NextSteps {
					for _, service := range step.Services {
						seen = true
						if service.Provider != wantProvider || service.Model != wantModel {
							t.Fatalf("service = %q / %q, want %q / %q",
								service.Provider, service.Model, wantProvider, wantModel)
						}
					}
				}
			}
			if !seen {
				t.Fatalf("no next_steps services in %s", raw)
			}
		})
	}
}
