package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// shippedToolScopes is the classification the product documents
// (docs/product.md). A new Agent tool fails this test until someone decides
// which scope may use it, in the Schema and here.
var shippedToolScopes = map[string]string{
	"create_motion_clip":         "3d",
	"refine_image":               "2d",
	"remove_background":          "2d",
	"create_three_view":          "2d",
	"create_model_3d":            "3d",
	"create_model_3d_from_image": "3d",
	"create_gaussian_splat":      "3d",
	"create_multiview_images":    "2d",
	"edit_multiview_images":      "2d",
	"retexture_model":            "3d",
	"process_model":              "3d",
	"plan_model_processing":      "3d",
	"get_processing_plan":        "3d",
	"convert_model_format":       "3d",
	"list_session_models":        "3d",
	"set_params":                 "utility",
	"create_creative_lab_design": "3d",
	"build_creative_lab_item":    "3d",
	"update_session_notes":       "utility",
	"find_history_images":        "utility",
	"recover_task_result":        "utility",
	"list_offerings":             "utility",
	"get_task":                   "utility",
	"observe_task":               "utility",
	"request_consent":            "utility",
	"mcp_list_offerings":         "utility",
	"mcp_list_session_tasks":     "utility",
	"mcp_get_task_assets":        "utility",
}

func TestEveryShippedAgentToolDeclaresItsScopeClass(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	seen := map[string]string{}
	for _, tool := range app.Chat.Tools {
		seen[tool.Name] = tool.Execution.AgentScope
	}
	for _, tool := range app.Chat.AgentTools {
		seen[tool.Name] = tool.Execution.AgentScope
	}
	for _, preset := range app.AgentConfiguration.MCPPresets {
		for _, tool := range preset.Tools {
			seen[tool.Name] = tool.Execution.AgentScope
		}
	}
	for name, class := range seen {
		want, known := shippedToolScopes[name]
		if !known {
			t.Errorf("tool %q has no documented scope class (declared %q)", name, class)
			continue
		}
		if class != want {
			t.Errorf("tool %q scope class = %q, want %q", name, class, want)
		}
	}
	for name := range shippedToolScopes {
		if _, declared := seen[name]; !declared {
			t.Errorf("documented tool %q is not in the Schema", name)
		}
	}
}

func TestAgentScopeDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"a generation tool without a class", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.AgentScope = "" }, "agent_scope"},
		{"a control tool without a class", func(a *catalog.ApplicationSchema) { a.Chat.AgentTools[0].Execution.AgentScope = "" }, "agent_scope"},
		{"an unknown class", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.AgentScope = "4d" }, "agent_scope"},
		{"a 3d class on an image tool", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.AgentScope = "3d" }, "矛盾"},
		{"an MCP tool without a class", func(a *catalog.ApplicationSchema) {
			a.AgentConfiguration.MCPPresets[0].Tools[0].Execution.AgentScope = ""
		}, "agent_scope"},
		{"no modes", func(a *catalog.ApplicationSchema) { a.Chat.AgentScope.Modes = nil }, "modes"},
		{"an unknown mode", func(a *catalog.ApplicationSchema) { a.Chat.AgentScope.Modes[0].ID = "4d" }, "4d"},
		{"a default the Schema does not offer", func(a *catalog.ApplicationSchema) { a.Chat.AgentScope.DefaultMode = "manual" }, "default_mode"},
		{"a mode that drops utility tools", func(a *catalog.ApplicationSchema) {
			a.Chat.AgentScope.Modes[0].Allows = []string{"2d"}
		}, "utility"},
		{"auto that hides cross tools", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentScope.Modes {
				if a.Chat.AgentScope.Modes[index].ID == "auto" {
					a.Chat.AgentScope.Modes[index].Allows = []string{"2d", "3d", "utility"}
				}
			}
		}, "auto"},
		{"a workspace domain that contradicts the allowed classes", func(a *catalog.ApplicationSchema) {
			a.Chat.AgentScope.Modes[0].WorkspaceDomains = []string{"image", "model-3d"}
		}, "workspace_domains"},
		{"a context template without the tool lists", func(a *catalog.ApplicationSchema) {
			a.Chat.AgentScope.ContextTemplate = "{mode} {description}"
		}, "context_template"},
		{"a rejection without the tool name", func(a *catalog.ApplicationSchema) {
			a.Chat.AgentScope.RejectionTemplate = "refused in {mode}"
		}, "rejection_template"},
		{"no scope route parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionAgentScope = "/api/v1/chat/agent-scope"
		}, "sessionID"},
		{"no request field", func(a *catalog.ApplicationSchema) { a.Chat.API.Fields.AgentScope = "" }, "agent_scope"},
		{"no audit action", func(a *catalog.ApplicationSchema) { a.Chat.Audit.AgentScopeAction = "" }, "agent_scope_action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want mention of %q", err, tt.want)
			}
		})
	}
}

func TestPublicSchemaExposesScopeModesButNotTheClassification(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	public := c.Application().Public()
	if public.Chat.AgentScope.DefaultMode != "auto" || len(public.Chat.AgentScope.Modes) != 3 {
		t.Fatalf("public agent scope = %+v", public.Chat.AgentScope)
	}
	byID := map[string][]string{}
	for _, mode := range public.Chat.AgentScope.Modes {
		byID[mode.ID] = mode.WorkspaceDomains
	}
	if len(byID["2d"]) != 1 || byID["2d"][0] != "image" || len(byID["3d"]) != 1 || byID["3d"][0] != "model-3d" || len(byID["auto"]) != 2 {
		t.Fatalf("workspace domains by mode = %v", byID)
	}
	if public.Runtime.HTTP.Routes.ChatSessionAgentScope == "" || public.Chat.API.ResponseFields.AgentScope == "" || public.Chat.API.Fields.AgentScope == "" {
		t.Fatal("the client needs the scope route and members")
	}
}
