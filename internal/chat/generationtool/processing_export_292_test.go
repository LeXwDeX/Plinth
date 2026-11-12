package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #292: the files a Tripo processing step exports are written at the texture
// size the session purpose allows, whichever way the Agent starts the step: one
// process_model call or a plan of steps. The size is a public parameter with the
// texture-size semantic, so the shared purpose budget (#129, #295) gives it the
// value; the Agent names none and gives no reason, and a brief without a purpose
// leaves the Offering's default of 8K.

func exportOptions(t *testing.T, brief map[string]any) chat.TurnOptions {
	t.Helper()
	if brief == nil {
		return chat.TurnOptions{}
	}
	return chat.TurnOptions{ProductionBrief: purposeBrief(t, brief)}
}

// exportBriefs are the briefs a game character, a prop and an unbounded display
// model are made under, with the size each gives an export.
var exportBriefs = map[string]struct {
	brief map[string]any
	want  string
}{
	"game character":            {map[string]any{"purpose": "game_character"}, "2k"},
	"game character on a phone": {map[string]any{"purpose": "game_character", "target_platform": "mobile"}, "2k"},
	"game character on a PC":    {map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, "4k"},
	"game prop":                 {map[string]any{"purpose": "game_prop"}, "2k"},
	"web and AR":                {map[string]any{"purpose": "web_ar"}, "2k"},
	"display":                   {map[string]any{"purpose": "render"}, "8k"},
	"a brief without a purpose": {map[string]any{"style": "写实"}, "8k"},
	"no brief":                  {nil, "8k"},
}

func TestProcessModelExportsTakeThePurposeTextureBudget(t *testing.T) {
	for name, test := range exportBriefs {
		t.Run(name, func(t *testing.T) {
			// A Tripo rigging for a game engine and a Tripo decimation: the two
			// steps of the generate, decimate, rig path of a game character.
			for _, step := range []struct{ capability, offering, arguments string }{
				{"rigging", "tripo/processing/rigging-v3", `{"step":"rigging","source_task_id":"tripo-model","offering_id":"tripo/processing/rigging-v3"}`},
				{"remesh", "tripo/geometry/decimate-triangle", `{"step":"remesh","source_task_id":"tripo-model","offering_id":"tripo/geometry/decimate-triangle"}`},
			} {
				h := newProcessingHarness(t, "auto", tripoNativeTasks)
				h.gateway.normalize = true
				result, _ := h.runWith("owner-a", "session-a", "process_model", step.arguments, nil, exportOptions(t, test.brief))
				if result.Suspend || h.gateway.submitCount != 1 || string(h.gateway.frozen[0].OfferingID) != step.offering {
					t.Fatalf("%s: the step was not submitted: %s submits=%d", step.offering, result.AgentContent, h.gateway.submitCount)
				}
				input := h.frozenInput(0)
				if input["export_texture_size"] != test.want {
					t.Fatalf("%s: export_texture_size = %v, want %s (frozen %v)", step.offering, input["export_texture_size"], test.want, input)
				}
				// The value is the purpose's budget, not an advised change: no
				// adjustment, no reason.
				frozen := h.gateway.frozen[0]
				if frozen.ParameterReasons["export_texture_size"] != "" || frozen.ParameterSources["export_texture_size"] != "" {
					t.Fatalf("%s: the size was frozen as an adjustment: %q / %q", step.offering,
						frozen.ParameterReasons["export_texture_size"], frozen.ParameterSources["export_texture_size"])
				}
			}
		})
	}
}

// A plan step freezes the same value as a single call, and the size the Agent
// names in a step is the step's own: the purpose does not overwrite it.
func TestPlanStepsExportAtThePurposeTextureBudget(t *testing.T) {
	// The rigging of Tripo takes a generated model and the decimation takes one
	// too, so each is a plan of its own.
	steps := map[string]string{
		"tripo/processing/rigging-v3":      `{"capability":"rigging","offering_id":"tripo/processing/rigging-v3","parameters":PARAMETERS}`,
		"tripo/geometry/decimate-triangle": `{"capability":"remesh","offering_id":"tripo/geometry/decimate-triangle","parameters":PARAMETERS}`,
	}
	plan := func(t *testing.T, brief map[string]any, offering, parameters string) map[string]any {
		t.Helper()
		h := newAgentPlanHarness(t, "manual", false)
		model := modelTask("tripo-model", "owner-a", "session-a", h.tripo, "text-to-3d", "glb")
		model.Offering.ID = "tripo/tripo-v3.1-full/text-to-3d"
		h.processingHarness.gateway.tasks[model.ID] = model
		command := h.command(`{"source_task_id":"tripo-model","steps":[` + strings.Replace(steps[offering], "PARAMETERS", parameters, 1) + `]}`)
		command.Options = exportOptions(t, brief)
		result, err := h.executor.Execute(context.Background(), command, nil)
		if err != nil || result.Error != "" {
			t.Fatalf("proposal failed: %v %+v", err, result)
		}
		planned := h.get(h.content(result)["plan_id"].(string))
		var input map[string]any
		if len(planned.Steps) != 1 || json.Unmarshal(planned.Steps[0].Input, &input) != nil {
			t.Fatalf("plan = %+v", planned)
		}
		return input
	}
	for name, test := range exportBriefs {
		t.Run(name, func(t *testing.T) {
			for offering := range steps {
				if input := plan(t, test.brief, offering, `{}`); input["export_texture_size"] != test.want {
					t.Fatalf("%s: export_texture_size = %v, want %s (input %v)", offering, input["export_texture_size"], test.want, input)
				}
			}
		})
	}
	// The Agent's own size is kept, over the budget too.
	mobile := map[string]any{"purpose": "game_character", "target_platform": "mobile"}
	for offering := range steps {
		if input := plan(t, mobile, offering, `{"export_texture_size":"4k"}`); input["export_texture_size"] != "4k" {
			t.Fatalf("%s: the Agent's 4k was replaced by %v", offering, input["export_texture_size"])
		}
	}
}

// The frozen step is what the adapter exports: a size the purpose gave is in the
// input the Grant is bound to, so the approval card shows the same step the
// upstream receives.
func TestExportSizeIsFrozenWithTheStepNotAddedLater(t *testing.T) {
	h := newProcessingHarness(t, "auto", tripoNativeTasks)
	h.gateway.normalize = true
	h.runWith("owner-a", "session-a", "process_model",
		`{"step":"rigging","source_task_id":"tripo-model","offering_id":"tripo/processing/rigging-v3"}`, nil,
		exportOptions(t, map[string]any{"purpose": "game_character", "target_engine": "unreal"}))
	if len(h.gateway.frozen) != 1 {
		t.Fatalf("frozen = %d", len(h.gateway.frozen))
	}
	input := h.frozenInput(0)
	// The engine picks the skeleton naming and the purpose the export size, by the
	// same resolver and in the same step.
	if input["spec"] != "unreal" || input["export_texture_size"] != "2k" {
		t.Fatalf("frozen rigging = %v", input)
	}
	if h.gateway.frozen[0].OfferingID != "tripo/processing/rigging-v3" {
		t.Fatalf("frozen Offering = %s", h.gateway.frozen[0].OfferingID)
	}
}
