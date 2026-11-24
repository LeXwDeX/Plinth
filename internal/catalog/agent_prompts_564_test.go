package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #564: the shipped prompts agree with what the server does with the session
// image count and with each model's prompt language.
func TestShippedPromptsAgreeOnImageCountAndPromptLanguage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat

	// The server writes the count into every call: no text may tell the
	// Agent to make it up over several calls or to follow a count it cannot
	// set.
	count := chat.ImageCount
	for _, stale := range []string{"出不了就分几次补齐", "按用户这次说的执行"} {
		if strings.Contains(count.ContextTemplate, stale) {
			t.Errorf("chat.image_count.context_template still says %q", stale)
		}
	}
	if len(count.OfferingCapabilities) == 0 || count.LimitTemplate == "" || count.UncountedTemplate == "" {
		t.Error("chat.image_count does not report the per-model range and the models without a count")
	}

	// A session image count is a stated count for the open-request card.
	if !strings.Contains(chat.ConceptDirections.ContextTemplate, "会话设了【生成张数】也算说过") {
		t.Error("the concept quantity trigger ignores the session image count")
	}
	var askChoice string
	for _, tool := range chat.AgentTools {
		if tool.Name == "ask_choice" {
			askChoice = tool.Description
		}
	}
	if !strings.Contains(askChoice, "stated count, session image count or request to start") {
		t.Error("the ask_choice concept_quantity trigger ignores the session image count")
	}

	// The English prompt rule yields to a model whose prompt guidance names
	// another language (Seedream declares Chinese).
	if !strings.Contains(chat.Agent.SystemPrompt, "模型的提示词指引另定语言时按指引") {
		t.Error("the system prompt still requires English for every model's prompt")
	}
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" && !strings.Contains(prompt.Text, "用模型提示词指引要求的语言（没写就用英文）") {
			t.Error("the 2D craft brief still requires English for every model's prompt")
		}
	}
	for _, tool := range chat.Tools {
		if tool.Name != "refine_image" {
			continue
		}
		if strings.Contains(string(tool.Parameters), "A concise English description") {
			t.Error("refine_image's prompt argument still requires English for every model")
		}
	}
}
