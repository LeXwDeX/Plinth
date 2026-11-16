package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #279: the game purposes recommend Rodin Gen-2.5 and carry its Quad overrides.
// The person locked Rodin; the purpose layer must freeze the Quad topology, the
// face count inside the purpose's triangle budget, the texture mode inside its
// texture budget and the game switches, with the purpose as the provenance of
// the advised ones, and a purpose without an override must still bound the face
// count and the texture mode by its budget (the polycount and texture-size
// semantics of the parameters).
//
// #304: the Offering declares what a Quad face comes to in the delivered GLB
// (output_triangles: 2.2 triangles, one real sample). The game purposes keep
// their explicit counts, which the Catalog holds to each platform's budget with
// that factor when it loads; a Quad nobody gave a count for (another purpose, an
// Agent's choice with its reason) and an explicit count over a tighter budget of
// the brief converge by the factor.

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

// agentChooses stands for the Agent's set_params on an Offering: the values it
// chose and, for each, the reason it gave. The executor reads them as the
// session's remembered settings, the layer above the purpose's.
func (h *processingHarness) agentChooses(offering string, values map[string]json.RawMessage, reasons map[string]string) {
	h.t.Helper()
	h.config.OfferingParameters = func(_, _, id string) map[string]json.RawMessage {
		if id != offering {
			return nil
		}
		return values
	}
	h.config.OfferingParameterReasons = func(_, _, id string) map[string]generation.ParameterReason {
		if id != offering {
			return nil
		}
		entries := map[string]generation.ParameterReason{}
		for name, reason := range reasons {
			entries[name] = generation.ParameterReason{Value: values[name], Reason: reason}
		}
		return entries
	}
	executor, err := generationtool.New(h.config)
	if err != nil {
		h.t.Fatal(err)
	}
	h.executor = executor
}

// quadFactor is the triangles per Quad face the shipped Offering declares.
func (h *processingHarness) quadFactor(offeringID string) float64 {
	h.t.Helper()
	offering, found := h.gateway.product.Get(schema.OfferingID(offeringID))
	if !found || offering.OutputTriangles == nil {
		h.t.Fatalf("%s declares no output_triangles", offeringID)
	}
	factor, declared := offering.OutputTriangles.Factor("Quad")
	if !declared {
		h.t.Fatalf("%s declares no Quad factor", offeringID)
	}
	return factor
}

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
		// largest says a budget fit chose the Quad count, so it is the most faces
		// the budget holds; an explicit count of the purpose is below it.
		largest bool
	}{
		// The advised parameters (topology, count and texture mode) keep the purpose as their
		// provenance and reason; the plain switches are applied as values only
		// (a reason on a non-advised parameter would make the freeze refuse). The counts
		// are the purposes' explicit ones, 2.2 triangles a quad, a tenth of the budget free.
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
			// A brief's own triangle budget replaces the platform's and the purpose's. One
			// the explicit count still fits changes nothing: the purpose's count is not
			// raised to the budget.
			name:  "a brief budget the explicit count fits leaves it as it is",
			brief: map[string]any{"purpose": "game_character", "target_platform": "pc_console", "triangle_budget": 30000},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(10000), "texture_mode": "high",
				"TAPose": true, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_character", "quality_override": "profile:game_character",
				"texture_mode": "profile:game_character",
			},
			triangles: 30000,
		},
		{
			// A tighter brief budget than the explicit count (10000 quads are about 22,000
			// triangles) converges it by the declared factor: 15000 triangles hold 6818.
			name:  "a brief budget tighter than the explicit count converges it by the factor",
			brief: map[string]any{"purpose": "game_character", "target_platform": "pc_console", "triangle_budget": 15000},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(6818), "texture_mode": "high",
				"TAPose": true, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_character", "quality_override": "profile:game_character",
				"texture_mode": "profile:game_character",
			},
			triangles: 15000,
			largest:   true,
		},
		{
			name:  "a prop under a brief budget of 5000 triangles holds 2272 quads",
			brief: map[string]any{"purpose": "game_prop", "target_platform": "mobile", "triangle_budget": 5000},
			want: map[string]any{"mesh_mode": "Quad", "quality_override": float64(2272), "texture_mode": "high",
				"TAPose": false, "quad_normal": true},
			sources: map[string]string{
				"mesh_mode": "profile:game_prop", "quality_override": "profile:game_prop", "texture_mode": "profile:game_prop",
			},
			triangles: 5000,
			largest:   true,
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
			// A Quad count is about 2.2 triangles each in the GLB, the factor the
			// Offering declares: the frozen count must leave the triangle budget of
			// the brief's platform unbroken once converted. A count a budget fit chose
			// is also the largest that does.
			if input["mesh_mode"] == "Quad" {
				if test.triangles == 0 {
					t.Fatalf("%s: a Quad case needs the triangle budget it is held to", test.name)
				}
				factor := h.quadFactor(rodin)
				count := input["quality_override"].(float64)
				if triangles := schema.TrianglesForFaces(count, factor); triangles > float64(test.triangles) {
					t.Fatalf("%v quads are about %v triangles, over the %d-triangle budget", count, triangles, test.triangles)
				}
				if triangles := schema.TrianglesForFaces(count+1, factor); test.largest && triangles <= float64(test.triangles) {
					t.Fatalf("%v quads are not the most that fit the %d-triangle budget (one more would be %v triangles)",
						count, test.triangles, triangles)
				}
			}
		})
	}
}

