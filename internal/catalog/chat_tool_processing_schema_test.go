package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func processingTool(t *testing.T, app *catalog.ApplicationSchema, name string) *catalog.ChatToolSchema {
	t.Helper()
	for index := range app.Chat.Tools {
		if app.Chat.Tools[index].Name == name {
			return &app.Chat.Tools[index]
		}
	}
	t.Fatalf("tool %s is not declared", name)
	return nil
}

func TestDefaultSchemaDeclaresModelProcessingTools(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	want := map[string][]schemacontract.Capability{
		"retexture_model":       {"retexture"},
		"process_model":         {"remesh", "mesh-segmentation", "mesh-completion", "rigging", "animation"},
		"plan_model_processing": {"remesh", "mesh-segmentation", "mesh-completion", "rigging", "animation"},
		"convert_model_format":  {"format-conversion"},
	}
	for name, capabilities := range want {
		tool := processingTool(t, &app, name)
		if tool.Processing == nil || tool.Execution.Domains[0] != "model-3d" {
			t.Fatalf("%s must be a model-3d processing tool", name)
		}
		for _, capability := range capabilities {
			if !slices.Contains(tool.Execution.Capabilities, capability) {
				t.Fatalf("%s execution closure misses %s: %v", name, capability, tool.Execution.Capabilities)
			}
		}
		if name == "process_model" || name == "plan_model_processing" {
			for _, step := range []schemacontract.Capability{"mesh-segmentation", "mesh-completion"} {
				if tool.Processing.Steps[string(step)] != step {
					t.Errorf("%s processing map does not bind %s: %v", name, step, tool.Processing.Steps)
				}
			}
			var parameters struct {
				Properties map[string]struct {
					Enum  []string `json:"enum"`
					Items struct {
						Properties map[string]struct {
							Enum []string `json:"enum"`
						} `json:"properties"`
					} `json:"items"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				t.Fatal(err)
			}
			enum := parameters.Properties["step"].Enum
			if name == "plan_model_processing" {
				enum = parameters.Properties["steps"].Items.Properties["capability"].Enum
			}
			for _, step := range []string{"mesh-segmentation", "mesh-completion"} {
				if !slices.Contains(enum, step) {
					t.Errorf("%s tool schema enum misses %s: %v", name, step, enum)
				}
			}
			if !strings.Contains(tool.Description, "mesh segmentation") || !strings.Contains(tool.Description, "part completion") {
				t.Errorf("%s description does not explain both capabilities", name)
			}
			if !strings.Contains(tool.Description, "same session") || !strings.Contains(tool.Description, "native Tripo Segment") ||
				!strings.Contains(tool.Description, "Smart Segmentation") || !strings.Contains(tool.Description, "Bang") {
				t.Errorf("%s description omits the mesh-completion source boundary", name)
			}
			if app.Localization.Bundles["en"].Fields["chat.tools[name="+name+"].description"] != tool.Description {
				t.Errorf("%s English description is not localized in the bundle", name)
			}
		}
	}
	// Chat converts formats along the format coverage's Schema-ordered routes.
	if argument := processingTool(t, &app, "convert_model_format").Processing.DeliveryFormatArgument; argument != "format" {
		t.Fatalf("convert_model_format delivery_format_argument = %q", argument)
	}
	if !slices.Contains(app.Runtime.Frontend.ModelProcessingCapabilities, "retexture") ||
		!slices.Contains(app.Runtime.Frontend.ModelProcessingCapabilities, "mesh-segmentation") ||
		!slices.Contains(app.Runtime.Frontend.ModelProcessingCapabilities, "mesh-completion") ||
		!slices.Contains(app.Runtime.Frontend.ModelProcessingCapabilities, "format-conversion") {
		t.Fatalf("panel capabilities = %v", app.Runtime.Frontend.ModelProcessingCapabilities)
	}
	for _, text := range []string{"模型分件", "部件补全", "本人同会话已成功的原生 Tripo Segment", "Smart Segmentation", "Bang"} {
		if !strings.Contains(app.Chat.Agent.SystemPrompt, text) {
			t.Errorf("Chinese Agent instructions omit %q", text)
		}
	}
	var listed bool
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "list_session_models" {
			listed = true
			if len(tool.Execution.Domains) != 1 || tool.Execution.Domains[0] != "model-3d" {
				t.Fatalf("list_session_models domains = %v", tool.Execution.Domains)
			}
		}
	}
	if !listed {
		t.Fatal("list_session_models is not declared")
	}
	// Server-only processing metadata must not reach the public projection.
	for _, tool := range app.Public().Chat.Tools {
		if tool.Name == "process_model" && tool.Capability == "" {
			t.Fatal("public tool lost its capability")
		}
	}
}

func TestChatToolProcessingValidationRejectsInconsistentDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"source argument must be declared", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.SourceTaskArgument = "missing"
		}},
		{"argument cannot be reused", func(a *catalog.ApplicationSchema) {
			tool := processingTool(t, a, "retexture_model")
			tool.Processing.PromptArgument = tool.Processing.SourceTaskArgument
		}},
		{"unknown mapped argument", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.ParameterArguments["ghost"] = "resolution"
		}},
		{"step argument must be required", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "process_model").Processing.StepArgument = "source_task_id"
		}},
		{"steps need a step argument", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.Steps = map[string]schemacontract.Capability{"x": "remesh"}
		}},
		{"steps must stay in the processing domain", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "process_model").Processing.Steps["remesh"] = "text-to-3d"
		}},
		{"single step tool needs matching fallback", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").FallbackCapability = "remesh"
		}},
		{"messages are required", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "convert_model_format").Processing.Messages.SourceNotFound = " "
		}},
		{"step capability must be in the execution closure", func(a *catalog.ApplicationSchema) {
			tool := processingTool(t, a, "process_model")
			tool.Execution.Capabilities = []schemacontract.Capability{"remesh"}
		}},
		{"list_session_models is model-3d only", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "list_session_models" {
					a.Chat.AgentTools[index].Execution.Domains = []string{"image", "model-3d"}
				}
			}
		}},
		{"listing bound is required", func(a *catalog.ApplicationSchema) { a.Chat.Agent.MaxListedModels = 0 }},
		{"plan steps argument must be declared", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "plan_model_processing").Processing.Plan.StepsArgument = "missing"
		}},
		{"plan cannot mix single-step mappings", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "plan_model_processing").Processing.StepArgument = "steps"
		}},
		{"delivery format argument must be a mapped parameter argument", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "convert_model_format").Processing.DeliveryFormatArgument = "source_task_id"
		}},
		{"delivery format argument is single-step only", func(a *catalog.ApplicationSchema) {
			tool := processingTool(t, a, "process_model")
			for argument := range tool.Processing.ParameterArguments {
				tool.Processing.DeliveryFormatArgument = argument
				break
			}
			if tool.Processing.DeliveryFormatArgument == "" {
				tool.Processing.DeliveryFormatArgument = "missing"
			}
		}},
		{"plan result content is required", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "plan_model_processing").Processing.Plan.WaitingContent = ""
		}},
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
				t.Fatal("ValidateApplicationSchema accepted an inconsistent processing declaration")
			}
		})
	}
}

func TestCrossDomainModelFallbackValidationRejectsUnsafeDeclarations(t *testing.T) {
	find := func(a *catalog.ApplicationSchema, name string) *catalog.ChatToolSchema {
		for index := range a.Chat.Tools {
			if a.Chat.Tools[index].Name == name {
				return &a.Chat.Tools[index]
			}
		}
		t.Fatalf("tool %s missing", name)
		return nil
	}
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"fallback needs the user-model requirement", func(a *catalog.ApplicationSchema) {
			find(a, "create_model_3d").RequireUserModel = false
		}},
		{"fallback needs exactly one domain", func(a *catalog.ApplicationSchema) {
			find(a, "create_model_3d").Execution.Domains = []string{"image", "model-3d"}
		}},
		{"fallback must be disclosed to the Agent", func(a *catalog.ApplicationSchema) {
			find(a, "create_model_3d").ResultFields.ModelFallback = ""
		}},
		{"fallback needs the model name result key", func(a *catalog.ApplicationSchema) {
			find(a, "create_model_3d").ResultFields.Model = ""
		}},
		{"domain models field is required", func(a *catalog.ApplicationSchema) {
			a.Chat.API.Fields.DomainModels = ""
		}},
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
				t.Fatal("ValidateApplicationSchema accepted an unsafe fallback declaration")
			}
		})
	}
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range c.Application().Public().Chat.Tools {
		if tool.Name == "create_model_3d" && tool.WorkspaceDomain != "model-3d" {
			t.Fatalf("public tool must name its workspace domain, got %q", tool.WorkspaceDomain)
		}
	}
}
