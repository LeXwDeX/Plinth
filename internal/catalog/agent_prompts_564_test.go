package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #564: the shipped prompts agree with each model's prompt language.
func TestShippedPromptsAgreeOnPromptLanguage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat

	// #360 keeps the concept sketch count on its own card: no image count
	// block is a stated count for the open-request trigger.
	if strings.Contains(chat.ConceptDirections.ContextTemplate, "生成张数") {
		t.Error("the concept quantity trigger reads the session image count, which #360 keeps separate")
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
