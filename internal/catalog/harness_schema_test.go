package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestHarnessPublicOperationsDescribeSemanticGroups(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var app struct {
		Chat struct {
			Harness struct {
				Groups []struct {
					ID string `json:"id"`
				} `json:"groups"`
				Operations []struct {
					ID         string   `json:"id"`
					Group      string   `json:"group"`
					Capability string   `json:"capability"`
					ModelIDs   []string `json:"model_ids"`
				} `json:"operations"`
			} `json:"harness"`
		} `json:"chat"`
	}
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Chat.Harness.Groups) != 2 || app.Chat.Harness.Groups[0].ID != "common" || app.Chat.Harness.Groups[1].ID != "model" {
		t.Fatalf("expected common operations before model-specific operations, got %+v", app.Chat.Harness.Groups)
	}
	seen := map[string]bool{}
	for _, op := range app.Chat.Harness.Operations {
		seen[op.ID] = true
		if op.Group == "common" && len(op.ModelIDs) > 0 {
			t.Errorf("common operation %s is filtered by the left generation model", op.ID)
		}
	}
	for _, id := range []string{"retexture", "remesh", "rigging", "animation"} {
		if !seen[id] {
			t.Errorf("existing executable operation %s missing from public Harness catalog", id)
		}
	}
	for _, id := range []string{"mesh-segmentation", "mesh-completion"} {
		if !seen[id] {
			t.Errorf("processing operation %s missing from public Harness catalog", id)
		}
	}
}

func TestHarnessMeshProcessingOperationsStaySchemaBound(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	want := map[string]struct {
		label      string
		capability string
		step       string
	}{
		"mesh-segmentation": {label: "模型分件", capability: "mesh-segmentation", step: "mesh-segmentation"},
		"mesh-completion":   {label: "部件补全", capability: "mesh-completion", step: "mesh-completion"},
	}
	seen := map[string]bool{}
	for _, op := range app.Chat.Harness.Operations {
		expected, ok := want[op.ID]
		if !ok {
			continue
		}
		seen[op.ID] = true
		var step string
		if err := json.Unmarshal(op.ToolArguments["step"], &step); err != nil {
			t.Fatalf("%s step argument: %v", op.ID, err)
		}
		if op.Label != expected.label || string(op.Capability) != expected.capability || op.ToolName != "process_model" || step != expected.step {
			t.Errorf("%s operation binding = %+v, step=%q", op.ID, op, step)
		}
		if op.Group != "common" || len(op.ModelIDs) != 0 || slices.Contains(app.Chat.Harness.DefaultEnabledOperations, op.ID) {
			t.Errorf("%s must stay a model-independent, non-default operation", op.ID)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("operation %s missing", id)
		}
	}
	completion := app.Chat.Harness.Operations[len(app.Chat.Harness.Operations)-1]
	if completion.ID != "mesh-completion" || !strings.Contains(completion.Description, "本人同会话已成功的原生 Tripo Segment") ||
		!strings.Contains(completion.Description, "Smart Segmentation") || !strings.Contains(completion.Description, "Bang") {
		t.Fatalf("completion source boundary missing or operation not appended last: %+v", completion)
	}
	translations := app.Localization.Bundles["en"].Fields
	for key, value := range map[string]string{
		"chat.harness.operations[id=mesh-segmentation].label":     "Model part segmentation",
		"chat.harness.operations[id=mesh-completion].label":       "Part completion",
		"chat.harness.operations[id=mesh-completion].description": "Accepts only your own successfully completed native Tripo Segment task from this same session; Smart Segmentation and Bang segmentation results are not valid sources. Executable only when a compatible service is enabled.",
	} {
		if translations[key] != value {
			t.Errorf("translation %s = %q, want %q", key, translations[key], value)
		}
	}
}

func TestHarnessDeclarationsRejectPermissionAndGoalDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"unknown tool", func(a *catalog.ApplicationSchema) { a.Chat.Harness.Operations[0].ToolName = "not_wired" }},
		{"unknown operation", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.DefaultEnabledOperations = append(a.Chat.Harness.DefaultEnabledOperations, "invented")
		}},
		{"duplicate operation", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations = append(a.Chat.Harness.Operations, a.Chat.Harness.Operations[0])
		}},
		{"paid base tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.BaseTools = append(a.Chat.Harness.BaseTools, "create_three_view")
		}},
		{"wrong step", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations[1].ToolArguments["step"] = json.RawMessage(`"animation"`)
		}},
		{"unexpected tool argument", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations[1].ToolArguments["provider_key"] = json.RawMessage(`"x"`)
		}},
		{"common tied to generation model", func(a *catalog.ApplicationSchema) { a.Chat.Harness.Operations[1].ModelIDs = []string{"meshy-6"} }},
		{"model-specific without model", func(a *catalog.ApplicationSchema) { a.Chat.Harness.Operations[1].Group = "model" }},
		{"unknown prerequisite", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations[3].Prerequisites = [][]string{{"invented"}}
		}},
		{"goal as its own prerequisite", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations[3].Prerequisites = [][]string{{"animation"}}
		}},
		{"duplicate prerequisite", func(a *catalog.ApplicationSchema) {
			a.Chat.Harness.Operations[3].Prerequisites = [][]string{{"rigging", "rigging"}}
		}},
		{"unwired without reason", func(a *catalog.ApplicationSchema) { a.Chat.Harness.Operations[0].ToolName = "" }},
		{"snapshot missing from context", func(a *catalog.ApplicationSchema) { a.Chat.Harness.ContextTemplate = "no snapshot" }},
		{"route has no session", func(a *catalog.ApplicationSchema) { a.Runtime.HTTP.Routes.ChatSessionHarness = "/api/v1/harness" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			a := c.Application()
			tc.mutate(&a)
			if err := catalog.ValidateApplicationSchema(a); err == nil {
				t.Fatal("invalid Harness contract accepted")
			}
		})
	}
}

func TestHarnessPublicProjectionOmitsPrivatePolicy(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := c.Application()
	a.Chat.Harness.Operations[0].ToolName = ""
	a.Chat.Harness.Operations[0].UnavailableReason = "Not connected yet"
	if err := catalog.ValidateApplicationSchema(a); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(a.Public().Chat.Harness)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"context_template", "base_tools", "prerequisite_tool", "rejection_message"} {
		if _, found := fields[key]; found {
			t.Errorf("private policy %s exposed", key)
		}
	}
	var operations []map[string]json.RawMessage
	if err := json.Unmarshal(fields["operations"], &operations); err != nil {
		t.Fatal(err)
	}
	if string(operations[0]["unavailable_reason"]) != `"Not connected yet"` {
		t.Fatal("unwired operation lost its public reason")
	}
	for _, op := range operations {
		for _, key := range []string{"tool_name", "tool_arguments", "implementation_key"} {
			if _, found := op[key]; found {
				t.Errorf("private wiring %s exposed", key)
			}
		}
	}
}
