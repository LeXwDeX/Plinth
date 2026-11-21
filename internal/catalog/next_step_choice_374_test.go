package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #374: the shipped Catalog declares the next-step card as an agent kind with
// no target tool, whose options may carry the two page-performed actions, and
// ask_choice offers the Agent exactly that vocabulary.
func TestShippedNextStepCardNamesNoTargetTool(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	var kind catalog.ChatChoiceKindSchema
	ids := []string{}
	for _, declared := range app.Chat.Choice.Kinds {
		ids = append(ids, declared.ID)
		if declared.ID == "next_step" {
			kind = declared
		}
	}
	if kind.ID == "" {
		t.Fatalf("choice.kinds %v declares no next_step", ids)
	}
	if kind.ToolName != "" || kind.Argument != "" || kind.OptionSource != "agent" || kind.MinOptions != 2 || kind.MaxOptions != 4 ||
		!slices.Equal(kind.Actions, []string{catalog.ChoiceActionSwitchOperation, catalog.ChoiceActionClearOperation}) {
		t.Fatalf("next_step = %+v", kind)
	}
	for name, text := range map[string]string{"answer_template": kind.AnswerTemplate, "pending_template": kind.PendingTemplate} {
		if !strings.Contains(text, "{continuation}") {
			t.Errorf("%s lacks {continuation}", name)
		}
	}
	if kind.Question == "" || kind.Message == "" {
		t.Fatalf("next_step words no question or message: %+v", kind)
	}
	var ask catalog.ChatAgentToolSchema
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "ask_choice" {
			ask = tool
		}
	}
	var parameters struct {
		Properties struct {
			Kind struct {
				Enum []string `json:"enum"`
			} `json:"kind"`
			Options struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Properties struct {
						Action struct {
							Properties struct {
								Type struct {
									Enum []string `json:"enum"`
								} `json:"type"`
								OperationID struct {
									MaxLength int `json:"maxLength"`
								} `json:"operation_id"`
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
	if !slices.Equal(parameters.Properties.Kind.Enum, ids) || ids[len(ids)-1] != "next_step" {
		t.Fatalf("ask_choice.kind enum %v does not follow choice.kinds %v", parameters.Properties.Kind.Enum, ids)
	}
	if !slices.Equal(parameters.Properties.Options.Items.Properties.Action.Properties.Type.Enum, kind.Actions) ||
		parameters.Properties.Options.Items.Properties.Action.Properties.OperationID.MaxLength == 0 {
		t.Fatalf("ask_choice.options.action = %+v", parameters.Properties.Options.Items.Properties.Action)
	}
	// The card's own bound sits inside the schema's: 2 to 4 of at most 8.
	if app.Chat.Choice.MaxOptions < kind.MaxOptions {
		t.Fatalf("choice.max_options %d is below next_step's %d", app.Chat.Choice.MaxOptions, kind.MaxOptions)
	}
}

// #374: a fixed function still reaches the next-step card (ask_choice is a base
// tool), and its function context and the plan tool's description lead the
// Agent to that card with the real function list instead of an invented name.
func TestShippedHarnessLeadsTheAgentToTheNextStepCard(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	if !slices.Contains(app.Chat.Harness.BaseTools, "ask_choice") {
		t.Fatalf("harness base_tools = %v lacks ask_choice", app.Chat.Harness.BaseTools)
	}
	english := loaded.Application().Localization.Bundles["en"].Fields
	for name, text := range map[string]string{
		"context_template zh":  app.Chat.Harness.ContextTemplate,
		"context_template en":  english["chat.harness.context_template"],
		"proposal_message zh":  app.Chat.Harness.ProposalMessage,
		"proposal_message en":  english["chat.harness.proposal_message"],
		"ask_choice (default)": askChoiceDescription(t, app),
	} {
		for _, want := range []string{"next_step", "switch_operation"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %s", name, want)
			}
		}
	}
	for name, text := range map[string]string{
		"context_template zh": app.Chat.Harness.ContextTemplate,
		"context_template en": english["chat.harness.context_template"],
		"ask_choice":          askChoiceDescription(t, app),
	} {
		for _, want := range []string{"clear_operation", "switchable_operations"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %s", name, want)
			}
		}
	}
	for _, key := range []string{"chat.choice.kinds[id=next_step].question", "chat.choice.kinds[id=next_step].message"} {
		if strings.TrimSpace(english[key]) == "" {
			t.Errorf("English bundle lacks %s", key)
		}
	}
}

// #374/#407: the Agent's standing rule says when a decision needs a card and
// that a card is used instead of a list; the card vocabulary (kinds, 2 to 4
// options, one card per reply, switchable operations) lives in the ask_choice
// description, and the purpose question lives in the brief texts. Each rule is
// stated once.
func TestShippedAgentPromptAsksForDirectionCardsAndFewerQuestions(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	prompt := app.Chat.Agent.SystemPrompt
	// The brief tool's own description must not ask for the purpose again.
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "update_production_brief" && !strings.Contains(tool.Description, "needs no separate purpose question") {
			t.Errorf("update_production_brief still asks for the purpose on a clear instruction: %s", tool.Description)
		}
	}
	for _, want := range []string{
		"一次一张卡", "一回合最多一张", "方向卡", "不要写成编号或列表",
		"用户给了明确的生成指令且输入齐全时，直接开始",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "kind=next_step") || strings.Contains(prompt, "switchable_operations") {
		t.Error("the system prompt repeats the card vocabulary that lives in the ask_choice description")
	}
	ask := askChoiceDescription(t, app)
	for _, want := range []string{
		"two or more directions", "numbered or bulleted list", "at most one card per reply",
		"2 to 4 options", "switchable_operations", "clear_operation", "usage_profile",
	} {
		if !strings.Contains(ask, want) {
			t.Errorf("ask_choice description lacks %q", want)
		}
	}
	if !strings.Contains(app.Chat.Model3DUsage.Brief.EmptyText, "问清用途") {
		t.Error("the empty-brief text no longer asks for the purpose")
	}
}

func askChoiceDescription(t *testing.T, app catalog.ApplicationSchema) string {
	t.Helper()
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "ask_choice" {
			return tool.Description
		}
	}
	t.Fatal("ask_choice is not declared")
	return ""
}

