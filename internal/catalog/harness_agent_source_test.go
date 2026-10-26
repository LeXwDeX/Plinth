package catalog

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHarnessAgentSourceSchemaPublicModesAndFailClosedValidation(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h := c.Application().Chat.Harness
	public := h.Public()
	if len(public.SourceKinds) != 2 || len(public.TaskSourceModes) != 2 || public.TaskSourceModes[0].ID != "agent" || public.TaskSourceModes[1].ID != "fixed" || public.DefaultTaskSourceMode != "agent" || public.SourceClarificationMessage == "" {
		t.Fatal("missing Agent/fixed contract")
	}
	raw, _ := json.Marshal(public)
	if bytes.Contains(raw, []byte("context_template")) {
		t.Fatal("private instructions exposed")
	}
	for _, mutate := range []func(map[string]any){func(h map[string]any) { h["default_task_source_mode"] = "invented" }, func(h map[string]any) { h["source_clarification_message"] = "" }, func(h map[string]any) {
		h["task_source_modes"] = []any{map[string]any{"id": "invented", "label": "unknown", "description": "unknown"}}
	}} {
		var doc map[string]any
		json.Unmarshal(defaultCatalog, &doc)
		mutate(doc["application"].(map[string]any)["chat"].(map[string]any)["harness"].(map[string]any))
		b, _ := json.Marshal(doc)
		if _, err := Load(bytes.NewReader(b)); err == nil {
			t.Fatal("invalid source mode declaration loaded")
		}
	}
}

func TestHarnessTaskSourceModesAreDefensivelyCloned(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	original := c.Application().Chat.Harness.TaskSourceModes[0]
	application := c.Application()
	public := application.Public()
	public.Chat.Harness.TaskSourceModes[0].Label = "public mutation"
	if application.Chat.Harness.TaskSourceModes[0] != original {
		t.Fatal("public mode aliases application")
	}
	application.Chat.Harness.TaskSourceModes[0].ID = "application mutation"
	if c.Application().Chat.Harness.TaskSourceModes[0] != original {
		t.Fatal("application mode aliases catalog")
	}
	public = c.Application().Public()
	public.Chat.Harness.TaskSourceModes[0].Description = "another public mutation"
	if c.Application().Public().Chat.Harness.TaskSourceModes[0] != original {
		t.Fatal("public mode aliases catalog")
	}
}
