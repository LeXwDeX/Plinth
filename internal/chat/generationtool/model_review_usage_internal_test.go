package generationtool

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

func usageInt(value int) *int { return &value }

func usageIdentityText(template string, replacements ...string) string {
	for index := 0; index+1 < len(replacements); index += 2 {
		template = strings.ReplaceAll(template, replacements[index], replacements[index+1])
	}
	return template
}

// usageBaseUsage is a small purpose whose bounds are easy to cross in a test:
// 10–20 triangles, UV, watertight, skin, 1024 px textures and 1–2 m.
func usageBaseUsage() catalog.ChatModel3DUsageSchema {
	return catalog.ChatModel3DUsageSchema{
		DefaultProfile: "game",
		Profiles: []catalog.ChatModel3DUsageProfile{
			{
				ID: "game", Label: "游戏角色", Description: "d", Reason: "r",
				Budget: catalog.ChatModel3DUsageBudget{
					MinTriangles: usageInt(10), MaxTriangles: 20,
					MaxTextureSize: usageInt(1024),
					RequireUV:      true, RequireWatertight: true, RequireSkin: true,
				},
				ScaleRangeM: &catalog.ChatModel3DUsageScaleRange{Min: 1, Max: 2},
			},
			{
				ID: "prop", Label: "道具", Description: "d", Reason: "r",
				Budget:      catalog.ChatModel3DUsageBudget{MaxTriangles: 20},
				ScaleRangeM: &catalog.ChatModel3DUsageScaleRange{Min: 0.02, Max: 20},
			},
		},
		Brief: catalog.ChatProductionBriefSchema{Fields: []catalog.ChatProductionBriefField{
			{Name: "purpose", Type: "profile"},
			{Name: "triangle_budget", Type: "integer"},
		}},
	}
}

// usageBaseReport meets every bound of usageBaseUsage.
func usageBaseReport() modelinspect.Report {
	return modelinspect.Report{
		Triangles: 15,
		Materials: []modelinspect.MaterialReport{},
		UV:        modelinspect.UVReport{AllHaveUV: true},
		Skins:     []modelinspect.SkinReport{{Index: 0, Joints: 2}},
		BBox: modelinspect.BBoxReport{
			Size:               [3]float64{1, 1.5, 0.5},
			BottomCenterOffset: [3]float64{0, 0, 0},
		},
		Topology: modelinspect.TopologyReport{Watertight: true},
		Warnings: []string{},
	}
}

func usageProduction(t *testing.T) catalog.ChatModelInspectProductionSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat.ModelReview.Inspect.Production
}

func usageItem(t *testing.T, check *modelReviewUsageCheck, id string) modelReviewUsageCheckItem {
	t.Helper()
	if check == nil {
		t.Fatalf("no usage check was produced, want %q", id)
	}
	for _, item := range check.Checks {
		if item.Check == id {
			return item
		}
	}
	t.Fatalf("usage check has no %q item", id)
	return modelReviewUsageCheckItem{}
}

