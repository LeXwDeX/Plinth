package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #241: the shipped preference block, routes, member names and UI copy are
// declared and validated together.
func TestDefaultChatPreferencesDeclarationIsValid(t *testing.T) {
	app := loadChatApplication(t)
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	preferences := app.Chat.Preferences
	if preferences.MaximumEntries <= 0 || preferences.MaximumPromptItems <= 0 ||
		preferences.MaximumPromptItems > preferences.MaximumEntries ||
		preferences.MaximumDirectionCharacters <= 0 || preferences.MaximumReasonCharacters <= 0 {
		t.Fatalf("bounds = %+v", preferences)
	}
	if preferences.OpenMarker != "<<<PREFERENCES>>>" || preferences.CloseMarker != "<<<END_PREFERENCES>>>" {
		t.Fatalf("markers = %q %q", preferences.OpenMarker, preferences.CloseMarker)
	}
	if !strings.Contains(preferences.ContextTemplate, "{open}") ||
		!strings.Contains(preferences.ContextTemplate, "{items}") ||
		!strings.Contains(preferences.ContextTemplate, "{close}") {
		t.Fatalf("context template = %q", preferences.ContextTemplate)
	}
	ids := make([]string, 0, len(preferences.ReasonPresets))
	for _, preset := range preferences.ReasonPresets {
		ids = append(ids, preset.ID)
		if app.Content.Messages["ui.workspace.preferences.reason."+preset.ID] == "" ||
			app.Localization.Bundles["en"].Messages["ui.workspace.preferences.reason."+preset.ID] == "" {
			t.Fatalf("reason %q lacks a localized UI message", preset.ID)
		}
	}
	if strings.Join(ids, ",") != "style,proportion,detail,reference,other" {
		t.Fatalf("reason ids = %v", ids)
	}
	for _, key := range []string{
		"ui.chat.image.reject", "ui.chat.image.rejected", "ui.chat.image.reject_failed",
		"ui.workspace.preferences.title", "ui.workspace.preferences.hint",
		"ui.workspace.preferences.empty", "ui.workspace.preferences.load_failed",
		"ui.workspace.preferences.edit", "ui.workspace.preferences.delete",
		"ui.workspace.preferences.save", "ui.workspace.preferences.too_long",
	} {
		if app.Content.Messages[key] == "" || app.Localization.Bundles["en"].Messages[key] == "" {
			t.Fatalf("%s lacks a localized UI message", key)
		}
	}
	// The Agent-facing use rule is in the system prompt and both craft prompts.
	for name, text := range map[string]string{
		"system":  app.Chat.Agent.SystemPrompt,
		"image2d": craftPromptText(app, "image2d"),
		"model3d": craftPromptText(app, "model3d"),
	} {
		if !strings.Contains(text, "偏好") || !strings.Contains(text, "已否决") {
			t.Fatalf("%s prompt does not tell the Agent to use the preferences", name)
		}
	}
	if app.Chat.API.Preference.ReasonPreset == "" || app.Chat.API.Preference.Signal == "" ||
		app.Chat.API.ResponseFields.Preferences == app.Chat.API.Fields.Content {
		t.Fatalf("preference members = %+v", app.Chat.API.Preference)
	}
}

func craftPromptText(app catalog.ApplicationSchema, id string) string {
	for _, prompt := range app.Chat.Agent.CraftPrompts {
		if prompt.ID == id {
			return prompt.Text
		}
	}
	return ""
}

func TestChatPreferencesDeclarationIsValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"a prompt cap above the entry cap", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.MaximumPromptItems = app.Chat.Preferences.MaximumEntries + 1
		}, "maximum_prompt_items"},
		{"no items placeholder", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ContextTemplate = "{open}{close}"
		}, "context_template"},
		{"items before open", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ContextTemplate = "{items}{open}{close}"
		}, "context_template"},
		{"one marker", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.CloseMarker = app.Chat.Preferences.OpenMarker
		}, "context_template"},
		{"a marker shared with the notes", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.OpenMarker = app.Chat.Context.SessionNotesOpenMarker
		}, "设定摘要"},
		{"a selected item without the handle", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.SelectedItemTemplate = "- {direction}"
		}, "selected_item_template"},
		{"a rejection without the reason", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.RejectedItemTemplate = "- {direction}（{handle}）"
		}, "rejected_item_template"},
		{"a reason join without text", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ReasonJoinTemplate = "{preset}"
		}, "reason_join_template"},
		{"no reason presets", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ReasonPresets = nil
		}, "reason_presets 不能为空"},
		{"an unstable preset id", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ReasonPresets[0].ID = "Style"
		}, "小写稳定 id"},
		{"a duplicate preset id", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ReasonPresets[1].ID = app.Chat.Preferences.ReasonPresets[0].ID
		}, "id 重复"},
		{"a preset without a label", func(app *catalog.ApplicationSchema) {
			app.Chat.Preferences.ReasonPresets[0].Label = ""
		}, "小写稳定 id 与非空 label"},
		{"a preset without UI copy", func(app *catalog.ApplicationSchema) {
			delete(app.Content.Messages, "ui.workspace.preferences.reason.style")
		}, "ui.workspace.preferences.reason.style"},
		{"no preferences response member", func(app *catalog.ApplicationSchema) {
			app.Chat.API.ResponseFields.Preferences = ""
		}, "api.response_fields.preferences"},
		{"no signal member", func(app *catalog.ApplicationSchema) {
			app.Chat.API.Preference.Signal = ""
		}, "api.preference.signal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := loadChatApplication(t)
			test.mutate(&app)
			err := catalog.ValidateApplicationSchema(app)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	// A Catalog that declares no preference storage stays valid; the feature is
	// simply absent, like every other optional block. A zero entry cap is the
	// declared off switch and is valid too.
	app := loadChatApplication(t)
	app.Chat.Preferences = catalog.ChatPreferencesSchema{}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Catalog without preferences must stay valid: %v", err)
	}
	off := loadChatApplication(t)
	off.Chat.Preferences.MaximumEntries = 0
	if err := catalog.ValidateApplicationSchema(off); err != nil {
		t.Fatalf("a zero preference entry cap must disable the feature: %v", err)
	}
}

func TestChatPreferencesPublicProjectionPublishesOnlyBoundsAndPresetIDs(t *testing.T) {
	app := loadChatApplication(t)
	public := app.Public()
	if public.Chat.Preferences == nil {
		t.Fatal("the public projection has no preferences")
	}
	if public.Chat.Preferences.MaximumEntries != app.Chat.Preferences.MaximumEntries ||
		public.Chat.Preferences.MaximumDirectionCharacters != app.Chat.Preferences.MaximumDirectionCharacters ||
		public.Chat.Preferences.MaximumReasonCharacters != app.Chat.Preferences.MaximumReasonCharacters {
		t.Fatalf("public bounds = %+v", public.Chat.Preferences)
	}
	if strings.Join(public.Chat.Preferences.ReasonPresets, ",") != "style,proportion,detail,reference,other" {
		t.Fatalf("public reason ids = %v", public.Chat.Preferences.ReasonPresets)
	}
	if public.Chat.API.Preference.ReasonPreset != app.Chat.API.Preference.ReasonPreset ||
		public.Chat.API.Preference.Kind == "" {
		t.Fatalf("public preference members = %+v", public.Chat.API.Preference)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		app.Chat.Preferences.OpenMarker, app.Chat.Preferences.CloseMarker,
		app.Chat.Preferences.ContextTemplate, app.Chat.Preferences.SelectedItemTemplate,
		app.Chat.Preferences.RejectedItemTemplate,
	} {
		if secret != "" && strings.Contains(string(encoded), secret) {
			t.Fatalf("server-only preference text leaked into the public schema: %q", secret)
		}
	}
	// The failure texts and prompt paragraph stay server-only too.
	for _, secret := range []string{"已选用方向要沿用", "不要换个说法重复提交"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("server-only preference prompt leaked: %q", secret)
		}
	}
}
