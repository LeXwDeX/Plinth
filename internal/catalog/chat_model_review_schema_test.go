package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// modelReviewApplication loads the shipped Schema; the caller may edit the
// returned copy and validate it.
func modelReviewApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application()
}

func modelReviewFix(app *catalog.ApplicationSchema, check string) *catalog.ChatModelInspectFixSchema {
	for index := range app.Chat.ModelReview.Inspect.Production.Fixes {
		fix := &app.Chat.ModelReview.Inspect.Production.Fixes[index]
		if fix.Check == check {
			return fix
		}
	}
	return nil
}

// TestShippedModelReviewToolsAreReadOnlySessionBound3D declares the contract
// the Agent relies on: the three review tools never create a Task, never ask
// for consent and are offered only in 3D scope.
func TestShippedModelReviewToolsAreReadOnlySessionBound3D(t *testing.T) {
	app := modelReviewApplication(t)
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	implementations := map[string]string{
		"inspect_model":      "model.inspect",
		"render_model_views": "model.render",
		"compare_models":     "model.compare",
	}
	found := map[string]bool{}
	for _, tool := range app.Chat.AgentTools {
		implementation, expected := implementations[tool.Name]
		if !expected {
			continue
		}
		found[tool.Name] = true
		execution := tool.Execution
		if execution.ImplementationKey != implementation {
			t.Errorf("%s implementation_key = %q, want %q", tool.Name, execution.ImplementationKey, implementation)
		}
		if len(execution.Effects) != 1 || execution.Effects[0] != "READ" {
			t.Errorf("%s effects = %v, want [READ]", tool.Name, execution.Effects)
		}
		if execution.Scope != "session" || execution.AgentScope != "3d" {
			t.Errorf("%s scope/agent_scope = %q/%q, want session/3d", tool.Name, execution.Scope, execution.AgentScope)
		}
		if len(execution.Domains) != 1 || execution.Domains[0] != "model-3d" || len(execution.Capabilities) != 0 {
			t.Errorf("%s domains/capabilities = %v/%v", tool.Name, execution.Domains, execution.Capabilities)
		}
		if execution.Consent != "none" || execution.Idempotency != "none" || execution.Cancellation != "none" {
			t.Errorf("%s consent/idempotency/cancellation = %q/%q/%q",
				tool.Name, execution.Consent, execution.Idempotency, execution.Cancellation)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		var parameters struct {
			Type                 string          `json:"type"`
			AdditionalProperties *bool           `json:"additionalProperties"`
			Properties           json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Errorf("%s parameters: %v", tool.Name, err)
			continue
		}
		if parameters.Type != "object" || parameters.AdditionalProperties == nil || *parameters.AdditionalProperties ||
			len(parameters.Properties) == 0 {
			t.Errorf("%s parameters must be a closed object with properties: %s", tool.Name, tool.Parameters)
		}
	}
	for name := range implementations {
		if !found[name] {
			t.Errorf("the shipped Schema does not declare %s", name)
		}
	}
	if !containsString(app.Chat.Harness.BaseTools, "inspect_model") ||
		!containsString(app.Chat.Harness.BaseTools, "render_model_views") ||
		!containsString(app.Chat.Harness.BaseTools, "compare_models") {
		t.Fatalf("harness base_tools = %v; the review tools belong to the basic harness", app.Chat.Harness.BaseTools)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestShippedModelReviewBoundsAreConsistent pins the shipped defaults and the
// relations the validator relies on.
func TestShippedModelReviewBoundsAreConsistent(t *testing.T) {
	review := modelReviewApplication(t).Chat.ModelReview
	if review.Render.Size != 768 || review.Render.MaxSize != 1024 || review.Render.Supersample != 2 ||
		review.Render.WireframeMaxTriangles != 200000 || review.Render.Workers != 4 ||
		review.Render.MaxConcurrentRenders != 2 || review.Render.MaxViewsPerCall != 6 ||
		review.Render.MaxRenderCallsPerTurn != 6 {
		t.Fatalf("render defaults = %+v", review.Render)
	}
	if review.Inspect.Limits.MaxBytes != 268435456 || review.Inspect.Limits.MaxTriangles != 8000000 ||
		review.Inspect.DebrisRatio != 0.001 || review.Inspect.WeldEpsilonRatio != 1e-6 ||
		review.Inspect.MaxListedParts != 50 {
		t.Fatalf("inspect defaults = %+v", review.Inspect)
	}
	production := review.Inspect.Production
	if production.PivotBottomCenterRatio != 0.01 || production.YUpFlatRatio != 0.05 ||
		production.DebrisRatio != 0.01 || production.DegenerateRatio != 0.001 {
		t.Fatalf("production thresholds = %+v", production)
	}
	if len(production.Fixes) != len(catalog.ModelReviewUsageCheckIDs()) {
		t.Fatalf("production fixes = %d, want one per usage check", len(production.Fixes))
	}
	if len(review.Render.DefaultViews) != 3 || review.Render.DefaultViews[0] != "front" ||
		review.Render.DefaultViews[1] != "back" || review.Render.DefaultViews[2] != "three_quarter" {
		t.Fatalf("default views = %v", review.Render.DefaultViews)
	}
	if review.Compare.Layout != "side_by_side" || review.Compare.MaxViewsPerCall > review.Render.MaxViewsPerCall {
		t.Fatalf("compare = %+v", review.Compare)
	}
	if review.ResultViewsField == "" || review.Cache.MaxModels <= 0 || review.Cache.MaxBytes <= 0 {
		t.Fatalf("cache/result field = %+v/%q", review.Cache, review.ResultViewsField)
	}
	if review.Render.MaxSize*review.Render.Supersample > modelinspect.MaxRenderDimension {
		t.Fatalf("max render dimension exceeds %d", modelinspect.MaxRenderDimension)
	}
}

func TestModelReviewDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"no byte limit", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Inspect.Limits.MaxBytes = 0 }, "inspect"},
		{"no triangle limit", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Inspect.Limits.MaxTriangles = -1 }, "inspect"},
		{"no weld ratio", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Inspect.WeldEpsilonRatio = 0 }, "inspect"},
		{"no parts bound", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Inspect.MaxListedParts = 0 }, "inspect"},
		{"no pivot tolerance", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.PivotBottomCenterRatio = 0
		}, "pivot_bottom_center_ratio"},
		{"pivot tolerance above one", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.PivotBottomCenterRatio = 1.1
		}, "pivot_bottom_center_ratio"},
		{"flat ratio one", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.YUpFlatRatio = 1
		}, "y_up_flat_ratio"},
		{"negative debris ratio", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.DebrisRatio = -0.1
		}, "debris_ratio"},
		{"degenerate ratio above one", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.DegenerateRatio = 1.1
		}, "degenerate_ratio"},
		{"empty default purpose text", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.DefaultPurposeText = " "
		}, "default_purpose_text"},
		{"default purpose text lacks the purpose", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.DefaultPurposeText = "按默认用途判定。"
		}, "{purpose}"},
		{"brief purpose text lacks the purpose", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.BriefPurposeText = "按制作简报判定。"
		}, "{purpose}"},
		{"triangle override text lacks the value", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.TriangleOverrideText = "按简报预算判定。"
		}, "{value}"},
		{"empty no-capability text", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.NoCapabilityText = ""
		}, "no_capability_text"},
		{"no fixes", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.Fixes = nil
		}, "缺少检查"},
		{"unknown fix check", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Inspect.Production.Fixes[0].Check = "unknown"
		}, "未知检查"},
		{"duplicate fix check", func(a *catalog.ApplicationSchema) {
			fixes := a.Chat.ModelReview.Inspect.Production.Fixes
			a.Chat.ModelReview.Inspect.Production.Fixes = append(fixes, fixes[0])
		}, "重复"},
		{"dynamic fix without capabilities", func(a *catalog.ApplicationSchema) {
			modelReviewFix(a, "triangles").Template = "可用能力降低面数。"
		}, "{capabilities}"},
		{"static fix with a capability placeholder", func(a *catalog.ApplicationSchema) {
			fix := modelReviewFix(a, "texture_size")
			fix.Template = "可用 {capabilities} 降低贴图。"
		}, "{capabilities}"},
		{"fix option without capabilities", func(a *catalog.ApplicationSchema) {
			modelReviewFix(a, "triangles").Options[0].Capabilities = nil
		}, "没有能力"},
		{"fix capability not declared", func(a *catalog.ApplicationSchema) {
			modelReviewFix(a, "triangles").Options[0].Capabilities[0].Capability = "missing"
		}, "未声明"},
		{"fix parameter without value", func(a *catalog.ApplicationSchema) {
			candidate := &modelReviewFix(a, "triangles").Options[0].Capabilities[0]
			candidate.Parameter = "origin_at"
			candidate.Value = ""
		}, "同时为空或同时非空"},
		{"scale fix lacks the range", func(a *catalog.ApplicationSchema) {
			modelReviewFix(a, "plausible_scale").Template = "可用 {capabilities} 调整尺度。"
		}, "{minimum}"},
		{"default size above max", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.Size = a.Chat.ModelReview.Render.MaxSize + 1
		}, "size"},
		{"no supersample", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.Supersample = 0 }, "render"},
		{"supersampled size above the renderer ceiling", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.MaxSize = modelinspect.MaxRenderDimension
			a.Chat.ModelReview.Render.Supersample = 2
		}, "supersample"},
		{"no views", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.Views = nil }, "views"},
		{"unknown view", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.Views = append(a.Chat.ModelReview.Render.Views, "under")
		}, "未知"},
		{"duplicate view", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.Views = append(a.Chat.ModelReview.Render.Views, a.Chat.ModelReview.Render.Views[0])
		}, "重复"},
		{"default view not declared", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.DefaultViews = []string{"front", "under"}
		}, "default_views"},
		{"no modes", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.Modes = nil }, "modes"},
		{"unknown mode", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Render.Modes = append(a.Chat.ModelReview.Render.Modes, "xray")
		}, "未知"},
		{"default mode not declared", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.DefaultMode = "xray" }, "default_mode"},
		{"no asset type", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.AssetType = " " }, "asset_type"},
		{"background is not a colour", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.Background = "grey" }, "background"},
		{"no workers", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Render.Workers = 0 }, "render"},
		{"compare layout is not implemented", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Compare.Layout = "overlay" }, "side_by_side"},
		{"compare renders more views than render", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Compare.MaxViewsPerCall = a.Chat.ModelReview.Render.MaxViewsPerCall + 1
		}, "compare"},
		{"no cache", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Cache.MaxModels = 0 }, "cache"},
		{"no result views field", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.ResultViewsField = "" }, "result_views_field"},
		{"source text is empty", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Source.SourceNotReady = "" }, "source"},
		{"not-model text lacks the asset", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Source.SourceNotModel = "not a model" }, "{asset}"},
		{"not-glb text lacks the media type", func(a *catalog.ApplicationSchema) {
			a.Chat.ModelReview.Source.SourceNotGLB = "not a GLB: {asset}"
		}, "{media}"},
		{"too-large text lacks the limit", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.TooLarge = "too large {bytes}" }, "too_large"},
		{"unknown-part text lacks the part", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.UnknownPart = "no such part" }, "unknown_part"},
		{"source-read text lacks the reason", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.SourceReadFailed = "unreadable" }, "source_read_failed"},
		{"upload text lacks the reason", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.UploadFailed = "not stored" }, "upload_failed"},
		{"empty vision note", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.VisionOff = " " }, "vision_off"},
		{"empty parts-required text", func(a *catalog.ApplicationSchema) { a.Chat.ModelReview.Texts.PartsRequired = "" }, "parts_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := modelReviewApplication(t)
			tt.mutate(&app)
			err := catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid model_review declaration")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want mention of %q", err, tt.want)
			}
		})
	}
}