// TestModelReviewUsageChecksReportPassFailAndUnknown covers all ten checks:
// a value inside the bound passes, a value outside it fails (with a fix hint),
// and a property the purpose does not request or the file cannot support is
// unknown (without a hint).
func TestModelReviewUsageChecksReportPassFailAndUnknown(t *testing.T) {
	production := usageProduction(t)
	allAvailable := func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true }
	tests := []struct {
		name   string
		check  string
		want   string
		mutate func(*modelinspect.Report, *catalog.ChatModel3DUsageSchema)
	}{
		{name: "triangles inside the range", check: catalog.ModelReviewUsageCheckTriangles, want: modelReviewUsageStatusPass},
		{
			name: "triangles below the minimum", check: catalog.ModelReviewUsageCheckTriangles,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Triangles = 9
			},
		},
		{
			name: "triangles above the maximum", check: catalog.ModelReviewUsageCheckTriangles,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Triangles = 21
			},
		},
		{
			name: "an unreadable triangle count", check: catalog.ModelReviewUsageCheckTriangles,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Warnings = []string{"read_error"}
			},
		},
		{name: "UV present", check: catalog.ModelReviewUsageCheckUV, want: modelReviewUsageStatusPass},
		{
			name: "UV missing", check: catalog.ModelReviewUsageCheckUV,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.UV.AllHaveUV = false
			},
		},
		{
			name: "UV not required", check: catalog.ModelReviewUsageCheckUV,
			want: modelReviewUsageStatusUnknown,
			mutate: func(_ *modelinspect.Report, usage *catalog.ChatModel3DUsageSchema) {
				usage.Profiles[0].Budget.RequireUV = false
			},
		},
		{name: "watertight", check: catalog.ModelReviewUsageCheckWatertight, want: modelReviewUsageStatusPass},
		{
			name: "not watertight", check: catalog.ModelReviewUsageCheckWatertight,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Topology.Watertight = false
			},
		},
		{
			name: "watertight not required", check: catalog.ModelReviewUsageCheckWatertight,
			want: modelReviewUsageStatusUnknown,
			mutate: func(_ *modelinspect.Report, usage *catalog.ChatModel3DUsageSchema) {
				usage.Profiles[0].Budget.RequireWatertight = false
			},
		},
		{
			name: "an empty mesh cannot be watertight", check: catalog.ModelReviewUsageCheckWatertight,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Triangles = 0
				report.Topology.Watertight = false
			},
		},
		{name: "skin present", check: catalog.ModelReviewUsageCheckSkin, want: modelReviewUsageStatusPass},
		{
			name: "skin missing", check: catalog.ModelReviewUsageCheckSkin,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Skins = nil
			},
		},
		{
			name: "skin not required", check: catalog.ModelReviewUsageCheckSkin,
			want: modelReviewUsageStatusUnknown,
			mutate: func(_ *modelinspect.Report, usage *catalog.ChatModel3DUsageSchema) {
				usage.Profiles[0].Budget.RequireSkin = false
			},
		},
		{name: "textures inside the limit", check: catalog.ModelReviewUsageCheckTextureSize, want: modelReviewUsageStatusPass},
		{
			name: "a texture above the limit", check: catalog.ModelReviewUsageCheckTextureSize,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Materials = []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
					{Slot: "base_color", Present: true, Width: 2048, Height: 1024},
				}}}
			},
		},
		{
			name: "no texture limit", check: catalog.ModelReviewUsageCheckTextureSize,
			want: modelReviewUsageStatusUnknown,
			mutate: func(_ *modelinspect.Report, usage *catalog.ChatModel3DUsageSchema) {
				usage.Profiles[0].Budget.MaxTextureSize = nil
			},
		},
		{name: "pivot at the bottom centre", check: catalog.ModelReviewUsageCheckPivotBottomCenter, want: modelReviewUsageStatusPass},
		{
			name: "pivot away from the bottom centre", check: catalog.ModelReviewUsageCheckPivotBottomCenter,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.BottomCenterOffset = [3]float64{0, -1, 0}
			},
		},
		{
			name: "an empty bounding box has no pivot distance", check: catalog.ModelReviewUsageCheckPivotBottomCenter,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{0, 0, 0}
			},
		},
		{name: "Y is the highest axis", check: catalog.ModelReviewUsageCheckYUp, want: modelReviewUsageStatusPass},
		{
			name: "X is the highest axis", check: catalog.ModelReviewUsageCheckYUp,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{2, 1, 0.5}
			},
		},
		{
			name: "Z is the highest axis", check: catalog.ModelReviewUsageCheckYUp,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{0.5, 1, 2}
			},
		},
		{
			name: "a flat model has no up axis", check: catalog.ModelReviewUsageCheckYUp,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{2, 1, 0.02}
			},
		},
		{
			name: "an unreadable bounding box has no up axis", check: catalog.ModelReviewUsageCheckYUp,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Warnings = []string{"read_error"}
			},
		},
		{name: "a plausible scale", check: catalog.ModelReviewUsageCheckPlausibleScale, want: modelReviewUsageStatusPass},
		{
			name: "a model below the scale range", check: catalog.ModelReviewUsageCheckPlausibleScale,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{0.1, 0.1, 0.1}
			},
		},
		{
			name: "a model above the scale range", check: catalog.ModelReviewUsageCheckPlausibleScale,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.BBox.Size = [3]float64{3, 0.5, 0.5}
			},
		},
		{
			name: "a purpose without a scale range", check: catalog.ModelReviewUsageCheckPlausibleScale,
			want: modelReviewUsageStatusUnknown,
			mutate: func(_ *modelinspect.Report, usage *catalog.ChatModel3DUsageSchema) {
				usage.Profiles[0].ScaleRangeM = nil
			},
		},
		{name: "no debris", check: catalog.ModelReviewUsageCheckDebris, want: modelReviewUsageStatusPass},
		{
			name: "debris above the ratio", check: catalog.ModelReviewUsageCheckDebris,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Topology.DebrisTriangles = 1
			},
		},
		{
			name: "an empty mesh has no debris ratio", check: catalog.ModelReviewUsageCheckDebris,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Triangles = 0
			},
		},
		{name: "no degenerate triangles", check: catalog.ModelReviewUsageCheckDegenerate, want: modelReviewUsageStatusPass},
		{
			name: "degenerate triangles above the ratio", check: catalog.ModelReviewUsageCheckDegenerate,
			want: modelReviewUsageStatusFail,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Topology.DegenerateTriangles = 1
			},
		},
		{
			name: "an empty mesh has no degenerate ratio", check: catalog.ModelReviewUsageCheckDegenerate,
			want: modelReviewUsageStatusUnknown,
			mutate: func(report *modelinspect.Report, _ *catalog.ChatModel3DUsageSchema) {
				report.Triangles = 0
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := usageBaseReport()
			usage := usageBaseUsage()
			if test.mutate != nil {
				test.mutate(&report, &usage)
			}
			check := newModelReviewUsageCheck(report, usage, production, chat.ProductionBrief{}, usageIdentityText, allAvailable)
			item := usageItem(t, check, test.check)
			if item.Status != test.want {
				t.Fatalf("%s = %q, want %q (actual=%v budget=%v)", test.check, item.Status, test.want, item.Actual, item.Budget)
			}
			if test.want == modelReviewUsageStatusFail && strings.TrimSpace(item.FixHint) == "" {
				t.Fatalf("a failed %s check has no fix hint", test.check)
			}
			if test.want != modelReviewUsageStatusFail && item.FixHint != "" {
				t.Fatalf("a %s check carries a fix hint: %q", test.want, item.FixHint)
			}
		})
	}
}