// #374: the Schema refuses a next-step declaration the runtime could not carry
// out: a missing action vocabulary on ask_choice, a tool or argument on a kind
// that names no tool, or an action nobody implements.
func TestShippedNextStepDeclarationRejectsDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"target tool on the next-step kind with templates", func(a *catalog.ApplicationSchema) {
			nextStepKind(a).ToolName = "update_production_brief"
		}},
		{"argument on a kind without a tool", func(a *catalog.ApplicationSchema) { nextStepKind(a).Argument = "style" }},
		{"templates without a continuation", func(a *catalog.ApplicationSchema) { nextStepKind(a).AnswerTemplate = "chose it" }},
		{"unimplemented action", func(a *catalog.ApplicationSchema) { nextStepKind(a).Actions = []string{"delete_session"} }},
		{"option bound above the card bound", func(a *catalog.ApplicationSchema) { nextStepKind(a).MaxOptions = a.Chat.Choice.MaxOptions + 1 }},
		{"ask_choice loses the action parameter", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "ask_choice" {
					a.Chat.AgentTools[index].Parameters = json.RawMessage(strings.Replace(
						string(a.Chat.AgentTools[index].Parameters), `"action"`, `"renamed_action"`, 1))
				}
			}
		}},
		{"paid tool as a base tool next to ask_choice", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.BaseTools = append(a.Chat.Harness.BaseTools, "create_three_view")
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
				t.Fatal("invalid next-step declaration accepted")
			}
		})
	}
}

func nextStepKind(app *catalog.ApplicationSchema) *catalog.ChatChoiceKindSchema {
	for index := range app.Chat.Choice.Kinds {
		if app.Chat.Choice.Kinds[index].ID == "next_step" {
			return &app.Chat.Choice.Kinds[index]
		}
	}
	panic("missing next_step kind")
}
