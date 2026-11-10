package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func selfCheckApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application()
}

func selfCheckTool(app catalog.ApplicationSchema, name string) (catalog.ChatAgentToolSchema, bool) {
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == name {
			return tool, true
		}
	}
	return catalog.ChatAgentToolSchema{}, false
}

func TestShippedSelfCheckContractIsComplete(t *testing.T) {
	app := selfCheckApplication(t)
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	selfCheck := app.Chat.SelfCheck
	if selfCheck.Tools.Record != "record_self_check" || selfCheck.Tools.ReferenceCompare != "compare_model_reference" {
		t.Fatalf("tools = %+v", selfCheck.Tools)
	}
	if selfCheck.Limits.MaxRoundsPerResult <= 0 || selfCheck.Limits.MaxCorrectionsPerResult <= 0 ||
		selfCheck.Limits.MaxRendersPerRound <= 0 {
		t.Fatalf("limits = %+v", selfCheck.Limits)
	}
	roles := map[string]string{}
	for _, status := range selfCheck.Statuses {
		roles[status.Role] = status.ID
		if status.ID == "" || status.Label == "" {
			t.Fatalf("status = %+v", status)
		}
	}
	if roles["unchecked"] == "" || roles["passed"] == "" || roles["issues"] == "" {
		t.Fatalf("roles = %v", roles)
	}
	if !selfCheck.RequiredChecks.Inspect || !selfCheck.RequiredChecks.RenderOrReference {
		t.Fatalf("required checks = %+v", selfCheck.RequiredChecks)
	}
	render := app.Chat.ModelReview.Render
	for _, view := range selfCheck.Reference.Views {
		if !containsString(render.Views, view) {
			t.Fatalf("reference view %q is not a render view %v", view, render.Views)
		}
	}
	if selfCheck.Reference.AssetType != render.AssetType || selfCheck.Reference.Layout != "side_by_side" {
		t.Fatalf("reference = %+v", selfCheck.Reference)
	}
	if len(selfCheck.Checkpoints) == 0 || selfCheck.Checkpoints[0].ID == "" {
		t.Fatalf("checkpoints = %+v", selfCheck.Checkpoints)
	}
	if !containsString(app.Chat.Harness.BaseTools, "compare_model_reference") ||
		!containsString(app.Chat.Harness.BaseTools, "record_self_check") {
		t.Fatalf("harness base_tools = %v", app.Chat.Harness.BaseTools)
	}

	reference, found := selfCheckTool(app, "compare_model_reference")
	if !found || reference.Execution.ImplementationKey != "model.compare_reference" ||
		len(reference.Execution.Effects) != 1 || reference.Execution.Effects[0] != "READ" ||
		reference.Execution.Scope != "session" || reference.Execution.AgentScope != "3d" {
		t.Fatalf("compare tool = %+v", reference.Execution)
	}
	record, found := selfCheckTool(app, "record_self_check")
	if !found || record.Execution.ImplementationKey != "self_check.record" ||
		len(record.Execution.Effects) != 1 || record.Execution.Effects[0] != "SESSION_WRITE" ||
		record.Execution.Scope != "session" || record.Execution.AgentScope != "3d" {
		t.Fatalf("record tool = %+v", record.Execution)
	}
	var parameters struct {
		AdditionalProperties *bool    `json:"additionalProperties"`
		Required             []string `json:"required"`
	}
	if err := json.Unmarshal(record.Parameters, &parameters); err != nil {
		t.Fatalf("record parameters: %v", err)
	}
	if parameters.AdditionalProperties == nil || *parameters.AdditionalProperties {
		t.Fatalf("record parameters must be closed: %s", record.Parameters)
	}
	for _, required := range parameters.Required {
		if required == "model" {
			t.Fatalf("model is optional (the selected model), not required: %v", parameters.Required)
		}
	}
}

