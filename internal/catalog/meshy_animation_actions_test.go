package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestMeshyAnimationActionsFollowOfficialLibrary pins the Meshy animation
// action_id options to Meshy's official Animation Library (#265): the value is
// the official action_id and the label ends with the official name in
// parentheses, which is also the clip name Meshy delivers (a real run with
// action_id 4 returned the clip "Armature|Attack|baselayer").
//
// Source: https://docs.meshy.ai/en/api/animation-library (linked from
// https://docs.meshy.ai/en/api/animation), retrieved 2026-10-20: 656 actions,
// ids 0–696 with gaps for retired actions.
func TestMeshyAnimationActionsFollowOfficialLibrary(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, found := c.Get("meshy/processing/animation")
	if !found {
		t.Fatal("missing meshy/processing/animation")
	}
	var options []generation.ParameterOption
	for _, parameter := range offering.Parameters {
		if parameter.Name == "action_id" {
			options = parameter.Options
		}
	}
	if len(options) != 656 {
		t.Fatalf("action_id options = %d, want the 656 actions of the official library", len(options))
	}
	labels := map[string]string{}
	seen := map[string]bool{}
	for _, option := range options {
		if seen[option.Label] {
			t.Fatalf("duplicate label %q", option.Label)
		}
		seen[option.Label] = true
		labels[option.Value] = option.Label
		if option.Group == "" {
			t.Fatalf("action %s has no group", option.Value)
		}
	}
	for value, name := range map[string]string{
		"0":   "Idle",
		"1":   "Walking_Woman",
		"2":   "Alert",
		"3":   "Arise",
		"4":   "Attack",
		"92":  "Double_Combo_Attack",
		"290": "Wave_One_Hand",
		"696": "Walk_with_Walker_Support_inplace",
	} {
		if !strings.HasSuffix(labels[value], " ("+name+")") {
			t.Fatalf("action_id %s label = %q, want official name %q", value, labels[value], name)
		}
	}
	// Retired ids leave gaps; a gap must not be selectable.
	if _, listed := labels["332"]; listed {
		t.Fatal("retired action_id 332 is listed")
	}
	for _, input := range []string{`{"source_task":"rig-1","action_id":332}`, `{"source_task":"rig-1","action_id":697}`} {
		if _, _, err := c.Normalize(generation.Animation, "meshy/processing/animation", json.RawMessage(input)); err == nil {
			t.Fatalf("input %s accepted", input)
		}
	}
	if _, _, err := c.Normalize(generation.Animation, "meshy/processing/animation", json.RawMessage(`{"source_task":"rig-1","action_id":696}`)); err != nil {
		t.Fatalf("official action 696 rejected: %v", err)
	}
}

// A number parameter with options is a closed enum: Catalog load rejects an
// option that is not a number, repeats another or breaks the declared range.
func TestNumberParameterOptionsMustBeDistinctNumbersInRange(t *testing.T) {
	for name, bad := range map[string]string{"not a number": "Idle", "duplicate": "0.0", "below minimum": "-1"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("default_catalog.json")
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			patched := false
			for _, value := range doc["offerings"].([]any) {
				offering := value.(map[string]any)
				if offering["id"] != "meshy/processing/animation" {
					continue
				}
				for _, item := range offering["parameters"].([]any) {
					parameter := item.(map[string]any)
					if parameter["name"] == "action_id" {
						options := parameter["options"].([]any)
						options[1].(map[string]any)["value"] = bad
						patched = true
					}
				}
			}
			if !patched {
				t.Fatal("action_id parameter missing")
			}
			raw, _ = json.Marshal(doc)
			if _, err := catalog.Load(bytes.NewReader(raw)); err == nil {
				t.Fatalf("number option %q accepted", bad)
			}
		})
	}
}