// TestModelReviewIsOnlyValidatedWhenAToolUsesIt keeps unrelated Schemas free
// to omit the block, as they did before the tools existed.
func TestModelReviewIsOnlyValidatedWhenAToolUsesIt(t *testing.T) {
	app := modelReviewApplication(t)
	var remaining []catalog.ChatAgentToolSchema
	for _, tool := range app.Chat.AgentTools {
		switch tool.Execution.ImplementationKey {
		case "model.inspect", "model.render", "model.compare":
			continue
		}
		remaining = append(remaining, tool)
	}
	app.Chat.AgentTools = remaining
	// A craft brief may only trigger on a tool the Schema declares; the review
	// trigger goes with the review tools.
	for index := range app.Chat.Agent.CraftPrompts {
		var triggers []string
		for _, name := range app.Chat.Agent.CraftPrompts[index].WhenToolsAny {
			switch name {
			case "compare_models", "inspect_model", "render_model_views":
				continue
			}
			triggers = append(triggers, name)
		}
		app.Chat.Agent.CraftPrompts[index].WhenToolsAny = triggers
	}
	var base []string
	for _, name := range app.Chat.Harness.BaseTools {
		if name == "inspect_model" || name == "render_model_views" || name == "compare_models" {
			continue
		}
		base = append(base, name)
	}
	app.Chat.Harness.BaseTools = base
	app.Chat.ModelReview = catalog.ChatModelReviewSchema{}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Schema without the review tools was rejected: %v", err)
	}
}
