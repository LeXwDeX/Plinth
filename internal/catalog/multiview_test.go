package catalog

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"reflect"
	"strings"
	"testing"
)

func TestMeshyMultiviewHasAnIndependentFrozenContract(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("meshy/meshy-7.1-multiview/image-to-3d")
	o, ok := c.Get(id)
	if !ok || !o.Enabled || o.ReferenceSelectionPolicy != "preserve" || o.NativePricing == nil || o.NativePricing.Unit != "meshy.credit" {
		t.Fatalf("candidate=%+v", o)
	}
	if err := meshy.ValidateOffering(o); err != nil {
		t.Fatal(err)
	}
	old, _ := c.Get("meshy/meshy-7.1/image-to-3d")
	if old.ModelID == o.ModelID || old.ReferenceSelectionPolicy != "" {
		t.Fatal("legacy single-view identity changed")
	}
	for _, p := range old.Parameters {
		if p.Name == "geometry_resolution" && string(p.Default) != `"4k"` {
			t.Fatal("legacy geometry downgraded")
		}
	}
	slots := map[string]bool{}
	for _, output := range o.Outputs {
		if output.Role == "model" && output.Minimum != nil && *output.Minimum == 1 {
			slots[output.Variant] = true
			if output.Delivery != "required" {
				t.Fatal("mandatory model slot is not required")
			}
			if len(output.MediaTypes) != 1 {
				t.Fatal("missing MIME")
			}
			if output.Variant == "fbx" && output.DeliveryProfile != schema.DeliveryProfileBinaryFBXV1 {
				t.Fatal("FBX lacks byte profile")
			}
			if output.Variant == "3mf" && output.DeliveryProfile != schema.DeliveryProfile3MFPackageV1 {
				t.Fatal("3MF lacks byte profile")
			}
		}
	}
	for _, variant := range []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"} {
		if !slots[variant] {
			t.Fatalf("missing mandatory %s", variant)
		}
	}
	for _, p := range o.Parameters {
		if p.Type == "task" {
			t.Fatal("unqualified native Meshy image Task source advertised")
		}
		if p.Name == "reference_assets" && (p.MaxItems != 4 || p.MinItems != 1 || p.MaxBytes != 20_000_000 || p.WireName != "image_urls") {
			t.Fatal("source bounds not explicit")
		}
	}
	_, raw, err := c.Normalize(schema.ImageTo3D, id, []byte(`{"reference_assets":["front","back","left"],"authorize_extra_credits":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]json.RawMessage
	_ = json.Unmarshal(raw, &input)
	if string(input["reference_assets"]) != `["front","back","left"]` || string(input["geometry_resolution"]) != `"2k"` || string(input["texture_resolution"]) != `"8k"` {
		t.Fatalf("input=%s", raw)
	}
	if _, _, err = c.Normalize(schema.ImageTo3D, id, []byte(`{"reference_assets":["1","2","3","4","5"]}`)); err == nil {
		t.Fatal("over-limit views accepted")
	}
}

// 每个必需模型槽位都必须由 model_urls.<variant> 的标量投影声明精确 MIME，
// 不依赖 URL 后缀、宿主机 MIME 表或 CDN 的通用/错标响应。真实多图任务曾因
// 依赖推断在物化阶段以 integrity_failure 失败（#191）。
func TestMeshyMultiviewDeclaresExactDeliveryMIMEForEveryModelSlot(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("multiview offering missing")
	}
	type projection struct {
		Path      string `json:"path"`
		Role      string `json:"role"`
		Mode      string `json:"mode"`
		Variant   string `json:"variant"`
		MediaType string `json:"media_type"`
	}
	var options struct {
		ResultAssets []projection `json:"result_assets"`
	}
	if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	for _, output := range o.Outputs {
		if output.Role != "model" || output.Minimum == nil || *output.Minimum != 1 {
			continue
		}
		if len(output.MediaTypes) != 1 {
			t.Fatalf("model slot %q must declare exactly one media type", output.Variant)
		}
		matched := 0
		for _, candidate := range options.ResultAssets {
			if candidate.Path != "model_urls."+output.Variant {
				continue
			}
			matched++
			if candidate.Mode != "scalar" || candidate.Role != "model" || candidate.Variant != output.Variant || candidate.MediaType != output.MediaTypes[0] {
				t.Fatalf("projection %q lost the exact slot declaration: %+v", candidate.Path, candidate)
			}
		}
		if matched != 1 {
			t.Fatalf("model slot %q has %d scalar projections, want exactly 1", output.Variant, matched)
		}
	}
	for _, candidate := range options.ResultAssets {
		if candidate.Role == "model" && candidate.Mode != "scalar" {
			t.Fatalf("model projection %q mode %q still relies on inference", candidate.Path, candidate.Mode)
		}
	}
}

func TestReferenceSelectionPolicyRejectsUnknownOrUnboundedContracts(t *testing.T) {
	valid := schema.Offering{Capability: schema.ImageTo3D, ReferenceSelectionPolicy: "preserve", Parameters: []schema.Parameter{{Name: "images", Type: "asset-array", Semantic: schema.SemanticReferenceImage, MinItems: 1, MaxItems: 4}}}
	if err := validateReferenceSelectionPolicy(valid); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown", "capability", "missing", "scalar", "unbounded", "zero-minimum", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			o := valid
			o.Parameters = append([]schema.Parameter(nil), valid.Parameters...)
			switch kind {
			case "unknown":
				o.ReferenceSelectionPolicy = "latest"
			case "capability":
				o.Capability = schema.TextToImage
			case "missing":
				o.Parameters = nil
			case "scalar":
				o.Parameters[0].Type = "asset"
			case "unbounded":
				o.Parameters[0].MaxItems = 0
			case "zero-minimum":
				o.Parameters[0].MinItems = 0
			case "duplicate":
				o.Parameters = append(o.Parameters, o.Parameters[0])
			}
			if err := validateReferenceSelectionPolicy(o); err == nil || !strings.Contains(err.Error(), "Offering") {
				t.Fatalf("unsafe declaration=%+v error=%v", o, err)
			}
		})
	}
	// Empty policy is the historical contract, including old snapshots with no reference limits.
	if err := validateReferenceSelectionPolicy(schema.Offering{}); err != nil {
		t.Fatal(err)
	}
}

func TestMeshyMultiviewAdvisedPricesAndDeliveryBounds(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("meshy/meshy-7.1-multiview/image-to-3d")
	o, _ := c.Get(id)
	for _, p := range o.Parameters {
		if p.Presentation != nil && p.Presentation.Surface == schema.SurfaceReadOnly && (p.AdjustmentPolicy != "advised" || p.AdjustmentReason == "" || p.Fixed || o.UserParameterAllowed(p) || !o.AgentParameterAllowed(p)) {
			t.Fatalf("parameter policy: %+v", p)
		}
	}
	patch := map[string]json.RawMessage{"geometry_resolution": json.RawMessage(`"standard"`)}
	if _, err := ValidateParameterPatch(o, patch, false, nil); err == nil {
		t.Fatal("human changed advised geometry")
	}
	if _, err := ValidateParameterPatch(o, patch, true, nil); err == nil {
		t.Fatal("missing Agent reason accepted")
	}
	if _, err := ValidateParameterPatch(o, patch, true, map[string]string{"geometry_resolution": "先检查多视角草稿"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, input, total string }{
		{"highest", `{"reference_assets":["front"],"authorize_extra_credits":true}`, "40"},
		{"standard-eight-k", `{"reference_assets":["front"],"geometry_resolution":"standard","authorize_extra_credits":true}`, "35"},
		{"ultra-four-k", `{"reference_assets":["front"],"texture_resolution":"4k","authorize_extra_credits":true}`, "35"},
		{"standard-two-k", `{"reference_assets":["front"],"geometry_resolution":"standard","texture_resolution":"2k","authorize_extra_credits":true}`, "30"},
		{"ultra-mesh-only", `{"reference_assets":["front"],"should_texture":false,"authorize_extra_credits":true}`, "25"},
		{"standard-mesh-only", `{"reference_assets":["front"],"geometry_resolution":"standard","should_texture":false}`, "20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, input, err := c.Normalize(schema.ImageTo3D, id, []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			q, err := o.EvaluateNativeQuote(input)
			if err != nil || q.TotalAmount != tc.total || q.Unit != "meshy.credit" {
				t.Fatalf("input=%s quote=%+v err=%v", input, q, err)
			}
		})
	}
	for _, input := range []string{
		`{"reference_assets":["front"]}`,
		`{"reference_assets":["front"],"geometry_resolution":"4k","authorize_extra_credits":true}`,
		`{"reference_assets":["front"],"topology":"quad","authorize_extra_credits":true}`,
		`{"reference_assets":["front"],"target_formats":["glb"],"authorize_extra_credits":true}`,
	} {
		if _, _, err := c.Normalize(schema.ImageTo3D, id, []byte(input)); err == nil {
			t.Fatalf("unsafe input accepted: %s", input)
		}
	}
}

func TestMultiviewNativeModelDoesNotRequireOrAcceptLegacyIntegerMapping(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	configured := c.Application().ProviderDiscovery.LegacyModels
	native := ProviderDiscoveryLegacyModel{ModelID: "meshy-7.1-multiview", ProviderKey: "meshy-3d"}
	if err := validateProviderDiscoveryLegacyModels(append(configured, native), c.byID); err == nil {
		t.Fatal("native-only model accepted by integer compatibility mapping")
	}
	if err := validateProviderDiscoveryLegacyModels(configured[1:], c.byID); err == nil {
		t.Fatal("non-native mapping no longer required")
	}
}

func TestMultiviewNativeBalanceUnitMustMatchOffering(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtime := c.Application().Runtime
	account := runtime.ProviderAccounts["meshy"]
	account.Balance.NativeUnit = "different.credit"
	runtime.ProviderAccounts["meshy"] = account
	if err := validateNativeBalanceUnits(runtime, c.byID); err == nil {
		t.Fatal("wrong-unit balance accepted")
	}
	account.Balance.NativeUnit = "meshy.credit"
	account.Balance.MockNativeAmount = "0.0000001"
	runtime.ProviderAccounts["meshy"] = account
	if err := validateRuntime(runtime); err == nil {
		t.Fatal("inexact native mock accepted")
	}
}

func TestTripoImageGroupQuotesAndConditionalSlots(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	create, ok := c.Get("tripo/image-to-multiview")
	if !ok || !create.Enabled || create.Capability != schema.ImageToMultiview || len(create.Outputs) != 4 {
		t.Fatalf("create candidate: %+v", create)
	}
	q, err := create.EvaluateNativeQuote(json.RawMessage(`{"image":"image-1"}`))
	if err != nil || q.TotalAmount != "10" || q.Unit != "tripo.credit" {
		t.Fatalf("create quote: %+v %v", q, err)
	}
	edit, ok := c.Get("tripo/edit-multiview")
	if !ok || !edit.Enabled || edit.Capability != schema.EditMultiview || len(edit.Outputs) != 4 {
		t.Fatalf("edit candidate: %+v", edit)
	}
	for _, tc := range []struct {
		raw, amount string
		selected    []string
	}{
		{`{"source_task":"source","front_prompt":"green","authorize_extra_credits":true}`, "5", []string{"front"}},
		{`{"source_task":"source","front_prompt":"green","left_prompt":"green","back_prompt":"green","right_prompt":"green","authorize_extra_credits":true}`, "20", []string{"front", "left", "back", "right"}},
	} {
		input, err := normalizeInput(edit, []byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		q, err := edit.EvaluateNativeQuote(input)
		if err != nil || q.TotalAmount != tc.amount || len(q.Components) != len(tc.selected) {
			t.Fatalf("quote=%+v err=%v", q, err)
		}
		var values map[string]any
		if err := json.Unmarshal(input, &values); err != nil {
			t.Fatal(err)
		}
		for _, output := range edit.Outputs {
			wanted := false
			for _, view := range tc.selected {
				wanted = wanted || output.Variant == view
			}
			if output.RequiredWhen == nil || schema.ConditionMatches(*output.RequiredWhen, values) != wanted {
				t.Fatalf("conditional output=%+v", output)
			}
		}
	}
	for _, raw := range []string{
		`{"source_task":"source","authorize_extra_credits":true}`,
		`{"source_task":"source","front_prompt":"   ","authorize_extra_credits":true}`,
		`{"source_task":"source","front_prompt":"green"}`,
	} {
		if _, err := normalizeInput(edit, []byte(raw)); err == nil {
			t.Fatalf("invalid prompt or consent accepted: %s", raw)
		}
	}
	mixed, err := normalizeInput(edit, []byte(`{"source_task":"source","front_prompt":"green","left_prompt":" ","authorize_extra_credits":true}`))
	if err != nil {
		t.Fatal(err)
	}
	q, err = edit.EvaluateNativeQuote(mixed)
	if err != nil || q.TotalAmount != "5" {
		t.Fatalf("whitespace must not cost an edited view: %+v %v", q, err)
	}
}

func TestImageResultGroupCannotAdvertiseMissingOrConditionalSelectableView(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	create, _ := c.Get("tripo/image-to-multiview")
	create.Outputs[0].Variant = "top"
	if err := validateImageResultGroupOffering(app, create); err == nil {
		t.Fatal("public group advertised a nonexistent output slot")
	}
	create, _ = c.Get("tripo/image-to-multiview")
	create.Outputs[0].RequiredWhen = &schema.ParameterCondition{Parameter: "prompt", Operator: "present"}
	if err := validateImageResultGroupOffering(app, create); err == nil {
		t.Fatal("selectable source accepted a conditional view")
	}
}

func TestMultiviewPreservesTwoDimensionalManualApprovalPolicy(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	manual := c.Application().Chat.ApprovalPolicy.ManualApprovalCapabilities
	want := []schema.Capability{schema.TextTo3D, schema.ImageTo3D, schema.ImageToSplat, schema.Retexture}
	if !reflect.DeepEqual(manual, want) {
		t.Fatalf("manual capabilities changed: got %v want %v", manual, want)
	}
}

// Partial delivery of a public image group is a Schema declaration with its own
// copy: only a group that declares partial_message may carry conditional views,
// and a complete, source-selectable group may not declare one.
func TestImageResultGroupDeclaresPartialDeliveryAndItsCopy(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	groups := map[schema.Capability]*ImageResultGroupSchema{}
	for _, capability := range app.Capabilities {
		groups[capability.ID] = capability.ImageResultGroup
	}
	if groups[schema.ImageToMultiview] == nil || groups[schema.ImageToMultiview].PartialMessage != "" {
		t.Fatal("a generated four-view group must be complete")
	}
	edit := groups[schema.EditMultiview]
	if edit == nil || edit.PartialMessage == "" || app.Content.Messages[edit.PartialMessage] == "" {
		t.Fatalf("edit group must declare its partial copy: %+v", edit)
	}

	withoutCopy := cloneApplicationWithGroup(t, app, schema.EditMultiview, func(group *ImageResultGroupSchema) { group.PartialMessage = "" })
	offering, _ := c.Get("tripo/edit-multiview")
	if err := validateImageResultGroupOffering(withoutCopy, offering); err == nil {
		t.Fatal("conditional views accepted without a declared partial delivery")
	}
	for name, change := range map[string]func(*ImageResultGroupSchema){
		"selectable and partial": func(group *ImageResultGroupSchema) { group.SourceSelectable = true },
		"unknown copy":           func(group *ImageResultGroupSchema) { group.PartialMessage = "ui.studio.result.no_such_message" },
		"blank copy":             func(group *ImageResultGroupSchema) { group.PartialMessage = " " },
	} {
		if err := validateApplication(cloneApplicationWithGroup(t, app, schema.EditMultiview, change)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func cloneApplicationWithGroup(t *testing.T, app ApplicationSchema, capability schema.Capability, change func(*ImageResultGroupSchema)) ApplicationSchema {
	t.Helper()
	raw, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	var clone ApplicationSchema
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	for index := range clone.Capabilities {
		if clone.Capabilities[index].ID == capability {
			change(clone.Capabilities[index].ImageResultGroup)
		}
	}
	return clone
}
