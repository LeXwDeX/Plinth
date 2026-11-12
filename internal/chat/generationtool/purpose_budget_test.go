package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
)

// #129: the production brief's purpose bounds the triangle count and the
// texture size of every 3D generation and processing step, not only of the
// Offerings it names overrides for. The shipped game_prop purpose allows 20000
// triangles and 2048-pixel textures.

const budgetNoteKey = "purpose_budget_note"

func purposeBrief(t *testing.T, fields map[string]any) chat.ProductionBrief {
	t.Helper()
	brief := chat.ProductionBrief{}
	for name, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = encoded
	}
	return brief
}

// runLocked3D runs create_model_3d on the model the person locked in the
// session, with the given brief and the Agent's explicit parameter values.
func (h *processingHarness) runLocked3D(call string, brief chat.ProductionBrief, explicit map[string]json.RawMessage) chat.ToolResult {
	h.t.Helper()
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:               agents.ToolCall{ID: call, Name: "create_model_3d", Arguments: []byte(`{"prompt":"a wooden crate"}`)},
		Tool:               h.tool("create_model_3d"),
		ExplicitParameters: explicit,
		Options: chat.TurnOptions{CreativeDomain: "model-3d", ExplicitModel3D: "tripo-v3.1-full",
			ProductionBrief: brief}}
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil {
		h.t.Fatalf("execute: %v", err)
	}
	return result
}

// The dev-test case: the person locked Tripo v3.1, which the game_prop purpose
// neither recommends nor overrides. Its face limit used to default to two
// million; the budget now bounds it, with the purpose's provenance.
func TestLockedModelOutsideThePurposeTakesItsTriangleBudget(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result := h.runLocked3D("call-locked", purposeBrief(t, map[string]any{"purpose": "game_prop"}), nil)
	if len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "tripo/tripo-v3.1-full/text-to-3d" {
		t.Fatalf("frozen = %+v, result = %+v", h.gateway.frozen, result)
	}
	if limit := h.frozenInput(0)["face_limit"]; limit != float64(20000) {
		t.Fatalf("face_limit = %v, want the game_prop budget 20000", limit)
	}
	frozen := h.gateway.frozen[0]
	if frozen.ParameterSources["face_limit"] != "profile:game_prop" || frozen.ParameterReasons["face_limit"] == "" {
		t.Fatalf("face_limit provenance = %q / %q, want the purpose", frozen.ParameterSources["face_limit"], frozen.ParameterReasons["face_limit"])
	}
	if _, reported := h.content(result)[budgetNoteKey]; reported {
		t.Fatalf("a value within the budget was reported: %s", result.AgentContent)
	}
}

// The brief's own triangle budget replaces the purpose's maximum.
func TestBriefTriangleBudgetBoundsTheFaceLimit(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.runLocked3D("call-brief", purposeBrief(t, map[string]any{"purpose": "game_prop", "triangle_budget": 5000}), nil)
	if limit := h.frozenInput(0)["face_limit"]; limit != float64(5000) {
		t.Fatalf("face_limit = %v, want the brief's 5000", limit)
	}
}

// Without a brief nothing bounds the step: the Offering default applies.
func TestNoBriefLeavesTheOfferingDefault(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result := h.runLocked3D("call-free", nil, nil)
	if _, set := h.frozenInput(0)["face_limit"]; set {
		t.Fatalf("face_limit was set without a purpose: %v", h.frozenInput(0))
	}
	if _, reported := h.content(result)[budgetNoteKey]; reported {
		t.Fatalf("a step without a purpose was reported: %s", result.AgentContent)
	}
}

