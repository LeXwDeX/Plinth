package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #683: a pick only writes state. The pick hint says the person tells the
// Agent to continue (both locales), and ask_choice tells the Agent to offer
// one next_step option per 2D candidate instead of submitting the next step
// itself with @selected in the same reply.
func TestPickHintAndCandidateNextStepGuidance(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	key := "ui.chat.image.pick_hint"
	if !strings.Contains(app.Content.Messages[key], "继续") ||
		!strings.Contains(app.Localization.Bundles["en"].Messages[key], "continue") {
		t.Fatalf("pick hint does not say to continue: %q / %q",
			app.Content.Messages[key], app.Localization.Bundles["en"].Messages[key])
	}
	var description string
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "ask_choice" {
			description = tool.Description
		}
	}
	for _, want := range []string{"2D candidates", "one option per candidate", "@selected"} {
		if !strings.Contains(description, want) {
			t.Fatalf("ask_choice description lacks %q", want)
		}
	}
}
