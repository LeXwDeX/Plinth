package generationtool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
)

// #139: dev-test sent Tripo v3.1 steps with smart_low_poly on and the
// two-million face default; Tripo refused them (code 1004) because smart low
// poly allows 500–20000 faces on a triangle mesh and 500–10000 on a quad mesh.
// The Schema now declares those ranges: a default moves into them, a value the
// Agent gave outside them is refused before anything is sent.

// smartLowPolyHarness runs the Tripo step through the real normalization with
// the Agent's remembered settings (set_params) for the locked model.
func smartLowPolyHarness(t *testing.T, settings map[string]any) *processingHarness {
	t.Helper()
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	encoded := map[string]json.RawMessage{}
	for name, value := range settings {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		encoded[name] = raw
	}
	h.config.OfferingParameters = func(_, _, offering string) map[string]json.RawMessage {
		if offering != "tripo/tripo-v3.1-full/text-to-3d" {
			return nil
		}
		return encoded
	}
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	return h
}

func TestSmartLowPolyDefaultFaceLimitFollowsTheMesh(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings map[string]any
		want     float64
	}{
		{"smart low poly off", map[string]any{}, 2000000},
		{"smart low poly with a triangle mesh", map[string]any{"smart_low_poly": true}, 20000},
		{"smart low poly with a quad mesh", map[string]any{"smart_low_poly": true, "quad": true}, 10000},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := smartLowPolyHarness(t, test.settings)
			result := h.runLocked3D("call-smart", nil, nil)
			if len(h.gateway.frozen) != 1 || result.NoSubmission {
				t.Fatalf("step was not frozen: %+v", result)
			}
			if limit := h.frozenInput(0)["face_limit"]; limit != test.want {
				t.Fatalf("face_limit = %v, want %v", limit, test.want)
			}
		})
	}
}

func TestGivenFaceLimitOutsideSmartLowPolyIsRefusedBeforeSubmission(t *testing.T) {
	for _, test := range []struct {
		settings map[string]any
		rangeOf  string
	}{
		{map[string]any{"smart_low_poly": true, "face_limit": 2000000}, "1000–20000"},
		{map[string]any{"smart_low_poly": true, "quad": true, "face_limit": 20000}, "1000–10000"},
	} {
		h := smartLowPolyHarness(t, test.settings)
		result := h.runLocked3D("call-refused", nil, nil)
		if !result.NoSubmission || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 {
			t.Fatalf("%v: an illegal face_limit reached the paid path: %+v frozen=%d", test.settings, result, len(h.gateway.frozen))
		}
		message, _ := h.content(result)["error"].(string)
		if !strings.Contains(message, test.rangeOf) || !strings.Contains(message, "面数上限") {
			t.Fatalf("%v: refusal %q does not name the label and the range %s", test.settings, message, test.rangeOf)
		}
	}
}

// The purpose budget (#129) and the smart-low-poly range both bound the face
// limit nobody gave; the stricter one wins, and the budget's value keeps the
// purpose's provenance.
func TestSmartLowPolyAndThePurposeBudgetTakeTheStricterLimit(t *testing.T) {
	for _, test := range []struct {
		name     string
		brief    map[string]any
		settings map[string]any
		want     float64
		source   string
	}{
		{"quad smart low poly under game_prop", map[string]any{"purpose": "game_prop"},
			map[string]any{"smart_low_poly": true, "quad": true}, 10000, ""},
		{"smart low poly under game_character", map[string]any{"purpose": "game_character"},
			map[string]any{"smart_low_poly": true}, 20000, ""},
		{"smart low poly under a 5000-triangle brief", map[string]any{"purpose": "game_prop", "triangle_budget": 5000},
			map[string]any{"smart_low_poly": true}, 5000, "profile:game_prop"},
		{"a quad mesh without smart low poly under game_prop", map[string]any{"purpose": "game_prop"},
			map[string]any{"quad": true}, 20000, "profile:game_prop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := smartLowPolyHarness(t, test.settings)
			result := h.runLocked3D("call-budget", purposeBrief(t, test.brief), nil)
			if len(h.gateway.frozen) != 1 || result.NoSubmission {
				t.Fatalf("step was not frozen: %+v", result)
			}
			if limit := h.frozenInput(0)["face_limit"]; limit != test.want {
				t.Fatalf("face_limit = %v, want %v", limit, test.want)
			}
			if source := h.gateway.frozen[0].ParameterSources["face_limit"]; source != test.source {
				t.Fatalf("face_limit source = %q, want %q", source, test.source)
			}
			if _, reported := h.content(result)[budgetNoteKey]; reported {
				t.Fatalf("a face limit within the budget was reported: %s", result.AgentContent)
			}
		})
	}
}
