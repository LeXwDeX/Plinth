package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func processingActionWire(t *testing.T, application ApplicationSchema) map[string]any {
	t.Helper()
	raw, err := json.Marshal(application)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return wire["canvas"].(map[string]any)["frontend"].(map[string]any)
}

func TestProcessingActionDefaultPublicAndClone(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	original := c.Application()
	want := map[string]any{"calls_model": false, "cost_hint": "process_panel", "group": "produce"}
	if got := processingActionWire(t, original)["processing_action"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("processing navigation presentation = %#v, want %#v", got, want)
	}
	public := original.Public()
	raw, err := json.Marshal(public.Canvas.Frontend)
	if err != nil {
		t.Fatal(err)
	}
	var projected map[string]any
	if err := json.Unmarshal(raw, &projected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projected["processing_action"], want) {
		t.Fatalf("public processing_action = %#v", projected["processing_action"])
	}
	// The presentation has only scalar fields; the Catalog's value copy must remain independent.
	first := c.Application()
	field := reflect.ValueOf(&first.Canvas.Frontend).Elem().FieldByName("ProcessingAction")
	if !field.IsValid() {
		t.Fatal("typed ProcessingAction field missing")
	}
	field.FieldByName("CallsModel").SetBool(true)
	field.FieldByName("CostHint").SetString("approval_card")
	field.FieldByName("Group").SetString("view")
	if !reflect.DeepEqual(processingActionWire(t, c.Application())["processing_action"], want) {
		t.Fatal("processing_action leaked a mutation across Catalog snapshots")
	}
}

func TestProcessingActionValidation(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, group, hint string
		mark              bool
		want              string
	}{
		{"neutral navigation", "produce", "process_panel", false, ""},
		{"marked navigation", "produce", "process_panel", true, ""},
		{"approval", "output", "approval_card", true, ""},
		{"free", "view", "free", false, ""},
		{"missing fields", "", "", false, "processing_action.group"},
		{"unknown group", "identity", "process_panel", false, "processing_action.group"},
		{"unknown hint", "produce", "paid", false, "processing_action.cost_hint"},
		{"approval without mark", "produce", "approval_card", false, "processing_action.calls_model"},
		{"free with mark", "produce", "free", true, "processing_action.calls_model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			application := c.Application()
			raw, err := json.Marshal(application)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			wire["canvas"].(map[string]any)["frontend"].(map[string]any)["processing_action"] = map[string]any{"calls_model": tc.mark, "cost_hint": tc.hint, "group": tc.group}
			raw, err = json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ApplicationSchema
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			err = ValidateApplicationSchema(decoded)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	// The same common rule protects static actions: neutral process navigation is valid,
	// approval_card without the mark and free with it are still rejected by existing tests.
	application := c.Application()
	application.Canvas.Frontend.StageActions[2].CallsModel = false
	application.Canvas.Frontend.StageActions[2].CostHint = "process_panel"
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("neutral static navigation rejected: %v", err)
	}
}
