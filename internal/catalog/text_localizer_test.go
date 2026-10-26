package catalog

import "testing"

func TestTextLocalizerTranslatesMessagesAndDefaultTexts(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	english := application.Localization.Bundles["en"]

	if got := localizer.Message("en", "error.forbidden"); got != english.Messages["error.forbidden"] {
		t.Fatalf("en message = %q", got)
	}
	if got := localizer.Message("zh-CN", "error.forbidden"); got != application.Content.Messages["error.forbidden"] {
		t.Fatalf("zh-CN message = %q", got)
	}
	if got := localizer.Message("fr", "error.forbidden"); got != application.Content.Messages["error.forbidden"] {
		t.Fatalf("undeclared locale message = %q", got)
	}

	// A text the server wrote from a message value or a translated field.
	if got := localizer.Text("en", application.Content.Messages["error.forbidden"]); got != english.Messages["error.forbidden"] {
		t.Fatalf("en text for a message value = %q", got)
	}
	invalid := application.Authentication.Messages.InvalidCredentials
	if got := localizer.Text("en", invalid); got != english.Fields["authentication.messages.invalid_credentials"] {
		t.Fatalf("en text for a field value = %q", got)
	}
	if got := localizer.Text("zh-CN", invalid); got != invalid {
		t.Fatalf("zh-CN text changed: %q", got)
	}
	// Text that is not Schema copy (an upstream error, a composed sentence) is left alone.
	if got := localizer.Text("en", "upstream said no"); got != "upstream said no" {
		t.Fatalf("free text changed: %q", got)
	}
}

// A generation tool that refuses a step because no model is chosen answers
// with its model_required_message; the Agent (the Mock Agent quotes it) must
// read it in the turn's locale, so every such message has an English copy.
func TestToolModelRequiredMessagesHaveEnglishText(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, tool := range application.Chat.Tools {
		if tool.ModelRequiredMessage == "" {
			continue
		}
		checked++
		if got := localizer.Text("en", tool.ModelRequiredMessage); got == tool.ModelRequiredMessage || chineseTextCharacter.MatchString(got) {
			t.Errorf("tool %s model_required_message has no English text: %q", tool.Name, got)
		}
	}
	if checked == 0 {
		t.Fatal("no tool declares model_required_message")
	}
}

func TestTextLocalizerForSingleLocaleSchema(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization = nil
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	if got := localizer.Text("en", application.Content.Messages["error.forbidden"]); got != application.Content.Messages["error.forbidden"] {
		t.Fatalf("single-locale text = %q", got)
	}
}