// #304: a Quad the Agent picks, with its reason, takes a face count that keeps
// the triangles inside the budget of the session's purpose and platform,
// whichever purpose it is. A purpose that names no Rodin count (web and AR) gets
// the largest count its budget holds, converted with the Offering's declared
// factor; a game purpose keeps its explicit count, which that factor converges
// only when the brief's own budget is tighter. The Agent's own topology keeps its
// reason; the count is the purpose's, with the purpose as its provenance.
func TestAgentChosenQuadFitsTheBudgetOfAnyPurpose(t *testing.T) {
	const rodin = "hyper3d/rodin-gen-2.5/text-to-3d"
	const agentReason = "the person asked for quad topology"
	for _, test := range []struct {
		name  string
		brief map[string]any
		// triangles is the budget the purpose resolves to on the brief's platform.
		triangles int
		// want is the Quad count frozen; purposeCounts says the purpose gave it, by
		// its explicit count or by fitting its budget (otherwise the Offering's Quad
		// ceiling stands, which already fits).
		want          float64
		purposeCounts bool
	}{
		{"web and AR", map[string]any{"purpose": "web_ar"}, 100000, 45454, true},
		{"web and AR with the brief's own budget", map[string]any{"purpose": "web_ar", "triangle_budget": 30000}, 30000, 13636, true},
		{"a budget that is a whole number of Quad faces", map[string]any{"purpose": "web_ar", "triangle_budget": 22000}, 22000, 10000, true},
		{"a character without a platform", map[string]any{"purpose": "game_character"}, 60000, 8000, true},
		{"a character on a phone", map[string]any{"purpose": "game_character", "target_platform": "mobile"}, 20000, 8000, true},
		{"a character on PC and console under a tighter budget", map[string]any{"purpose": "game_character", "target_platform": "pc_console", "triangle_budget": 15000}, 15000, 6818, true},
		{"a prop on a phone", map[string]any{"purpose": "game_prop", "target_platform": "mobile"}, 8000, 3200, true},
		{"a prop on PC and console", map[string]any{"purpose": "game_prop", "target_platform": "pc_console"}, 40000, 4000, true},
		{"a prop on a phone under a tighter budget", map[string]any{"purpose": "game_prop", "target_platform": "mobile", "triangle_budget": 5000}, 5000, 2272, true},
		// The display and print purposes allow two million triangles: the Quad
		// ceiling of 200,000 faces (440,000 triangles) fits, so nothing is cut.
		{"display", map[string]any{"purpose": "render"}, 2000000, 200000, false},
		{"print", map[string]any{"purpose": "print"}, 2000000, 200000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			h.gateway.normalize = true
			h.agentChooses(rodin, map[string]json.RawMessage{"mesh_mode": json.RawMessage(`"Quad"`)},
				map[string]string{"mesh_mode": agentReason})
			result := h.runLockedRodin("call-quad", purposeBrief(t, test.brief))
			if len(h.gateway.frozen) != 1 || result.NoSubmission || h.gateway.frozen[0].OfferingID != rodin {
				t.Fatalf("Rodin was not frozen: %+v frozen=%+v", result, h.gateway.frozen)
			}
			input, frozen := h.frozenInput(0), h.gateway.frozen[0]
			if input["mesh_mode"] != "Quad" || input["quality_override"] != test.want {
				t.Fatalf("mesh_mode = %v, quality_override = %v, want Quad at %v in %v", input["mesh_mode"], input["quality_override"], test.want, input)
			}
			factor := h.quadFactor(rodin)
			if triangles := schema.TrianglesForFaces(test.want, factor); triangles > float64(test.triangles) {
				t.Fatalf("%v quads are about %v triangles, over the %d-triangle budget", test.want, triangles, test.triangles)
			}
			// The Agent's topology is the Agent's, with its own reason; the count the
			// purpose gave carries the purpose as its source.
			if frozen.ParameterSources["mesh_mode"] != "" || frozen.ParameterReasons["mesh_mode"] != agentReason {
				t.Fatalf("mesh_mode provenance = %q / %q, want the Agent's reason without a purpose source",
					frozen.ParameterSources["mesh_mode"], frozen.ParameterReasons["mesh_mode"])
			}
			wantSource := ""
			if test.purposeCounts {
				wantSource = "profile:" + test.brief["purpose"].(string)
			}
			if got := frozen.ParameterSources["quality_override"]; got != wantSource {
				t.Fatalf("quality_override source = %q, want %q", got, wantSource)
			}
			if test.purposeCounts && frozen.ParameterReasons["quality_override"] == "" {
				t.Fatal("the purpose's count has no reason")
			}
			if note, reported := h.content(result)[budgetNoteKey]; reported {
				t.Fatalf("a count inside the budget was reported: %v", note)
			}
		})
	}
}

