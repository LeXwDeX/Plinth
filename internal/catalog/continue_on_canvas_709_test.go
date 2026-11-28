package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #709: the next-step card may carry continue_on_canvas, the one option action
// that brings the board in. The page performs it (it sends the person's
// previous message again as a canvas turn); the Agent learns it from ask_choice
// and the answer template, and the card's badge words come from the Schema.
func TestShippedNextStepCardOffersContinueOnCanvas(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	kind := nextStepKind(&app)
	if !slices.Contains(kind.Actions, catalog.ChoiceActionContinueOnCanvas) {
		t.Fatalf("next_step actions %v lack continue_on_canvas", kind.Actions)
	}
	if !strings.Contains(kind.AnswerTemplate, "continue_on_canvas") {
		t.Fatal("next_step answer_template does not say what continue_on_canvas did")
	}
	var ask catalog.ChatAgentToolSchema
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "ask_choice" {
			ask = tool
		}
	}
	if !strings.Contains(ask.Description, "continue_on_canvas") {
		t.Fatal("ask_choice description does not name continue_on_canvas")
	}
	var parameters struct {
		Properties struct {
			Options struct {
				Items struct {
					Properties struct {
						Action struct {
							Properties struct {
								Type struct {
									Enum []string `json:"enum"`
								} `json:"type"`
							} `json:"properties"`
						} `json:"action"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"options"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ask.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(parameters.Properties.Options.Items.Properties.Action.Properties.Type.Enum, catalog.ChoiceActionContinueOnCanvas) {
		t.Fatalf("ask_choice.options.action.type enum %v lacks continue_on_canvas", parameters.Properties.Options.Items.Properties.Action.Properties.Type.Enum)
	}
	for locale, messages := range map[string]map[string]string{
		"zh": app.Content.Messages, "en": app.Localization.Bundles["en"].Messages,
	} {
		for _, key := range []string{
			"ui.chat.choice.action_continue_on_canvas",
			"ui.chat.choice.action_board_unavailable",
			"ui.chat.choice.action_original_missing",
		} {
			if strings.TrimSpace(messages[key]) == "" {
				t.Errorf("%s lacks %s", locale, key)
			}
		}
	}
}

func TestContinueOnCanvasDeclarationRejectsDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"declared twice", func(a *catalog.ApplicationSchema) {
			kind := nextStepKind(a)
			kind.Actions = append(kind.Actions, catalog.ChoiceActionContinueOnCanvas)
		}},
		{"kind declares it but ask_choice does not offer it", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "ask_choice" {
					a.Chat.AgentTools[index].Parameters = json.RawMessage(strings.Replace(
						string(a.Chat.AgentTools[index].Parameters), `, "continue_on_canvas"]`, `]`, 1))
				}
			}
		}},
		{"on a kind whose options the Agent does not write", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.Choice.Kinds {
				if a.Chat.Choice.Kinds[index].OptionSource != "agent" {
					a.Chat.Choice.Kinds[index].Actions = []string{catalog.ChoiceActionContinueOnCanvas}
					return
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loaded, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := loaded.Application()
			tc.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("invalid continue_on_canvas declaration accepted")
			}
		})
	}
}
