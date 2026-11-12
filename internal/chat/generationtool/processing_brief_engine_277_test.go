package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #277: the production brief's target engine picks the default skeleton naming
// (Tripo) or template (Meshy) of a rigging step. The Schema declares the
// mapping (brief.parameter_defaults); a value the Agent or the person set for
// the step wins, and a skeleton the naming does not apply to keeps Tripo naming.

func engineBrief(engine string) chat.TurnOptions {
	if engine == "" {
		return chat.TurnOptions{}
	}
	encoded, _ := json.Marshal(engine)
	return chat.TurnOptions{ProductionBrief: chat.ProductionBrief{"target_engine": encoded}}
}

func tripoNativeTasks(h *processingHarness) []generation.Task {
	model := modelTask("tripo-model", "owner-a", "session-a", h.tripo, "text-to-3d", "glb")
	model.Offering.ID = "tripo/tripo-v3.1-full/text-to-3d"
	return append(meshyCatalogTasks(h), model)
}

func TestBriefTargetEnginePicksTheMeshySkeletonTemplate(t *testing.T) {
	for name, tc := range map[string]struct {
		engine, arguments, want string
	}{
		"unreal":                        {"unreal", `{"step":"rigging","source_task_id":"model-1"}`, "ue5_mannequin"},
		"unity":                         {"unity", `{"step":"rigging","source_task_id":"model-1"}`, "unity_humanoid"},
		"godot has no engine skeleton":  {"godot", `{"step":"rigging","source_task_id":"model-1"}`, "meshyrig"},
		"web has no engine skeleton":    {"web", `{"step":"rigging","source_task_id":"model-1"}`, "meshyrig"},
		"another engine":                {"other", `{"step":"rigging","source_task_id":"model-1"}`, "meshyrig"},
		"no brief":                      {"", `{"step":"rigging","source_task_id":"model-1"}`, "meshyrig"},
		"the Agent's own template wins": {"unreal", `{"step":"rigging","source_task_id":"model-1","skeleton_template":"mixamo"}`, "mixamo"},
		"the default is a choice too":   {"unreal", `{"step":"rigging","source_task_id":"model-1","skeleton_template":"meshyrig"}`, "meshyrig"},
		"with the other options":        {"unreal", `{"step":"rigging","source_task_id":"model-1","height_meters":1.8,"include_fingers":true}`, "ue5_mannequin"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			h.gateway.normalize = true
			result, _ := h.runWith("owner-a", "session-a", "process_model", tc.arguments, nil, engineBrief(tc.engine))
			if result.Suspend || h.gateway.submitCount != 1 || h.gateway.frozen[0].Capability != "rigging" {
				t.Fatalf("rigging was not submitted: %+v submits=%d", result, h.gateway.submitCount)
			}
			if got := h.frozenInput(0)["skeleton_template"]; got != tc.want {
				t.Fatalf("skeleton_template = %v, want %s", got, tc.want)
			}
			// Finger bones are the person's call (#294): Meshy refuses them for a
			// quad-topology model, which the game character purposes now generate,
			// so no engine default turns them on.
			if !strings.Contains(tc.arguments, "include_fingers") {
				if fingers := h.frozenInput(0)["include_fingers"]; fingers == true {
					t.Fatalf("include_fingers = %v although nobody asked for finger bones", fingers)
				}
			}
		})
	}
	// Finger bones travel with the template.
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	h.runWith("owner-a", "session-a", "process_model", `{"step":"rigging","source_task_id":"model-1","include_fingers":true}`, nil, engineBrief("unreal"))
	if input := h.frozenInput(0); input["include_fingers"] != true || input["skeleton_template"] != "ue5_mannequin" {
		t.Fatalf("frozen rigging = %v", input)
	}
}