// #304: a count the Agent chose for a Quad topology is kept even when it is over
// the budget, and the note counts it in triangles: it names the converted amount,
// not the face count as if it were triangles.
func TestExplicitQuadCountOverTheBudgetIsReportedInTriangles(t *testing.T) {
	const rodin = "hyper3d/rodin-gen-2.5/text-to-3d"
	phoneProp := purposeBrief(t, map[string]any{"purpose": "game_prop", "target_platform": "mobile"})
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	// 4000 Quad faces are 8,800 triangles: over the phone prop's 8,000, though 4000
	// itself is not.
	h.agentChooses(rodin, map[string]json.RawMessage{"mesh_mode": json.RawMessage(`"Quad"`), "quality_override": json.RawMessage(`4000`)},
		map[string]string{"mesh_mode": "quads", "quality_override": "detail"})
	result := h.runLockedRodin("call-over", phoneProp)
	if len(h.gateway.frozen) != 1 {
		t.Fatalf("Rodin was not frozen: %+v", result)
	}
	if got := h.frozenInput(0)["quality_override"]; got != float64(4000) {
		t.Fatalf("the Agent's count was changed to %v", got)
	}
	note, _ := h.content(result)[budgetNoteKey].(string)
	for _, want := range []string{"4000", "8800", "8000"} {
		if !strings.Contains(note, want) {
			t.Fatalf("budget note %q does not mention %q", note, want)
		}
	}
	// The same count as triangles (Raw) is inside the budget and is not reported.
	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	h.agentChooses(rodin, map[string]json.RawMessage{"mesh_mode": json.RawMessage(`"Raw"`), "quality_override": json.RawMessage(`4000`)},
		map[string]string{"mesh_mode": "triangles", "quality_override": "detail"})
	result = h.runLockedRodin("call-raw", phoneProp)
	if _, reported := h.content(result)[budgetNoteKey]; reported {
		t.Fatalf("4000 triangles were reported over an 8000-triangle budget: %s", result.AgentContent)
	}
}
