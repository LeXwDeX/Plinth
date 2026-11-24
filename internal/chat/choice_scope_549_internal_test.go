package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type failingChoices struct{ ChoiceStore }

func (failingChoices) ListChoices(context.Context, string, string, int) ([]Choice, error) {
	return nil, errors.New("choice store unavailable")
}

// #549: the answer-turn scope is derived from stored data only (the card and
// the turn's last user message), so an orchestrator that restarted in the
// middle of the turn scopes it exactly as before; nothing is kept in memory.
func TestTurnChoiceScopeComesFromStoredDataOnly_549(t *testing.T) {
	schema := suspendTestSchema()
	schema.Choice.Kinds = []catalog.ChatChoiceKindSchema{{ID: "next_step", OptionSource: "agent",
		Step: &catalog.ChatChoiceStepSchema{OneStepRefusal: "一件事", UnchosenRefusal: "没有选 {tool}"}}}
	history := []Message{
		{ID: "user-1", Role: schema.Roles.User, Content: "优化面数"},
		{ID: "assistant-1", Role: schema.Roles.Assistant, Content: "请选择"},
		{ID: "user-2", Role: schema.Roles.User, Content: "选择：保轮廓"},
	}
	card := func(mutate func(*Choice)) Choice {
		choice := Choice{ID: "choice-1", Kind: "next_step", Capability: "next_step", StepIndex: -1, State: ChoiceAnswered,
			Options: []ChoiceOption{
				{ID: "balanced", Label: "保轮廓", Step: &ChoiceStep{Tool: "process_model", Capability: "remesh"}},
				{ID: "bake", Label: "保留木纹"},
			},
			AnswerOptionID: "balanced", AnswerSource: ChoiceAnsweredOnCard, AnswerMessageID: "user-2"}
		if mutate != nil {
			mutate(&choice)
		}
		return choice
	}
	scopeOf := func(store ChoiceStore, kinds []catalog.ChatChoiceKindSchema) *ChoiceScope {
		service := newChatServiceForInternal(t)
		service.repository = choicePromptRepository{}
		service.SetChoices(store, 20)
		fresh := schema
		fresh.Choice.Kinds = kinds
		return (&Orchestrator{schema: fresh, service: service}).turnChoiceScope(context.Background(), "owner", "session", history)
	}
	scope := scopeOf(fixedChoices{choices: []Choice{card(nil)}}, schema.Choice.Kinds)
	if scope == nil || scope.Step == nil || scope.Step.Tool != "process_model" || scope.Step.Capability != "remesh" ||
		scope.OptionLabel != "保轮廓" || scope.Refusal != "没有选 {tool}" {
		t.Fatalf("scope = %+v", scope)
	}
	if !scope.Permits("process_model", "remesh") || scope.Permits("process_model", "resize") || scope.Permits("retexture_model", "") {
		t.Fatalf("scope permits the wrong steps: %+v", scope.Step)
	}
	if scope := scopeOf(fixedChoices{choices: []Choice{card(func(c *Choice) { c.AnswerOptionID = "bake" })}}, schema.Choice.Kinds); scope == nil ||
		scope.Step != nil || scope.Permits("process_model", "remesh") {
		t.Fatalf("an option without a step = %+v", scope)
	}
	unscoped := map[string]Choice{
		"answered with an earlier message": card(func(c *Choice) { c.AnswerMessageID = "user-1" }),
		"typed reply mapped by the Agent":  card(func(c *Choice) { c.AnswerSource = ChoiceAnsweredByAgent }),
		"still pending":                    card(func(c *Choice) { c.State = ChoicePending }),
		"kind with a target tool":          card(func(c *Choice) { c.ToolName = "update_production_brief" }),
	}
	for name, choice := range unscoped {
		if scope := scopeOf(fixedChoices{choices: []Choice{choice}}, schema.Choice.Kinds); scope != nil {
			t.Errorf("%s scoped the turn: %+v", name, scope)
		}
	}
	// A Catalog whose kind declares no step leaves every turn unscoped.
	if scope := scopeOf(fixedChoices{choices: []Choice{card(nil)}},
		[]catalog.ChatChoiceKindSchema{{ID: "next_step", OptionSource: "agent"}}); scope != nil {
		t.Fatalf("a kind without step scoped the turn: %+v", scope)
	}
	// Unreadable cards never widen the turn: no paid step is allowed.
	if scope := scopeOf(failingChoices{}, schema.Choice.Kinds); scope == nil || scope.Step != nil || scope.Refusal == "" {
		t.Fatalf("an unreadable card store = %+v", scope)
	}
}