// A value the Agent chose is kept even over the budget; the result says which
// value is over it and what the purpose allows, so the Agent can tell the
// person.
func TestExplicitValueOverTheBudgetIsKeptAndReported(t *testing.T) {
	// auto submits and reports the finished step; manual stops at the
	// approval card, whose waiting result carries the same note.
	for _, test := range []struct{ mode, status string }{{"auto", ""}, {"manual", "awaiting_approval"}} {
		t.Run(test.mode, func(t *testing.T) {
			h := newProcessingHarness(t, test.mode, meshyCatalogTasks)
			// The Agent set the face limit with set_params: its remembered setting.
			h.config.OfferingParameters = func(_, _, offering string) map[string]json.RawMessage {
				if offering != "tripo/tripo-v3.1-full/text-to-3d" {
					return nil
				}
				return map[string]json.RawMessage{"face_limit": json.RawMessage(`2000000`)}
			}
			executor, err := generationtool.New(h.config)
			if err != nil {
				t.Fatal(err)
			}
			h.executor = executor
			result := h.runLocked3D("call-explicit", purposeBrief(t, map[string]any{"purpose": "game_prop"}), nil)
			if limit := h.frozenInput(0)["face_limit"]; limit != float64(2000000) {
				t.Fatalf("face_limit = %v, want the Agent's 2000000", limit)
			}
			if source := h.gateway.frozen[0].ParameterSources["face_limit"]; source != "" {
				t.Fatalf("the Agent's value was credited to %q", source)
			}
			content := h.content(result)
			if status, _ := content["status"].(string); status != test.status {
				t.Fatalf("status = %q, want %q: %s", status, test.status, result.AgentContent)
			}
			note, _ := content[budgetNoteKey].(string)
			for _, want := range []string{"2000000", "20000", "游戏/实时·道具与场景件"} {
				if !strings.Contains(note, want) {
					t.Fatalf("budget note %q does not mention %q", note, want)
				}
			}
		})
	}
}

// A model the Agent names takes the purpose's values for it. Meshy 7.1 is the
// last of the game purposes' recommendations since #276, so naming it gets the
// purpose's one-step remesh (a triangle prop at 8000 faces with 2K textures, all
// inside the budget) rather than the 300000-face default or the budget's own
// fallback of the purpose's upper bound (20000), which this test pinned before.
func TestNamedModelTakesTheTextureAndTriangleBudget(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-named", Name: "create_model_3d",
			Arguments: []byte(`{"prompt":"a wooden crate","model":"meshy-7.1"}`)},
		Tool: h.tool("create_model_3d"),
		Options: chat.TurnOptions{CreativeDomain: "model-3d",
			ProductionBrief: purposeBrief(t, map[string]any{"purpose": "game_prop"})}}
	if _, err := h.executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "meshy/meshy-7.1/text-to-3d" {
		t.Fatalf("frozen = %+v", h.gateway.frozen)
	}
	input := h.frozenInput(0)
	if input["texture_resolution"] != "2k" || input["target_polycount"] != float64(8000) || input["topology"] != "triangle" {
		t.Fatalf("frozen = %v, want 2k textures and a triangle remesh to 8000 faces", input)
	}
	for _, name := range []string{"texture_resolution", "target_polycount"} {
		if source := h.gateway.frozen[0].ParameterSources[name]; source != "profile:game_prop" {
			t.Fatalf("%s source = %q", name, source)
		}
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, normalized, err := product.Normalize("text-to-3d", h.gateway.frozen[0].OfferingID, h.gateway.frozen[0].Input)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(normalized, &values); err != nil || values["should_remesh"] != true {
		t.Fatalf("normalized = %s, want the remesh that makes the face target effective", normalized)
	}
}

// The dev-test retexture asked Meshy for 8K textures on a game prop. A
// retexture the Agent leaves open now takes the purpose's limit; an 8K it asks
// for is kept and reported.
func TestRetextureTakesThePurposeTextureLimit(t *testing.T) {
	options := chat.TurnOptions{ProductionBrief: purposeBrief(t, map[string]any{"purpose": "game_prop"})}
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"mossy stone"}`, nil, options)
	if texture := h.frozenInput(0)["texture_resolution"]; texture != "2k" {
		t.Fatalf("texture_resolution = %v, want 2k", texture)
	}
	if _, reported := h.content(result)[budgetNoteKey]; reported {
		t.Fatalf("a texture within the budget was reported: %s", result.AgentContent)
	}

	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ = h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"mossy stone","resolution":"8k"}`, nil, options)
	if texture := h.frozenInput(0)["texture_resolution"]; texture != "8k" {
		t.Fatalf("texture_resolution = %v, want the Agent's 8k", texture)
	}
	note, _ := h.content(result)[budgetNoteKey].(string)
	if !strings.Contains(note, "8K") || !strings.Contains(note, "2048") {
		t.Fatalf("budget note = %q, want the 8K value and the 2048 limit", note)
	}
}

