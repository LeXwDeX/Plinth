package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestDefaultChatToolsDeclareRuntimeExecutionContracts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	// 24 controls: the shipped 21 plus #246's compare_model_reference and
	// record_self_check, #250's create_selection_mask/set_visual_anchor and
	// #240's list_project_assets, which meet in an integration tree.
	if len(app.Chat.Tools) != 15 || len(app.Chat.AgentTools) != 24 {
		t.Fatalf("chat tool inventory = generation:%d controls:%d", len(app.Chat.Tools), len(app.Chat.AgentTools))
	}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("default execution metadata invalid: %v", err)
	}
	for _, tool := range app.Chat.Tools {
		switch tool.Name {
		case "create_model_3d", "create_model_3d_from_image", "create_model_3d_from_views", "create_gaussian_splat", "retexture_model", "process_model", "plan_model_processing", "convert_model_format",
			"create_creative_lab_design", "build_creative_lab_item":
		default:
			continue
		}
		if len(tool.Execution.Domains) != 1 || tool.Execution.Domains[0] != "model-3d" {
			t.Fatalf("3D tool %s must use the workspace's model-3d domain, got %v", tool.Name, tool.Execution.Domains)
		}
	}
	public := app.Public()
	if len(public.Chat.Tools) != len(app.Chat.Tools) {
		t.Fatalf("public tools = %d; want %d", len(public.Chat.Tools), len(app.Chat.Tools))
	}
}

// renameBackgroundWaitTool follows a tool rename into chat.agent.background_wait
// (#137), which names its eligible tools.
func renameBackgroundWaitTool(policy *catalog.ChatBackgroundWaitSchema, from, to string) {
	if policy == nil {
		return
	}
	for index, name := range policy.Tools {
		if name == from {
			policy.Tools[index] = to
		}
	}
}

func TestCatalogAllowsNewGenerationToolNameWithRegisteredCapability(t *testing.T) {
	payload, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	var app catalog.ApplicationSchema
	if err := json.Unmarshal(document["application"], &app); err != nil {
		t.Fatal(err)
	}
	// Rename a tool the Catalog localizes by identity (its description lives in
	// the tool declaration, not the bundles), so the new name only has to keep a
	// registered generation capability; the old name appears nowhere else.
	renamed := false
	for index := range app.Chat.Tools {
		if app.Chat.Tools[index].Name == "create_model_3d" {
			app.Chat.Tools[index].Name = "generate_model_3d_variant"
			renamed = true
		}
	}
	// A rename has to reach every declaration that names the tool, including a
	// craft brief's trigger list.
	for index := range app.Chat.Agent.CraftPrompts {
		for trigger := range app.Chat.Agent.CraftPrompts[index].WhenToolsAny {
			if app.Chat.Agent.CraftPrompts[index].WhenToolsAny[trigger] == "create_model_3d" {
				app.Chat.Agent.CraftPrompts[index].WhenToolsAny[trigger] = "generate_model_3d_variant"
			}
		}
	}
	renameBackgroundWaitTool(app.Chat.Agent.BackgroundWait, "create_model_3d", "generate_model_3d_variant")
	if !renamed {
		t.Fatal("create_model_3d is not declared")
	}
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	document["application"] = encoded
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(payload)); err != nil {
		t.Fatalf("renamed generation tool rejected: %v", err)
	}
}

func TestCatalogAllowsImageToolForProcessingClassifiedCapability(t *testing.T) {
	payload, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	var app catalog.ApplicationSchema
	if err := json.Unmarshal(document["application"], &app); err != nil {
		t.Fatal(err)
	}
	oldName := app.Chat.Tools[1].Name
	renameBackgroundWaitTool(app.Chat.Agent.BackgroundWait, oldName, "cutout_assets")
	for index := range app.Chat.Agent.CraftPrompts {
		for at, name := range app.Chat.Agent.CraftPrompts[index].WhenToolsAny {
			if name == oldName {
				app.Chat.Agent.CraftPrompts[index].WhenToolsAny[at] = "cutout_assets"
			}
		}
	}
	app.Chat.Tools[1].Name = "cutout_assets"
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	document["application"] = encoded
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(payload)); err != nil {
		t.Fatalf("image generation tool for processing-classified capability rejected: %v", err)
	}
}

func TestChatToolExecutionValidationRejectsUnsafeOrUnregisteredMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"paid task cannot drop consent", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Consent = "none" }},
		{"paid task cannot downgrade effect", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Effects = []string{"READ"} }},
		{"generation implementation is fixed", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.ImplementationKey = "arbitrary.execute" }},
		{"generation capability closure", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Capabilities = nil }},
		{"fake capability is rejected", func(a *catalog.ApplicationSchema) {
			a.Chat.Tools[0].Execution.Capabilities = []schemacontract.Capability{"unregistered-capability"}
		}},
		{"domains are validated", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Domains = []string{"admin"} }},
		{"legacy model domain is rejected", func(a *catalog.ApplicationSchema) { a.Chat.Tools[2].Execution.Domains = []string{"model"} }},
		{"cross-list duplicate tool names", func(a *catalog.ApplicationSchema) { a.Chat.AgentTools[0].Name = a.Chat.Tools[0].Name }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema unexpectedly accepted invalid execution metadata")
			}
		})
	}
}