func TestPublicSelfCheckProjectionKeepsOnlyTheBrowserContract(t *testing.T) {
	public := selfCheckApplication(t).Public()
	if public.Chat.SelfCheck == nil {
		t.Fatal("the public Schema lost self_check")
	}
	encoded, err := json.Marshal(public.Chat.SelfCheck)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"subject_threshold", "normalized_side", "context_template", "unchecked_rule_text",
		"record_needs_check", "max_summary_characters", "max_pairs_per_call"} {
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("the public projection leaks %q: %s", hidden, encoded)
		}
	}
	if len(public.Chat.SelfCheck.Statuses) != 3 || public.Chat.SelfCheck.Limits.MaxRoundsPerResult <= 0 {
		t.Fatalf("public self_check = %+v", public.Chat.SelfCheck)
	}
	// #246: the plan card renders the declared label, so the checkpoint ids
	// and labels are public even though the rest of the record stays hidden.
	checkpoints := public.Chat.SelfCheck.Checkpoints
	if len(checkpoints) == 0 || checkpoints[0].ID == "" || checkpoints[0].Label == "" {
		t.Fatalf("public checkpoints = %+v", checkpoints)
	}
}

func TestSelfCheckValidationRefusesEachBrokenContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"zero limits", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Limits.MaxRoundsPerResult = 0 }, "limits"},
		{"missing role", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Statuses = app.Chat.SelfCheck.Statuses[:2] }, "缺少"},
		{"duplicate role", func(app *catalog.ApplicationSchema) {
			app.Chat.SelfCheck.Statuses[1].Role = app.Chat.SelfCheck.Statuses[0].Role
		}, "role"},
		{"unknown role", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Statuses[0].Role = "done" }, "未知 role"},
		{"reference view not rendered", func(app *catalog.ApplicationSchema) {
			app.Chat.SelfCheck.Reference.Views = append(app.Chat.SelfCheck.Reference.Views, "bottom")
		}, "未声明"},
		{"default view outside views", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Reference.DefaultView = "bottom" }, "default_view"},
		{"mode not rendered", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Reference.Mode = "oil" }, "mode"},
		{"size above render", func(app *catalog.ApplicationSchema) {
			app.Chat.SelfCheck.Reference.Size = app.Chat.ModelReview.Render.MaxSize + 1
		}, "max_size"},
		{"asset type differs", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Reference.AssetType = "image" }, "asset_type"},
		{"layout", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Reference.Layout = "stacked" }, "side_by_side"},
		{"pairs above compare", func(app *catalog.ApplicationSchema) {
			app.Chat.SelfCheck.Reference.MaxPairsPerCall = app.Chat.ModelReview.Compare.MaxViewsPerCall + 1
		}, "max_pairs_per_call"},
		{"unchecked rule missing", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Context.UncheckedRuleText = "" }, "context"},
		{"context placeholder missing", func(app *catalog.ApplicationSchema) {
			app.Chat.SelfCheck.Context.ContextTemplate = "no placeholders"
		}, "context"},
		{"text placeholder missing", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Texts.ReferenceReady = "ready" }, "reference_ready"},
		{"conclusion bound", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Conclusion.MaxItems = 0 }, "conclusion"},
		{"checkpoints", func(app *catalog.ApplicationSchema) { app.Chat.SelfCheck.Checkpoints = nil }, "checkpoints"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			app := selfCheckApplication(t)
			tt.mutate(&app)
			err := catalog.ValidateApplicationSchema(app)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestSelfCheckValidationIsOffWithoutItsTools(t *testing.T) {
	app := selfCheckApplication(t)
	var kept []catalog.ChatAgentToolSchema
	for _, tool := range app.Chat.AgentTools {
		switch tool.Execution.ImplementationKey {
		case "model.compare_reference", "self_check.record":
			continue
		}
		kept = append(kept, tool)
	}
	app.Chat.AgentTools = kept
	var harness []string
	for _, name := range app.Chat.Harness.BaseTools {
		if name != "compare_model_reference" && name != "record_self_check" {
			harness = append(harness, name)
		}
	}
	app.Chat.Harness.BaseTools = harness
	for index := range app.Chat.Agent.CraftPrompts {
		prompt := &app.Chat.Agent.CraftPrompts[index]
		prompt.Text = strings.ReplaceAll(prompt.Text, "compare_model_reference", "inspect_model")
		prompt.Text = strings.ReplaceAll(prompt.Text, "record_self_check", "inspect_model")
		var when []string
		for _, name := range prompt.WhenToolsAny {
			if name != "compare_model_reference" && name != "record_self_check" {
				when = append(when, name)
			}
		}
		prompt.WhenToolsAny = when
	}
	app.Chat.SelfCheck.Limits.MaxRoundsPerResult = 0
	app.Chat.SelfCheck.Statuses = nil
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Catalog without the self-check tools must validate: %v", err)
	}
}