// TestModelReviewUsageCheckNamesTheBriefPurposeAndTriangleOverride shows that
// the frozen brief selects the purpose, its triangle_budget replaces the
// profile maximum, and the note says both.
func TestModelReviewUsageCheckNamesTheBriefPurposeAndTriangleOverride(t *testing.T) {
	production := usageProduction(t)
	brief := chat.ProductionBrief{
		"purpose":         []byte(`"prop"`),
		"triangle_budget": []byte(`12`),
	}
	check := newModelReviewUsageCheck(
		usageBaseReport(), usageBaseUsage(), production, brief,
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true },
	)
	if check == nil {
		t.Fatal("the brief did not produce a usage check")
	}
	if check.Purpose != "prop" || check.PurposeLabel != "道具" || check.PurposeSource != modelReviewUsageSourceBrief {
		t.Fatalf("purpose = %+v", check)
	}
	if !strings.Contains(check.Note, "道具") || !strings.Contains(check.Note, "12") {
		t.Fatalf("note = %q, want the purpose label and the override", check.Note)
	}
	triangles := usageItem(t, check, catalog.ModelReviewUsageCheckTriangles)
	if triangles.Status != modelReviewUsageStatusFail {
		t.Fatalf("triangles = %+v, want a failure above the override", triangles)
	}
	if got, _ := triangles.Budget["maximum"].(int); got != 12 {
		t.Fatalf("triangle maximum = %v, want 12", triangles.Budget["maximum"])
	}
}