// A Meshy remesh face is a quad unless the step says otherwise: the Schema's
// output estimate counts it as two triangles, so the budget halves the target.
func TestRemeshTargetCountsQuadsAgainstTheTriangleBudget(t *testing.T) {
	options := chat.TurnOptions{ProductionBrief: purposeBrief(t, map[string]any{"purpose": "game_prop"})}
	for _, test := range []struct {
		arguments string
		want      float64
	}{
		{`{"step":"remesh","source_task_id":"model-1"}`, 10000},
		{`{"step":"remesh","source_task_id":"model-1","topology":"triangle"}`, 20000},
	} {
		h := newProcessingHarness(t, "auto", meshyCatalogTasks)
		h.runWith("owner-a", "session-a", "process_model", test.arguments, nil, options)
		if len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "meshy/processing/remesh" {
			t.Fatalf("%s: frozen = %+v", test.arguments, h.gateway.frozen)
		}
		if target := h.frozenInput(0)["target_polycount"]; target != test.want {
			t.Fatalf("%s: target_polycount = %v, want %v", test.arguments, target, test.want)
		}
	}
}

// A processing plan freezes the budget's values into its steps and says which
// value the Agent planned over the budget.
func TestProcessingPlanStepsFollowThePurposeBudget(t *testing.T) {
	brief := purposeBrief(t, map[string]any{"purpose": "game_prop"})
	propose := func(h *agentPlanHarness, arguments string) map[string]any {
		t.Helper()
		command := h.command(arguments)
		command.Options.ProductionBrief = brief
		result, err := h.executor.Execute(context.Background(), command, nil)
		if err != nil {
			t.Fatal(err)
		}
		return h.content(result)
	}
	h := newAgentPlanHarness(t, "auto", false)
	content := propose(h, `{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{}}]}`)
	plan := h.get(content["plan_id"].(string))
	var input map[string]any
	if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["target_polycount"] != float64(10000) {
		t.Fatalf("planned remesh = %v, want 10000 quads within 20000 triangles", input)
	}
	if _, reported := content[budgetNoteKey]; reported {
		t.Fatalf("a plan within the budget was reported: %v", content)
	}

	h = newAgentPlanHarness(t, "auto", false)
	content = propose(h, `{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"target_polycount":300000}}]}`)
	note, _ := content[budgetNoteKey].(string)
	if !strings.Contains(note, "300000") || !strings.Contains(note, "600000") {
		t.Fatalf("plan budget note = %q, want the planned 300000 quads (600000 triangles)", note)
	}
}

// A production plan step names no parameters: it is checked before approval
// with the values it would run with (#129). Values the purpose budget can
// bound fit and say nothing; a value the budget cannot bring within it (a
// brief budget below the Offering's minimum face limit, so the step runs at
// that minimum) is named on the plan.
func TestPlanStepPurposeBudgetNoteNamesValuesTheBudgetCannotBound(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, found := product.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !found {
		t.Fatal("missing the shipped Tripo text-to-3d Offering")
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-plan", Name: "propose_production_plan", Arguments: []byte(`{}`)}}
	if note := h.executor.PlanStepPurposeBudgetNote(command, offering); note != "" {
		t.Fatalf("a step without a purpose was noted: %q", note)
	}
	command.Options.ProductionBrief = purposeBrief(t, map[string]any{"purpose": "game_prop"})
	if note := h.executor.PlanStepPurposeBudgetNote(command, offering); note != "" {
		t.Fatalf("a face limit the purpose bounds was noted: %q", note)
	}
	command.Options.ProductionBrief = purposeBrief(t, map[string]any{"purpose": "game_prop", "triangle_budget": 500})
	note := h.executor.PlanStepPurposeBudgetNote(command, offering)
	for _, want := range []string{"1000", "500"} {
		if !strings.Contains(note, want) {
			t.Fatalf("plan step note %q does not mention %q", note, want)
		}
	}
}
