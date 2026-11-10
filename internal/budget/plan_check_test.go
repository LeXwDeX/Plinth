package budget_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

func TestPlanStepCheckIsOptionalAndSurvivesJSON(t *testing.T) {
	var legacy budget.Plan
	if err := json.Unmarshal([]byte(`{"title":"p","steps":[{"capability":"text-to-3d"}]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Steps[0].Check != "" {
		t.Fatalf("legacy step check = %q", legacy.Steps[0].Check)
	}
	encoded, err := json.Marshal(legacy.Steps[0])
	if err != nil || strings.Contains(string(encoded), "check") {
		t.Fatalf("legacy step encoded = %s err=%v", encoded, err)
	}
	var checked budget.PlanStep
	if err := json.Unmarshal([]byte(`{"capability":"text-to-3d","check":"self_check"}`), &checked); err != nil {
		t.Fatal(err)
	}
	if checked.Check != "self_check" {
		t.Fatalf("check = %q", checked.Check)
	}
	encoded, err = json.Marshal(checked)
	if err != nil || !strings.Contains(string(encoded), `"check":"self_check"`) {
		t.Fatalf("checked step encoded = %s err=%v", encoded, err)
	}
}

func TestPlanPromptLineNamesTheCheckpoint(t *testing.T) {
	plan := budget.Plan{Title: "p", Steps: []budget.PlanStep{
		{Capability: "text-to-image", Estimate: budget.Legacy(5), Check: "self_check"},
		{Capability: "image-to-3d", Estimate: budget.Legacy(20)},
	}}
	line := plan.PromptLine(budget.AmountFormat{LegacyLabel: "积分"})
	if !strings.Contains(line, "检查点 self_check") {
		t.Fatalf("line = %q", line)
	}
	if strings.Count(line, "检查点") != 1 {
		t.Fatalf("only the declared step should name its checkpoint: %q", line)
	}
}
