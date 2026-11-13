package generationtool_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #279: the game purposes recommend Rodin Gen-2.5 and carry its Quad overrides.
// The person locked Rodin; the purpose layer must freeze the Quad topology, the
// face count inside the purpose's triangle budget, the texture mode inside its
// texture budget and the game switches, with the purpose as the provenance of
// the advised ones, and a purpose without an override must still bound the face
// count and the texture mode by its budget (the polycount and texture-size
// semantics of the parameters).

// runLockedRodin runs create_model_3d on the Rodin Gen-2.5 the person locked.
func (h *processingHarness) runLockedRodin(call string, brief chat.ProductionBrief) chat.ToolResult {
	h.t.Helper()
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: call, Name: "create_model_3d", Arguments: []byte(`{"prompt":"a wooden crate"}`)},
		Tool: h.tool("create_model_3d"),
		Options: chat.TurnOptions{CreativeDomain: "model-3d", ExplicitModel3D: "rodin-gen-2.5",
			ProductionBrief: brief}}
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil {
		h.t.Fatalf("execute: %v", err)
	}
	return result
}

// A Rodin Quad face is about 2.18 triangles in the delivered GLB (one real run,
// 2026-11-13, #294 follow-up: quality_override 10000 gave about 21,800 triangles);
// the rule reads it as 2.2, as internal/catalog's rodinQuadTrianglesPerFacePercent.
const rodinQuadTrianglesPerFacePercent = 220

func TestRodinGamePurposesFreezeQuadWithinTheBudget(t *testing.T) {
	const rodin = "hyper3d/rodin-gen-2.5/text-to-3d"
	for _, test := range []struct {
		name    string
		brief   map[string]any
		want    map[string]any
		sources map[string]string
		// triangles is the triangle budget the purpose resolves to on the brief's
		// platform (the purpose's own without one); set for the Quad cases.
		triangles int
	}{
		// The advised parameters (topology, count and texture mode) keep the purpose as their
		// provenance and reason; the plain switches are applied as values only
		// (a reason on a non-advised parameter would make the freeze refuse).
		{
			name:  "a character is Quad, T/A posed and normal baked",
			brief: map[string]any{"purpose": "game_character"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(8000), "texture_mode": "high",
				"TAPose": true, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_character", "quality_override": "profile:game_character",
				"texture_mode": "profile:game_character",
			},
			triangles: 60000,
		},
		{
			// The phone class budget is 20,000 triangles: 8000 quads are about 17,600.
			name:  "a character on a phone keeps the Quad count inside the phone budget",
			brief: map[string]any{"purpose": "game_character", "target_platform": "mobile"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(8000), "texture_mode": "high",
				"TAPose": true, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_character", "quality_override": "profile:game_character",
				"texture_mode": "profile:game_character",
			},
			triangles: 20000,
		},
		{
			// PC and console keep the count they had before the real run: about 22,000
			// triangles of a 100,000 budget, and the 2K mode is still the highest one
			// inside the 4096 texture budget.
			name:  "a character on PC and console keeps its larger count",
			brief: map[string]any{"purpose": "game_character", "target_platform": "pc_console"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(10000), "texture_mode": "high",
				"TAPose": true, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_character", "quality_override": "profile:game_character",
				"texture_mode": "profile:game_character",
			},
			triangles: 100000,
		},
		{
			name:  "a prop is Quad and normal baked, without a pose",
			brief: map[string]any{"purpose": "game_prop"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(3200), "texture_mode": "high",
				"TAPose": false, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_prop", "quality_override": "profile:game_prop", "texture_mode": "profile:game_prop",
			},
			triangles: 20000,
		},
		{
			// The phone class budget of a prop is 8,000 triangles: 3200 quads are about 7,040.
			name:  "a prop on a phone keeps the Quad count inside the phone budget",
			brief: map[string]any{"purpose": "game_prop", "target_platform": "mobile"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(3200), "texture_mode": "high",
				"TAPose": false, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_prop", "quality_override": "profile:game_prop", "texture_mode": "profile:game_prop",
			},
			triangles: 8000,
		},
		{
			name:  "a prop on PC and console keeps its larger count",
			brief: map[string]any{"purpose": "game_prop", "target_platform": "pc_console"},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(4000), "texture_mode": "high",
				"TAPose": false, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_prop", "quality_override": "profile:game_prop", "texture_mode": "profile:game_prop",
			},
			triangles: 40000,
		},
		{
			// web_ar has no Rodin override: the budget alone bounds the Raw count
			// and takes the 2K texture mode under its 2048 texture limit.
			name:  "a purpose without an override bounds the count and the texture by its budget",
			brief: map[string]any{"purpose": "web_ar"},
			want: map[string]any{"mesh_mode": "Raw", "quality_override": float64(100000), "texture_mode": "high",
				"TAPose": false, "quad_normal": false},
			sources: map[string]string{"quality_override": "profile:web_ar", "texture_mode": "profile:web_ar"},
		},
		{
			// render allows 8192-pixel textures: the 6K display mode fits.
			name:  "the display purpose keeps the display request",
			brief: map[string]any{"purpose": "render"},
			want: map[string]any{"mesh_mode": "Raw", "quality_override": float64(2000000), "texture_mode": "extreme-high",
				"TAPose": false, "quad_normal": false},
			sources: map[string]string{},
		},
		{
			name:  "no brief keeps the display request",
			brief: nil,
			want: map[string]any{"mesh_mode": "Raw", "quality_override": float64(2000000), "texture_mode": "extreme-high",
				"TAPose": false, "quad_normal": false},
			sources: map[string]string{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			h.gateway.normalize = true
			var brief chat.ProductionBrief
			if test.brief != nil {
				brief = purposeBrief(t, test.brief)
			}
			result := h.runLockedRodin("call-rodin", brief)
			if len(h.gateway.frozen) != 1 || result.NoSubmission || h.gateway.frozen[0].OfferingID != rodin {
				t.Fatalf("Rodin was not frozen: %+v frozen=%+v", result, h.gateway.frozen)
			}
			input := h.frozenInput(0)
			for name, want := range test.want {
				if input[name] != want {
					t.Fatalf("%s = %v, want %v in %v", name, input[name], want, input)
				}
			}
			frozen := h.gateway.frozen[0]
			for name, want := range test.sources {
				if frozen.ParameterSources[name] != want || frozen.ParameterReasons[name] == "" {
					t.Fatalf("%s provenance = %q / %q, want %q with a reason", name, frozen.ParameterSources[name], frozen.ParameterReasons[name], want)
				}
			}
			for name, source := range frozen.ParameterSources {
				if _, expected := test.sources[name]; !expected {
					t.Fatalf("%s was credited to %q although the purpose did not set it", name, source)
				}
			}
			if _, reported := h.content(result)[budgetNoteKey]; reported {
				t.Fatalf("a value within the budget was reported: %s", result.AgentContent)
			}
			// A Quad count is about 2.2 triangles each in the GLB: the frozen count
			// must leave the triangle budget of the brief's platform unbroken once
			// converted.
			if input["mesh_mode"] == "Quad" {
				if test.triangles == 0 {
					t.Fatalf("%s: a Quad case needs the triangle budget it is held to", test.name)
				}
				triangles := int(input["quality_override"].(float64)) * rodinQuadTrianglesPerFacePercent / 100
				if triangles > test.triangles {
					t.Fatalf("%v quads are about %d triangles, over the %d-triangle budget", input["quality_override"], triangles, test.triangles)
				}
			}
		})
	}
}