func TestBriefTargetEnginePicksTheTripoSkeletonNaming(t *testing.T) {
	const v3 = "tripo/processing/rigging-v3"
	for name, tc := range map[string]struct {
		engine, arguments, wantNaming, wantType string
	}{
		"unreal":                          {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `"}`, "unreal", "biped"},
		"unity":                           {"unity", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `"}`, "unity", "biped"},
		"godot":                           {"godot", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `"}`, "tripo", "biped"},
		"no brief":                        {"", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `"}`, "tripo", "biped"},
		"the Agent's own naming wins":     {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `","spec":"mixamo"}`, "mixamo", "biped"},
		"the default naming is a choice":  {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `","spec":"tripo"}`, "tripo", "biped"},
		"a quadruped keeps Tripo naming":  {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `","rig_type":"quadruped"}`, "tripo", "quadruped"},
		"a creature chosen as a rig type": {"unity", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `","rig_type":"aquatic"}`, "tripo", "aquatic"},
		"a biped named explicitly":        {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"` + v3 + `","rig_type":"biped"}`, "unreal", "biped"},
		"the v2.5 service has no naming":  {"unreal", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"tripo/processing/rigging","rig_type":"quadruped"}`, "tripo", "quadruped"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", tripoNativeTasks)
			h.gateway.normalize = true
			result, _ := h.runWith("owner-a", "session-a", "process_model", tc.arguments, nil, engineBrief(tc.engine))
			if result.Suspend || h.gateway.submitCount != 1 || h.gateway.frozen[0].Capability != "rigging" {
				t.Fatalf("rigging was not submitted: %s suspend=%v submits=%d", result.AgentContent, result.Suspend, h.gateway.submitCount)
			}
			input := h.frozenInput(0)
			if input["spec"] != tc.wantNaming || input["rig_type"] != tc.wantType {
				t.Fatalf("frozen rigging = %v, want naming %s and type %s", input, tc.wantNaming, tc.wantType)
			}
		})
	}
}

// A plan step names no parameters unless the Agent gives them; the engine fills
// the naming the same way, and a skeleton type given in the step still decides
// whether the naming applies.
func TestBriefTargetEngineFillsPlanSteps(t *testing.T) {
	step := func(parameters string) string {
		return `{"source_task_id":"tripo-model","steps":[{"capability":"rigging","offering_id":"tripo/processing/rigging-v3","parameters":` + parameters + `}]}`
	}
	for name, tc := range map[string]struct{ engine, parameters, wantNaming, wantType string }{
		"the engine names the biped":      {"unreal", `{}`, "unreal", "biped"},
		"unity names it too":              {"unity", `{}`, "unity", "biped"},
		"godot names nothing":             {"godot", `{}`, "tripo", "biped"},
		"no brief names nothing":          {"", `{}`, "tripo", "biped"},
		"the step's own naming wins":      {"unreal", `{"spec":"vrm"}`, "vrm", "biped"},
		"a creature in the plan":          {"unreal", `{"rig_type":"hexapod"}`, "tripo", "hexapod"},
		"a creature with a stale naming":  {"unreal", `{"rig_type":"quadruped","spec":"unity"}`, "tripo", "quadruped"},
		"a biped named by the step alone": {"", `{"rig_type":"biped","spec":"unity"}`, "unity", "biped"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAgentPlanHarness(t, "manual", false)
			model := modelTask("tripo-model", "owner-a", "session-a", h.tripo, "text-to-3d", "glb")
			model.Offering.ID = "tripo/tripo-v3.1-full/text-to-3d"
			h.processingHarness.gateway.tasks[model.ID] = model
			command := h.command(step(tc.parameters))
			command.Options = engineBrief(tc.engine)
			result, err := h.executor.Execute(context.Background(), command, nil)
			if err != nil || result.Error != "" {
				t.Fatalf("proposal failed: %v %+v", err, result)
			}
			plan := h.get(h.content(result)["plan_id"].(string))
			var input map[string]any
			if len(plan.Steps) != 1 || json.Unmarshal(plan.Steps[0].Input, &input) != nil {
				t.Fatalf("plan = %+v", plan)
			}
			if input["spec"] != tc.wantNaming || input["rig_type"] != tc.wantType {
				t.Fatalf("planned rigging = %v, want naming %s and type %s", input, tc.wantNaming, tc.wantType)
			}
		})
	}
}

// The Agent chooses a Tripo animation preset through the tool's own argument
// (Meshy actions stay on action_id), and the choice reaches the frozen step.
func TestProcessModelPassesATripoAnimationPreset(t *testing.T) {
	seed := func(h *processingHarness) []generation.Task {
		rigging, _ := h.gateway.product.Get("tripo/processing/rigging-v3")
		rig := modelTask("tripo-rig", "owner-a", "session-a", h.tripo, "rigging", "glb", "fbx")
		rig.Offering = rigging
		return []generation.Task{rig}
	}
	for _, preset := range []string{"preset:run", "preset:quadruped:walk"} {
		h := newProcessingHarness(t, "auto", seed)
		h.gateway.normalize = true
		result, _ := h.run("process_model", `{"step":"animation","source_task_id":"tripo-rig","animation_action":"`+preset+`"}`)
		if result.Suspend || h.gateway.submitCount != 1 || h.gateway.frozen[0].Capability != "animation" {
			t.Fatalf("%s: animation was not submitted: %s", preset, result.AgentContent)
		}
		if got := h.frozenInput(0)["animation_action"]; got != preset {
			t.Fatalf("animation_action = %v, want %s", got, preset)
		}
	}
}
