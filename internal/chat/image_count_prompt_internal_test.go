package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #486: the person's own 2D image count reaches the system prompt, so the
// Agent works to the number they set instead of guessing one. The block is a
// session fact read on every request, and a session that set none renders
// nothing rather than a number nobody chose.
func TestImageCountPromptStatesThePersonsCount(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	if strings.TrimSpace(schema.ImageCount.ContextTemplate) == "" {
		t.Fatal("默认 Catalog 未声明 chat.image_count.context_template")
	}
	orchestrator := &Orchestrator{
		schema: schema,
		imageCount: func(ownerID, sessionID string) (int, bool) {
			if ownerID == "owner-a" && sessionID == "session-1" {
				return 4, true
			}
			return 0, false
		},
	}
	prompt := orchestrator.imageCountPrompt("owner-a", "session-1")
	if !strings.Contains(prompt, "4 张") {
		t.Fatalf("prompt lacks the count: %q", prompt)
	}
	if strings.Contains(prompt, "{count}") {
		t.Fatalf("prompt kept the placeholder: %q", prompt)
	}
	if prompt := orchestrator.imageCountPrompt("owner-a", "session-2"); prompt != "" {
		t.Fatalf("a session without a count carries a number: %q", prompt)
	}
	if prompt := (&Orchestrator{schema: schema}).imageCountPrompt("owner-a", "session-1"); prompt != "" {
		t.Fatalf("a prompt without the reader carries a number: %q", prompt)
	}
}

// The count follows the person across Offerings: it is one stored value, so
// the Schema's own copy never names an Offering, a model or a parameter.
func TestImageCountPromptIsNotPerOffering(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	template := loaded.Application().Chat.ImageCount.ContextTemplate
	for _, placeholder := range placeholders(template) {
		if placeholder != "count" {
			t.Fatalf("模板还依赖别的占位符 %q: %q", placeholder, template)
		}
	}
	for _, name := range []string{"max_images", "estimated_credits_unit_parameter", "seedream", "gpt-image"} {
		if strings.Contains(template, name) {
			t.Fatalf("模板写死了 %q: %q", name, template)
		}
	}
}

// placeholders returns the names inside every {…} of a template.
func placeholders(template string) []string {
	var names []string
	for rest := template; ; {
		open := strings.Index(rest, "{")
		if open < 0 {
			return names
		}
		rest = rest[open+1:]
		close := strings.Index(rest, "}")
		if close < 0 {
			return names
		}
		names = append(names, rest[:close])
		rest = rest[close+1:]
	}
}