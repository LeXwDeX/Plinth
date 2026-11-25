package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #620: the directory declares how the Agent is told which models apply a
// style Skill ({models}) and what it is told when none is available; the
// Agent's list_offerings tool explains the style_skill mark.
func TestSkillDirectoryDeclaresTheStyleSkillModels(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	directory := chat.Skills.Directory
	if !strings.Contains(directory.StyleModelsTemplate, "{models}") || strings.TrimSpace(directory.Messages.NoStyleModel) == "" {
		t.Fatalf("style models template %q, no style model %q", directory.StyleModelsTemplate, directory.Messages.NoStyleModel)
	}
	if got := directory.StyleModelsText([]string{"a", "b"}); !strings.Contains(got, "a、b") || strings.Contains(got, "{models}") {
		t.Fatalf("models line = %q", got)
	}
	if got := directory.StyleModelsText(nil); got != directory.Messages.NoStyleModel {
		t.Fatalf("no models = %q", got)
	}
	described := false
	for _, tool := range chat.AgentTools {
		if tool.Name == "list_offerings" {
			described = strings.Contains(tool.Description, "style_skill")
		}
	}
	if !described {
		t.Fatal("list_offerings does not explain style_skill")
	}
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"no models slot": func(app *catalog.ApplicationSchema) {
			app.Chat.Skills.Directory.StyleModelsTemplate = "支持画风 Skill 的模型。"
		},
		"no no-model answer": func(app *catalog.ApplicationSchema) { app.Chat.Skills.Directory.Messages.NoStyleModel = "" },
	} {
		app := product.Application()
		app.Localization = nil
		mutate(&app)
		if err := catalog.ValidateApplicationSchema(app); err == nil {
			t.Errorf("%s: the Schema was accepted", name)
		}
	}
}
