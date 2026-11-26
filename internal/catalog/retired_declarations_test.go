package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #521: declarations the runtime never reads must not stay in the default
// Catalog. Each name below belonged to a path #490-#495 removed, or was never
// wired; keeping it only told a maintainer (or an Agent) something that was not
// true. Add a name here when a declaration is deleted, so it cannot come back.
func TestDefaultCatalogCarriesNoRetiredDeclarations(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, retired := range []string{
		// The conversational consent state machine (#491): paid steps are
		// authorized by exact execution grants, so its Agent texts and the
		// error for its stored state are gone.
		"consent_approved_content", "consent_blocked_content", "consent_required_content",
		"error.consent_unavailable",
		// Never read by any runtime code.
		"completion_flush_milliseconds", "purpose_argument",
	} {
		if strings.Contains(text, `"`+retired+`"`) {
			t.Errorf("the default Catalog still declares %q", retired)
		}
	}
	// The declarations that replaced them are still read.
	if _, err := catalog.LoadDefault(); err != nil {
		t.Fatal(err)
	}
}

func defaultChat(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Application struct {
			Chat map[string]any `json:"chat"`
		} `json:"application"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document.Application.Chat
}

func chatTool(t *testing.T, chat map[string]any, name string) map[string]any {
	t.Helper()
	for _, list := range []string{"tools", "agent_tools"} {
		for _, entry := range chat[list].([]any) {
			if tool := entry.(map[string]any); tool["name"] == name {
				return tool
			}
		}
	}
	t.Fatalf("tool %s is not declared", name)
	return nil
}

// #521: the budget-mode copy says what the server does. A step the approved
// plan covers is approved even with extra credits (the plan's reservation
// covers them, #490's cost rule belongs to manual mode), so extra credits are
// not a reason for a card there; what does card is a capability outside the
// plan, the unit running out, and the repeat of a charged-but-unstored step.
// A zero-credit report step (analyze_model) is a capability like any other, and
// the copy tells the Agent how to keep it from carding.
func TestBudgetCopyMatchesWhatTheServerDoes(t *testing.T) {
	chat := defaultChat(t)
	approval := chat["approval_policy"].(map[string]any)
	budgetBlock := approval["budget"].(map[string]any)
	modes := approval["prompt"].(map[string]any)["modes"].([]any)
	var budgetMode string
	for _, entry := range modes {
		if mode := entry.(map[string]any); mode["id"] == "budget" {
			budgetMode = mode["description"].(string)
		}
	}
	plan := chatTool(t, chat, "propose_production_plan")["description"].(string)
	texts := map[string]string{
		"propose_production_plan.description": plan,
		"budget.auto_approval_note":           budgetBlock["auto_approval_note"].(string),
		"budget.continuation.agent_template":  budgetBlock["continuation"].(map[string]any)["agent_template"].(string),
		"modes[budget].description":           budgetMode,
	}
	for name, text := range texts {
		if text == "" {
			t.Fatalf("%s is missing", name)
		}
		// Extra export formats and provider add-on fees do not card a covered
		// step in budget mode; the old copy listed them as an example of a step
		// that "would charge again".
		for _, stale := range []string{"extra export formats, additional fees", "额外导出格式、Tripo 的附加费用"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s still lists %q as a reason for a card in budget mode", name, stale)
			}
		}
	}
	for _, name := range []string{"propose_production_plan.description", "budget.auto_approval_note", "modes[budget].description"} {
		if !strings.Contains(texts[name], "correction_capabilities") {
			t.Errorf("%s does not tell the Agent how a zero-credit report step avoids a card", name)
		}
	}
	// The fixed note about the recharge guard stays, in the words of the guard.
	if !strings.Contains(plan, "charged but whose result was not stored") {
		t.Error("propose_production_plan.description lost the recharge-guard card reason")
	}
	// The correction_capabilities property names capabilities, not tools.
	properties := chatTool(t, chat, "propose_production_plan")["parameters"].(map[string]any)["properties"].(map[string]any)
	correction := properties["correction_capabilities"].(map[string]any)["description"].(string)
	if strings.Contains(correction, "process_model") {
		t.Errorf("correction_capabilities offers a tool name as a capability example: %q", correction)
	}
	if !strings.Contains(correction, "never tool names") {
		t.Errorf("correction_capabilities does not say it takes capability ids: %q", correction)
	}
	// analyze_model: auto and manual run it without a card; budget decides it
	// like any capability.
	analyze := chatTool(t, chat, "analyze_model")["description"].(string)
	if strings.Contains(analyze, "approval mode has nothing to decide") {
		t.Error("analyze_model still claims the approval mode never decides a report step")
	}
	if !strings.Contains(analyze, "Budget mode") || !strings.Contains(analyze, "correction_capabilities") {
		t.Error("analyze_model does not say a report step outside the plan cards in budget mode")
	}
}

// #521: analyze_model submits a Task like process_model does, and a Meshy
// printability report can run for minutes, so its wait must be able to continue
// in the background instead of holding the turn (and the calls batched with it).
func TestAnalyzeModelMayContinueInTheBackground(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy := c.Application().Chat.Agent.BackgroundWait
	if !policy.Allows("analyze_model") {
		t.Fatalf("analyze_model is not in chat.agent.background_wait.tools: %v", policy.Tools)
	}
	if !policy.Allows("process_model") {
		t.Fatal("process_model left chat.agent.background_wait.tools")
	}
}