// TestModelReviewUsageCheckFindsBriefFieldsByDeclaredType covers a Schema that
// renames the profile and integer fields: the usage check must still find them
// (#103 review C3).
func TestModelReviewUsageCheckFindsBriefFieldsByDeclaredType(t *testing.T) {
	usage := usageBaseUsage()
	usage.Brief.Fields = []catalog.ChatProductionBriefField{
		{Name: "use", Type: "profile"},
		{Name: "faces", Type: "integer"},
	}
	brief := chat.ProductionBrief{
		"use":   []byte(`"prop"`),
		"faces": []byte(`12`),
	}
	check := newModelReviewUsageCheck(
		usageBaseReport(), usage, usageProduction(t), brief,
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true },
	)
	if check == nil || check.Purpose != "prop" {
		t.Fatalf("usage check = %+v, want the renamed profile field", check)
	}
	triangles := usageItem(t, check, catalog.ModelReviewUsageCheckTriangles)
	if got, _ := triangles.Budget["maximum"].(int); got != 12 {
		t.Fatalf("triangle maximum = %v, want 12 from the renamed integer field", triangles.Budget["maximum"])
	}
}

// TestModelReviewUsageCheckFallsBackToTheDefaultPurpose covers a session
// without a brief and a brief whose purpose is absent or no longer declared.
func TestModelReviewUsageCheckFallsBackToTheDefaultPurpose(t *testing.T) {
	production := usageProduction(t)
	tests := []struct {
		name  string
		brief chat.ProductionBrief
	}{
		{name: "no brief at all", brief: chat.ProductionBrief{}},
		{name: "a brief without a purpose", brief: chat.ProductionBrief{"style": []byte(`"stylised"`)}},
		{name: "a purpose the Schema does not declare", brief: chat.ProductionBrief{"purpose": []byte(`"missing"`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := newModelReviewUsageCheck(
				usageBaseReport(), usageBaseUsage(), production, test.brief,
				usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true },
			)
			if check == nil {
				t.Fatal("the default profile did not produce a usage check")
			}
			if check.Purpose != "game" || check.PurposeSource != modelReviewUsageSourceDefault {
				t.Fatalf("purpose = %+v, want the default game purpose", check)
			}
			if !strings.Contains(check.Note, "默认用途") || !strings.Contains(check.Note, "游戏角色") {
				t.Fatalf("note = %q, want the default-purpose sentence", check.Note)
			}
		})
	}
}

// TestModelReviewUsageCheckRequiresUsageProfiles keeps historical Catalogs
// valid: without a usage declaration the inspect result simply has no
// usage_check object.
func TestModelReviewUsageCheckRequiresUsageProfiles(t *testing.T) {
	check := newModelReviewUsageCheck(
		usageBaseReport(), catalog.ChatModel3DUsageSchema{}, usageProduction(t),
		chat.ProductionBrief{}, usageIdentityText,
		func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true },
	)
	if check != nil {
		t.Fatalf("usage check = %+v, want nil without profiles", check)
	}
}

