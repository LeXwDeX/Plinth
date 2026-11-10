package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func planCommandWithBrief(arguments string, brief chat.ProductionBrief) chat.ToolCommand {
	command := planCommand(arguments)
	command.Options.ProductionBrief = brief
	return command
}

func propose(t *testing.T, service *chat.Service, store *recordingProductionPlans, command chat.ToolCommand) (budget.Plan, map[string]any) {
	t.Helper()
	result, err := service.ProposeProductionPlan(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if json.Unmarshal([]byte(result.AgentContent), &content) != nil {
		t.Fatalf("result = %s", result.AgentContent)
	}
	if content["success"] == false {
		return budget.Plan{}, content
	}
	if len(store.plans) == 0 {
		t.Fatal("plan was not stored")
	}
	return store.plans[len(store.plans)-1], content
}

func TestProposeProductionPlanBuildsAcceptanceFromPurposeAndBrief(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`)}
	command := planCommandWithBrief(`{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成角色"}],"budgets":[{"unit":"legacy.credits","amount":20}]}`, brief)
	plan, content := propose(t, service, store, command)
	if len(plan.Acceptance) == 0 {
		t.Fatalf("acceptance = %+v", plan.Acceptance)
	}
	var joined []string
	for _, criterion := range plan.Acceptance {
		joined = append(joined, criterion.ID+"="+criterion.Statement)
	}
	text := strings.Join(joined, " | ")
	if !strings.Contains(text, "purpose=") || !strings.Contains(text, "游戏/实时·角色") || !strings.Contains(text, "triangles=") ||
		!strings.Contains(text, "60000") || !strings.Contains(text, "uv=") {
		t.Fatalf("acceptance criteria = %s", text)
	}
	if content["report_instruction"] == "" {
		t.Fatal("the plan result must carry the report instruction")
	}
}

// #251 fix: the acceptance list only names conditions the server actually
// evaluates for the purpose. A boolean check the purpose does not require is
// unknown (never pass), and a threshold without its bound would freeze a
// literal placeholder as the standard the person approves.
func TestAcceptanceOmitsUnrequiredAndUnboundedChecks(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`)}
	command := planCommandWithBrief(`{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成角色"}],"budgets":[{"unit":"legacy.credits","amount":20}]}`, brief)
	plan, _ := propose(t, service, store, command)
	var statements []string
	for _, criterion := range plan.Acceptance {
		statements = append(statements, criterion.ID+"="+criterion.Statement)
	}
	text := strings.Join(statements, " | ")
	if strings.Contains(text, "需要水密") || strings.Contains(text, "需要骨骼") {
		t.Fatalf("unrequired checks were promised as acceptance: %s", text)
	}
	for _, wanted := range []string{"triangles=", "uv=", "plausible_scale=", "0.3–5"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("required acceptance %q missing from %s", wanted, text)
		}
	}

	// The render purpose declares no scale range: the scale criterion has no
	// bound and must be absent instead of reading "{minimum}–{maximum}".
	store = &recordingProductionPlans{}
	service = proposalService(t, store, shippedBudgetSchema(t))
	brief = chat.ProductionBrief{"purpose": json.RawMessage(`"render"`)}
	command = planCommandWithBrief(`{"title":"展示","steps":[{"capability":"image-to-3d","purpose":"生成展示模型"}],"budgets":[{"unit":"legacy.credits","amount":20}]}`, brief)
	plan, _ = propose(t, service, store, command)
	var render []string
	for _, criterion := range plan.Acceptance {
		render = append(render, criterion.ID+"="+criterion.Statement)
	}
	renderText := strings.Join(render, " | ")
	if strings.Contains(renderText, "{minimum}") || strings.Contains(renderText, "{maximum}") {
		t.Fatalf("an unfilled threshold was frozen: %s", renderText)
	}
	if strings.Contains(renderText, "plausible_scale=") {
		t.Fatalf("a check without a purpose bound was promised: %s", renderText)
	}
	if !strings.Contains(renderText, "triangles=") {
		t.Fatalf("bounded acceptance criteria are missing: %s", renderText)
	}
}

func TestProposeProductionPlanFreezesCheckpointThresholdsAndContingencies(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`)}
	arguments := `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成角色"},{"capability":"remesh","purpose":"减面"}],` +
		`"budgets":[{"unit":"legacy.credits","amount":20}],` +
		`"checkpoints":[{"after_capability":"remesh","checks":["triangles","pivot_bottom_center"]}],` +
		`"contingencies":[{"id":"pivot","trigger":["pivot_bottom_center"],"step":{"capability":"resize","purpose":"把原点移到底部中心"}}]}`
	plan, content := propose(t, service, store, planCommandWithBrief(arguments, brief))
	if len(plan.Checkpoints) != 1 || plan.Checkpoints[0].AfterCapability != "remesh" ||
		len(plan.Checkpoints[0].Checks) != 2 {
		t.Fatalf("checkpoints = %+v", plan.Checkpoints)
	}
	triangles := plan.Checkpoints[0].Checks[0]
	if triangles.Check != "triangles" || triangles.Maximum == nil || *triangles.Maximum != 60000 {
		t.Fatalf("triangles threshold = %+v", triangles)
	}
	pivot := plan.Checkpoints[0].Checks[1]
	if pivot.Check != "pivot_bottom_center" || pivot.MaximumRatio == nil || *pivot.MaximumRatio <= 0 {
		t.Fatalf("pivot threshold = %+v", pivot)
	}
	if len(plan.Contingencies) != 1 || plan.Contingencies[0].ID != "pivot" ||
		plan.Contingencies[0].Step.OfferingID != "resize-default" {
		t.Fatalf("contingency = %+v", plan.Contingencies)
	}
	// The contingency's estimate raises the worst-case limit: 7 + 6 + 3.
	if totals := plan.EstimatedTotals(); len(totals) != 1 || totals[0].Micros != budget.Legacy(16).Micros {
		t.Fatalf("estimated totals = %+v", totals)
	}
	if content["contingency_totals"] == nil || content["step_totals"] == nil {
		t.Fatalf("plan result must split step and contingency totals: %v", content)
	}
}

func TestProposeProductionPlanRefusesABudgetBelowStepsPlusContingencies(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	arguments := `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成角色"},{"capability":"remesh","purpose":"减面"}],` +
		`"budgets":[{"unit":"legacy.credits","amount":15}],` +
		`"checkpoints":[{"after_capability":"remesh","checks":["triangles"]}],` +
		`"contingencies":[{"id":"fix","trigger":["triangles"],"step":{"capability":"remesh","purpose":"再减面"}}]}`
	_, content := propose(t, service, store, planCommand(arguments))
	if content["success"] != false || !strings.Contains(content["error"].(string), "19 legacy.credits") {
		t.Fatalf("over-budget refusal = %v", content)
	}
	if len(store.created) != 0 {
		t.Fatalf("a refused plan was stored: %+v", store.created)
	}
}

func TestProposeProductionPlanRefusesUnknownOrOutsideDeclarations(t *testing.T) {
	cases := map[string]string{
		"checkpoint capability not in plan": `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成"}],` +
			`"budgets":[{"unit":"legacy.credits","amount":20}],` +
			`"checkpoints":[{"after_capability":"remesh","checks":["triangles"]}]}`,
		"unknown check": `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成"},{"capability":"remesh","purpose":"减面"}],` +
			`"budgets":[{"unit":"legacy.credits","amount":20}],` +
			`"checkpoints":[{"after_capability":"remesh","checks":["style"]}]}`,
		"contingency outside the fixes": `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成"}],` +
			`"budgets":[{"unit":"legacy.credits","amount":20}],` +
			`"contingencies":[{"id":"tex","trigger":["triangles"],"step":{"capability":"retexture","purpose":"换贴图"}}]}`,
		"contingency trigger not declared": `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成"}],` +
			`"budgets":[{"unit":"legacy.credits","amount":20}],` +
			`"contingencies":[{"id":"fix","trigger":["pivot_bottom_center"],"step":{"capability":"resize","purpose":"移原点"}}]}`,
	}
	for name, arguments := range cases {
		store := &recordingProductionPlans{}
		service := proposalService(t, store, shippedBudgetSchema(t))
		_, content := propose(t, service, store, planCommand(arguments))
		if content["success"] != false {
			t.Fatalf("%s was accepted: %v", name, content)
		}
		if len(store.created) != 0 {
			t.Fatalf("%s stored a plan", name)
		}
	}
}

func TestProposeProductionPlanKeepsAHistoricalPlanWithoutNewFields(t *testing.T) {
	// A plan stored before #251 has no acceptance, checkpoints or
	// contingencies; the prompt and card render it without change.
	plan := budget.Plan{Title: "旧计划", Steps: []budget.PlanStep{{Capability: "image-to-3d", Purpose: "生成", Estimate: budget.Legacy(7)}}}
	if plan.AcceptanceLine() != "" || plan.CheckpointLine() != "" || plan.ContingencyLine(budget.AmountFormat{}) != "" {
		t.Fatal("a historical plan must render no new sections")
	}
	if totals := plan.EstimatedTotals(); len(totals) != 1 || totals[0].Micros != budget.Legacy(7).Micros {
		t.Fatalf("historical totals = %+v", totals)
	}
}
