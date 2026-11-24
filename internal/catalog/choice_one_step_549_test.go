package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #549: one option of a direction card does one thing. The shipped next-step
// kind declares the step an option may name and the two refusals the server
// returns, ask_choice offers one step object per option, and the Agent is told
// the rule once, in the tool description, plus what to do after the answer, in
// the answer template.
func TestShippedNextStepOptionDoesOneThing(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	if app.Revision != "2026-11-24.19.23" {
		t.Fatalf("revision = %q, want 2026-11-24.19.23", app.Revision)
	}
	kind := nextStepKind(&app)
	if kind.Step == nil || !strings.Contains(kind.Step.OneStepRefusal, "一件事") ||
		!strings.Contains(kind.Step.UnchosenRefusal, "{tool}") || !strings.Contains(kind.Step.UnchosenRefusal, "ask_choice") {
		t.Fatalf("next_step.step = %+v", kind.Step)
	}
	english := app.Localization.Bundles["en"].Fields
	for _, field := range []string{"one_step_refusal", "unchosen_refusal"} {
		if text := english["chat.choice.kinds[id=next_step].step."+field]; !strings.Contains(strings.ToLower(text), "one") {
			t.Errorf("english %s = %q", field, text)
		}
	}
	// After the answer the Agent does the chosen thing once and stops.
	for _, want := range []string{"only that one", "stop", "new card"} {
		if !strings.Contains(kind.AnswerTemplate, want) {
			t.Errorf("answer_template lacks %q: %q", want, kind.AnswerTemplate)
		}
	}
	if strings.Contains(kind.AnswerTemplate, "Continue now with exactly that direction") {
		t.Errorf("answer_template still sends the Agent on without a stop: %q", kind.AnswerTemplate)
	}
	var ask catalog.ChatAgentToolSchema
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "ask_choice" {
			ask = tool
		}
	}
	for _, want := range []string{"exactly one action", "propose_production_plan", "step"} {
		if !strings.Contains(ask.Description, want) {
			t.Errorf("ask_choice description lacks %q", want)
		}
	}
	// No example of a multi-step route survives in the card wording.
	for _, text := range []string{ask.Description, kind.AnswerTemplate, kind.PendingTemplate, kind.Message} {
		for _, forbidden := range []string{"then retexture", "多步路线", "再按参考图"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("card wording still shows a multi-step route %q", forbidden)
			}
		}
	}
	var parameters struct {
		Properties struct {
			Options struct {
				Items struct {
					Properties struct {
						Step struct {
							Type       string `json:"type"`
							Properties struct {
								Tool       struct{ Type string } `json:"tool"`
								Capability struct{ Type string } `json:"capability"`
							} `json:"properties"`
						} `json:"step"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"options"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ask.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	step := parameters.Properties.Options.Items.Properties.Step
	if step.Type != "object" || step.Properties.Tool.Type != "string" || step.Properties.Capability.Type != "string" {
		t.Fatalf("ask_choice.options.step = %+v", step)
	}
}

// #549: the Schema refuses a step declaration the runtime could not carry out.
func TestNextStepStepDeclarationRejectsDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"step on a kind with a target tool", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.Choice.Kinds {
				if a.Chat.Choice.Kinds[index].ID == "direction" {
					a.Chat.Choice.Kinds[index].Step = &catalog.ChatChoiceStepSchema{OneStepRefusal: "x", UnchosenRefusal: "y"}
				}
			}
		}},
		{"step without its refusals", func(a *catalog.ApplicationSchema) {
			nextStepKind(a).Step = &catalog.ChatChoiceStepSchema{OneStepRefusal: "只能做一件事"}
		}},
		{"ask_choice loses the step parameter", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "ask_choice" {
					a.Chat.AgentTools[index].Parameters = json.RawMessage(strings.Replace(
						string(a.Chat.AgentTools[index].Parameters), `"step": {`, `"renamed_step": {`, 1))
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
			if nextStepKind(&app).Step == nil {
				t.Fatal("the shipped next_step kind declares no step")
			}
			tc.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("invalid step declaration accepted")
			}
		})
	}
}
