package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #251: the plan contract is declared in the Product Schema, and its bounds
// match the runtime constants so neither side can drift silently.
func TestDefaultCatalogDeclaresThePlanAcceptanceContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	budget := app.Chat.ApprovalPolicy.Budget
	if budget.MaxAcceptanceCriteria <= 0 || budget.MaxChecksPerCheckpoint <= 0 ||
		budget.MaxContingencies < 0 || budget.MaxWaivedCheckpoints != processing.MaxWaivedCheckpoints {
		t.Fatalf("plan contract bounds = %+v", budget)
	}
	if budget.Acceptance.Report == "" || budget.Acceptance.PurposeLine == "" ||
		budget.Acceptance.Criteria["triangles"] == "" || budget.Acceptance.Criteria["pivot_bottom_center"] == "" {
		t.Fatalf("acceptance templates = %+v", budget.Acceptance)
	}
	// #251 fix: a check with both bounds is worded as a range, and the English
	// bundle carries the acceptance and report templates.
	if rangeTemplate := budget.Acceptance.RangeCriteria["triangles"]; rangeTemplate == "" ||
		!strings.Contains(rangeTemplate, "{minimum}") || !strings.Contains(rangeTemplate, "{maximum}") {
		t.Fatalf("triangle range template = %q", rangeTemplate)
	}
	texts, err := catalog.NewTextLocalizer(app)
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"range":  budget.Acceptance.RangeCriteria["triangles"],
		"report": budget.Acceptance.Report,
	} {
		if translated := texts.Text("en", source); translated == "" || translated == source {
			t.Fatalf("%s acceptance text has no English template: %q", name, source)
		}
	}
	for _, placeholder := range []string{"{acceptance}", "{checkpoints}", "{contingencies}", "{report}"} {
		if !strings.Contains(budget.ContextTemplate, placeholder) {
			t.Fatalf("context_template lacks %s", placeholder)
		}
	}
}

func TestDefaultCatalogDeclaresProcessingFailuresAndRevision(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	failures := app.Chat.Processing.Failures
	wantCategories := map[string]bool{}
	for _, category := range generation.FailureCategories() {
		wantCategories[string(category)] = true
		if failures.Categories[string(category)] == "" || failures.Advice[string(category)] == "" {
			t.Fatalf("category %q missing words", category)
		}
	}
	for declared := range failures.Categories {
		if !wantCategories[declared] {
			t.Fatalf("undeclared failure category %q", declared)
		}
	}
	wantActions := map[string]bool{}
	for _, action := range generation.FailureActions() {
		wantActions[string(action)] = true
		if failures.Actions[string(action)] == "" {
			t.Fatalf("action %q missing words", action)
		}
	}
	for declared := range failures.Actions {
		if !wantActions[declared] {
			t.Fatalf("undeclared failure action %q", declared)
		}
	}
	if failures.CheckpointFailed == "" || failures.RevisionApplied == "" || failures.ReasonRequired == "" {
		t.Fatalf("processing messages = %+v", failures)
	}

	var planTool *catalog.ChatToolSchema
	var reviseTool *catalog.ChatAgentToolSchema
	for index := range app.Chat.Tools {
		if app.Chat.Tools[index].Name == "plan_model_processing" {
			planTool = &app.Chat.Tools[index]
		}
	}
	for index := range app.Chat.AgentTools {
		if app.Chat.AgentTools[index].Name == "revise_processing_plan" {
			reviseTool = &app.Chat.AgentTools[index]
		}
	}
	if planTool == nil || planTool.Processing.Plan.Revision.MaxReasonCharacters != processing.MaxRevisionReasonCharacters ||
		planTool.Processing.Plan.Revision.MaxWaivedCheckpoints != processing.MaxWaivedCheckpoints {
		t.Fatalf("plan revision bounds = %+v", planTool)
	}
	if reviseTool == nil || reviseTool.Execution.ImplementationKey != "processing.revise" ||
		reviseTool.Execution.AgentScope != "3d" || len(reviseTool.Execution.Domains) != 1 ||
		reviseTool.Execution.Domains[0] != "model-3d" ||
		reviseTool.Execution.Effects[0] != "SESSION_WRITE" || reviseTool.Execution.Idempotency != "required" {
		t.Fatalf("revise tool = %+v", reviseTool)
	}
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(reviseTool.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan_id", "expected_plan_version", "operation", "step_index", "reason"} {
		if parameters.Properties[name] == nil || !strings.Contains(strings.Join(parameters.Required, " "), name) {
			t.Fatalf("revise argument %q is missing or optional", name)
		}
	}
}

func TestPlanToolDeclaresRetextureAndFormatConversion(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var tool *catalog.ChatToolSchema
	for index := range c.Application().Chat.Tools {
		if c.Application().Chat.Tools[index].Name == "plan_model_processing" {
			tool = &c.Application().Chat.Tools[index]
		}
	}
	if tool == nil {
		t.Fatal("plan tool not found")
	}
	for _, capability := range []generation.Capability{"retexture", "format-conversion"} {
		if _, declared := tool.Processing.Steps[string(capability)]; !declared {
			t.Fatalf("plan steps lack %q", capability)
		}
		found := false
		for _, declared := range tool.Execution.Capabilities {
			if declared == capability {
				found = true
			}
		}
		if !found {
			t.Fatalf("execution capabilities lack %q", capability)
		}
	}
}

func containsText(haystack, needle string) bool {
	return len(needle) > 0 && (len(haystack) >= len(needle)) && (indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

func join(values []string) string {
	result := ""
	for _, value := range values {
		result += value + " "
	}
	return result
}