// TestModelReviewUsageFixHintNamesOnlyAvailableCapabilities filters the
// Schema's fix options through the runtime Offering list: a disabled or
// unavailable capability disappears, a grouped fix needs every member, and a
// fix with no available option falls back to the Schema's no-capability text.
func TestModelReviewUsageFixHintNamesOnlyAvailableCapabilities(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	production := product.Application().Chat.ModelReview.Inspect.Production
	profile := usageBaseUsage().Profiles[0]
	pivot := catalog.ChatModelInspectFixSchema{
		Check:    catalog.ModelReviewUsageCheckPivotBottomCenter,
		Template: "可用 {capabilities} 把模型原点移到底部中心。",
		Options: []catalog.ChatModelInspectFixOptionSchema{
			{Capabilities: []catalog.ChatModelInspectFixCapabilitySchema{
				{Capability: "resize", Parameter: "origin_at", Value: "bottom"},
			}},
			{Capabilities: []catalog.ChatModelInspectFixCapabilitySchema{
				{Capability: "format-conversion", Parameter: "pivot_to_center_bottom", Value: "true"},
			}},
		},
	}
	debris := catalog.ChatModelInspectFixSchema{
		Check:    catalog.ModelReviewUsageCheckDebris,
		Template: "可用 {capabilities} 先拆分再合并碎片。",
		Options: []catalog.ChatModelInspectFixOptionSchema{
			{Capabilities: []catalog.ChatModelInspectFixCapabilitySchema{
				{Capability: "model-split"},
				{Capability: "model-merge"},
			}},
		},
	}
	scale := catalog.ChatModelInspectFixSchema{
		Check:    catalog.ModelReviewUsageCheckPlausibleScale,
		Template: "可用 {capabilities} 把最长边调整到 {minimum}–{maximum} 米。",
		Options: []catalog.ChatModelInspectFixOptionSchema{
			{Capabilities: []catalog.ChatModelInspectFixCapabilitySchema{{Capability: "resize"}}},
		},
	}
	available := func(available ...string) modelReviewFixAvailability {
		allowed := map[string]bool{}
		for _, name := range available {
			allowed[name] = true
		}
		return func(candidate catalog.ChatModelInspectFixCapabilitySchema) bool {
			return allowed[candidate.Capability]
		}
	}
	t.Run("every option is named", func(t *testing.T) {
		hint := modelReviewUsageFixHint(pivot, profile, production, usageIdentityText,
			available("resize", "format-conversion"))
		if !strings.Contains(hint, "resize(origin_at=bottom)") ||
			!strings.Contains(hint, "format-conversion(pivot_to_center_bottom=true)") ||
			!strings.Contains(hint, production.OptionSeparator) {
			t.Fatalf("hint = %q", hint)
		}
	})
	t.Run("an unavailable option is omitted", func(t *testing.T) {
		hint := modelReviewUsageFixHint(pivot, profile, production, usageIdentityText, available("resize"))
		if strings.Contains(hint, "format-conversion") || !strings.Contains(hint, "resize(origin_at=bottom)") {
			t.Fatalf("hint = %q", hint)
		}
	})
	t.Run("no available option uses the Schema text", func(t *testing.T) {
		hint := modelReviewUsageFixHint(pivot, profile, production, usageIdentityText, available())
		if hint != production.NoCapabilityText {
			t.Fatalf("hint = %q, want %q", hint, production.NoCapabilityText)
		}
	})
	t.Run("a grouped option needs every capability", func(t *testing.T) {
		hint := modelReviewUsageFixHint(debris, profile, production, usageIdentityText, available("model-split"))
		if hint != production.NoCapabilityText {
			t.Fatalf("hint = %q, want the no-capability text while model-merge is unavailable", hint)
		}
		hint = modelReviewUsageFixHint(debris, profile, production, usageIdentityText, available("model-split", "model-merge"))
		if !strings.Contains(hint, "model-split"+production.CapabilityJoiner+"model-merge") {
			t.Fatalf("hint = %q", hint)
		}
	})
	t.Run("a scale fix names the purpose range", func(t *testing.T) {
		hint := modelReviewUsageFixHint(scale, profile, production, usageIdentityText, available("resize"))
		if !strings.Contains(hint, "1–2 米") || !strings.Contains(hint, "resize") {
			t.Fatalf("hint = %q", hint)
		}
	})
	t.Run("a static fix ignores Offering availability", func(t *testing.T) {
		fix := catalog.ChatModelInspectFixSchema{
			Check:    catalog.ModelReviewUsageCheckTextureSize,
			Template: "贴图超过用途上限；请重新生成并降低贴图尺寸。",
		}
		if hint := modelReviewUsageFixHint(fix, profile, production, usageIdentityText, available()); hint != fix.Template {
			t.Fatalf("hint = %q, want the static template", hint)
		}
	})
}
