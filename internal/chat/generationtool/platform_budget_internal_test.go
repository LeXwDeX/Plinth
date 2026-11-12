package generationtool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// #275: the brief's target platform selects the purpose's budget and parameter
// overrides. These tests run on the shipped Catalog, so they pin what a session
// actually gets; the Catalog's own tests pin the values and the validation.

func platformBrief(t *testing.T, values map[string]any) chat.ProductionBrief {
	t.Helper()
	brief := chat.ProductionBrief{}
	for name, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = encoded
	}
	return brief
}

func platformShipped(t *testing.T) (catalog.ChatModel3DUsageSchema, catalog.ChatModelInspectProductionSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	return application.Chat.Model3DUsage, application.Chat.ModelReview.Inspect.Production
}

// platformReport is a model that meets every bound of a game character except
// the ones a test sets: its triangle count and its largest texture side.
func platformReport(triangles int64, texture int) modelinspect.Report {
	report := usageBaseReport()
	report.Triangles = triangles
	report.Materials = []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
		{Slot: "base_color", Present: true, Width: texture, Height: texture},
	}}}
	return report
}

func platformInspect(t *testing.T, brief chat.ProductionBrief, triangles int64, texture int,
	available modelReviewFixAvailability) *modelReviewUsageCheck {
	t.Helper()
	usage, production := platformShipped(t)
	check := newModelReviewUsageCheck(platformReport(triangles, texture), usage, production, brief,
		usageIdentityText, available)
	if check == nil {
		t.Fatal("no usage check")
	}
	return check
}

func allFixCapabilities(catalog.ChatModelInspectFixCapabilitySchema) bool { return true }

func TestUsageCheckJudgesByThePlatformBudgetAndSaysWhich(t *testing.T) {
	const character = "game_character"
	tests := []struct {
		name         string
		brief        map[string]any
		minimum      int
		maximum      int
		texture      int
		platform     string
		platformName string
	}{
		{name: "no platform keeps the single budget",
			brief: map[string]any{"purpose": character}, minimum: 5000, maximum: 60000, texture: 2048},
		{name: "mobile", brief: map[string]any{"purpose": character, "target_platform": "mobile"},
			minimum: 5000, maximum: 20000, texture: 2048, platform: "mobile", platformName: "手机"},
		{name: "web shares the mobile-class budget", brief: map[string]any{"purpose": character, "target_platform": "web"},
			minimum: 5000, maximum: 20000, texture: 2048, platform: "web", platformName: "网页"},
		{name: "xr shares the mobile-class budget", brief: map[string]any{"purpose": character, "target_platform": "xr"},
			minimum: 5000, maximum: 20000, texture: 2048, platform: "xr", platformName: "XR（VR / AR / MR）"},
		{name: "pc and console", brief: map[string]any{"purpose": character, "target_platform": "pc_console"},
			minimum: 5000, maximum: 100000, texture: 4096, platform: "pc_console", platformName: "PC / 主机"},
		{name: "a stale free-text platform is unspecified",
			brief: map[string]any{"purpose": character, "target_platform": "手机 + Switch"}, minimum: 5000, maximum: 60000, texture: 2048},
		{name: "a purpose without variants ignores the platform",
			brief: map[string]any{"purpose": "render", "target_platform": "mobile"}, maximum: 2000000, texture: 8192},
		{name: "the brief triangle budget still wins over the platform",
			brief:   map[string]any{"purpose": character, "target_platform": "pc_console", "triangle_budget": 12000},
			minimum: 5000, maximum: 12000, texture: 4096, platform: "pc_console", platformName: "PC / 主机"},
		{name: "a prop on mobile", brief: map[string]any{"purpose": "game_prop", "target_platform": "mobile"},
			minimum: 300, maximum: 8000, texture: 2048, platform: "mobile", platformName: "手机"},
		{name: "a prop on pc and console", brief: map[string]any{"purpose": "game_prop", "target_platform": "pc_console"},
			minimum: 300, maximum: 40000, texture: 4096, platform: "pc_console", platformName: "PC / 主机"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := platformInspect(t, platformBrief(t, test.brief), 10000, 1024, allFixCapabilities)
			triangles := usageItem(t, check, catalog.ModelReviewUsageCheckTriangles)
			if got, _ := triangles.Budget["maximum"].(int); got != test.maximum {
				t.Fatalf("triangle maximum = %v, want %d", triangles.Budget["maximum"], test.maximum)
			}
			if test.minimum > 0 {
				if got, _ := triangles.Budget["minimum"].(int); got != test.minimum {
					t.Fatalf("triangle minimum = %v, want %d", triangles.Budget["minimum"], test.minimum)
				}
			}
			texture := usageItem(t, check, catalog.ModelReviewUsageCheckTextureSize)
			if got, _ := texture.Budget["maximum"].(int); got != test.texture {
				t.Fatalf("texture maximum = %v, want %d", texture.Budget["maximum"], test.texture)
			}
			if check.Platform != test.platform || check.PlatformLabel != test.platformName {
				t.Fatalf("platform = %q/%q, want %q/%q", check.Platform, check.PlatformLabel, test.platform, test.platformName)
			}
			sentence := "目标平台「" + test.platformName + "」"
			if test.platform == "" && strings.Contains(check.Note, "目标平台") {
				t.Fatalf("a session without a variant names a platform: %q", check.Note)
			}
			if test.platform != "" && !strings.Contains(check.Note, sentence) {
				t.Fatalf("note = %q, want it to say the %s budget was used", check.Note, sentence)
			}
			if _, overridden := test.brief["triangle_budget"]; overridden && !strings.Contains(check.Note, "12000") {
				t.Fatalf("note = %q, want the triangle override too", check.Note)
			}
		})
	}
}

// The same model passes on one platform and fails on another: the verdict
// follows the platform budget, not a single number.
func TestUsageCheckVerdictFollowsThePlatform(t *testing.T) {
	status := func(platform any, triangles int64, texture int) (string, string) {
		values := map[string]any{"purpose": "game_character"}
		if platform != nil {
			values["target_platform"] = platform
		}
		check := platformInspect(t, platformBrief(t, values), triangles, texture, allFixCapabilities)
		return usageItem(t, check, catalog.ModelReviewUsageCheckTriangles).Status,
			usageItem(t, check, catalog.ModelReviewUsageCheckTextureSize).Status
	}
	tests := []struct {
		name               string
		platform           any
		triangles          int64
		texture            int
		wantTri, wantImage string
	}{
		{"30000 triangles: fine without a platform", nil, 30000, 2048, modelReviewUsageStatusPass, modelReviewUsageStatusPass},
		{"30000 triangles: too heavy for mobile", "mobile", 30000, 2048, modelReviewUsageStatusFail, modelReviewUsageStatusPass},
		{"30000 triangles: fine on pc and console", "pc_console", 30000, 2048, modelReviewUsageStatusPass, modelReviewUsageStatusPass},
		{"70000 triangles: over the single budget", nil, 70000, 2048, modelReviewUsageStatusFail, modelReviewUsageStatusPass},
		{"70000 triangles: within the pc budget", "pc_console", 70000, 2048, modelReviewUsageStatusPass, modelReviewUsageStatusPass},
		{"120000 triangles: over even the pc budget", "pc_console", 120000, 2048, modelReviewUsageStatusFail, modelReviewUsageStatusPass},
		{"a 4096 texture: over the single budget", nil, 10000, 4096, modelReviewUsageStatusPass, modelReviewUsageStatusFail},
		{"a 4096 texture: over the mobile budget", "mobile", 10000, 4096, modelReviewUsageStatusPass, modelReviewUsageStatusFail},
		{"a 4096 texture: within the pc budget", "pc_console", 10000, 4096, modelReviewUsageStatusPass, modelReviewUsageStatusPass},
		{"an 8192 texture: over the pc budget", "pc_console", 10000, 8192, modelReviewUsageStatusPass, modelReviewUsageStatusFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotTriangles, gotTexture := status(test.platform, test.triangles, test.texture)
			if gotTriangles != test.wantTri || gotTexture != test.wantImage {
				t.Fatalf("triangles %s texture %s, want %s / %s", gotTriangles, gotTexture, test.wantTri, test.wantImage)
			}
		})
	}
}

// A texture over the platform's limit is fixed by downsampling to that limit,
// not by regenerating: the hint names the conversion capability with the
// platform's bound once an Offering that can resize textures is available, and
// falls back to regenerating while none is.
func TestTextureFixHintDownsamplesToThePlatformBound(t *testing.T) {
	brief := func(platform string) chat.ProductionBrief {
		return platformBrief(t, map[string]any{"purpose": "game_character", "target_platform": platform})
	}
	hint := func(platform string, available modelReviewFixAvailability, texture int) string {
		check := platformInspect(t, brief(platform), 10000, texture, available)
		return usageItem(t, check, catalog.ModelReviewUsageCheckTextureSize).FixHint
	}
	t.Run("an enabled conversion that sets the texture side", func(t *testing.T) {
		got := hint("mobile", allFixCapabilities, 4096)
		for _, want := range []string{"format-conversion(texture_size=2048)", "2048 像素", "降采样"} {
			if !strings.Contains(got, want) {
				t.Fatalf("hint = %q, want it to contain %q", got, want)
			}
		}
		if strings.Contains(got, "{") {
			t.Fatalf("hint leaves a placeholder: %q", got)
		}
		pc := hint("pc_console", allFixCapabilities, 8192)
		if !strings.Contains(pc, "format-conversion(texture_size=4096)") || !strings.Contains(pc, "4096 像素") {
			t.Fatalf("pc hint = %q", pc)
		}
		unset := hint("", allFixCapabilities, 4096)
		if !strings.Contains(unset, "format-conversion(texture_size=2048)") {
			t.Fatalf("hint without a platform = %q", unset)
		}
	})
	t.Run("the availability check receives the resolved bound", func(t *testing.T) {
		var asked []catalog.ChatModelInspectFixCapabilitySchema
		hint("pc_console", func(candidate catalog.ChatModelInspectFixCapabilitySchema) bool {
			asked = append(asked, candidate)
			return true
		}, 8192)
		if len(asked) != 1 || asked[0].Capability != "format-conversion" || asked[0].Parameter != "texture_size" || asked[0].Value != "4096" {
			t.Fatalf("availability asked about %+v, want the 4096 bound filled in", asked)
		}
	})
	t.Run("no enabled capability says to regenerate", func(t *testing.T) {
		got := hint("mobile", func(catalog.ChatModelInspectFixCapabilitySchema) bool { return false }, 4096)
		_, production := platformShipped(t)
		if strings.Contains(got, production.NoCapabilityText) {
			t.Fatalf("hint = %q: the generic no-capability sentence lost the regenerate path", got)
		}
		for _, want := range []string{"2048 像素", "没有已启用的贴图降采样能力", "重新生成"} {
			if !strings.Contains(got, want) {
				t.Fatalf("hint = %q, want it to contain %q", got, want)
			}
		}
	})
	t.Run("a fix without an unavailable template keeps the generic sentence", func(t *testing.T) {
		usage, production := platformShipped(t)
		profile, _ := usage.ForPlatform(usage.Profiles[1], "mobile")
		var fix catalog.ChatModelInspectFixSchema
		for _, candidate := range production.Fixes {
			if candidate.Check == catalog.ModelReviewUsageCheckTextureSize {
				fix = candidate
			}
		}
		fix.UnavailableTemplate = ""
		got := modelReviewUsageFixHint(fix, profile, production, usageIdentityText,
			func(catalog.ChatModelInspectFixCapabilitySchema) bool { return false })
		if got != production.NoCapabilityText {
			t.Fatalf("hint = %q, want %q", got, production.NoCapabilityText)
		}
	})
	t.Run("a purpose without a texture bound leaves no hint", func(t *testing.T) {
		usage, production := platformShipped(t)
		profile := usage.Profiles[0]
		profile.Budget.MaxTextureSize = nil
		var fix catalog.ChatModelInspectFixSchema
		for _, candidate := range production.Fixes {
			if candidate.Check == catalog.ModelReviewUsageCheckTextureSize {
				fix = candidate
			}
		}
		if got := modelReviewUsageFixHint(fix, profile, production, usageIdentityText, allFixCapabilities); got != "" {
			t.Fatalf("hint = %q, want none without a bound", got)
		}
	})
}

// A bound the fix computes from the purpose must lie in the range the
// conversion's parameter accepts, or the capability would be offered for a
// request the service refuses.
func TestModelReviewOfferingSupportsFixChecksTheComputedBound(t *testing.T) {
	minimum, maximum := 128.0, 4096.0
	offering := generation.Offering{Parameters: []generation.Parameter{{
		Name: "texture_size", Type: "number", Integer: true, Minimum: &minimum, Maximum: &maximum,
	}}}
	candidate := func(value string) catalog.ChatModelInspectFixCapabilitySchema {
		return catalog.ChatModelInspectFixCapabilitySchema{Capability: "format-conversion", Parameter: "texture_size", Value: value}
	}
	for value, want := range map[string]bool{
		"2048": true, "4096": true, "128": true, "8192": false, "64": false, "2048.5": false, "": false, "{maximum}": false,
	} {
		if got := modelReviewOfferingSupportsFix(offering, candidate(value)); got != want {
			t.Fatalf("texture_size=%q supported = %v, want %v", value, got, want)
		}
	}
	unbounded := generation.Offering{Parameters: []generation.Parameter{{Name: "texture_size", Type: "number"}}}
	if !modelReviewOfferingSupportsFix(unbounded, candidate("8192")) {
		t.Fatal("a parameter without a range must accept the bound")
	}
}

// What the brief's platform changes in the generation layer: the purpose's
// parameter overrides and the budget that bounds the parameters nobody chose.
func TestPurposeParameterValuesFollowThePlatform(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{chat: product.Application().Chat}
	command := chat.ToolCommand{Tool: catalog.ChatToolSchema{Name: "create_model_3d"}}
	values := func(offeringID, purpose, platform string) map[string]string {
		t.Helper()
		offering, found := product.Get(generation.OfferingID(offeringID))
		if !found {
			t.Fatalf("%s missing", offeringID)
		}
		brief := map[string]any{"purpose": purpose}
		if platform != "" {
			brief["target_platform"] = platform
		}
		command.Options.ProductionBrief = platformBrief(t, brief)
		_, applied := executor.profileParameterValues(command, offering)
		result := map[string]string{}
		for name, value := range applied {
			result[name] = string(value)
		}
		return result
	}
	expect := func(t *testing.T, got map[string]string, want map[string]string) {
		t.Helper()
		for name, value := range want {
			if got[name] != value {
				t.Fatalf("%s = %q, want %q (all: %v)", name, got[name], value, got)
			}
		}
	}

	t.Run("P2 asks for quads on every platform and for more faces where the budget allows", func(t *testing.T) {
		for _, id := range []string{"tripo/tripo-p2/text-to-3d", "tripo/tripo-p2/image-to-3d", "tripo/tripo-p2-multiview/image-to-3d"} {
			expect(t, values(id, "game_character", ""), map[string]string{"quad": "true", "face_limit": "10000", "texture_quality": `"standard"`})
			expect(t, values(id, "game_character", "mobile"), map[string]string{"quad": "true", "face_limit": "10000"})
			expect(t, values(id, "game_character", "pc_console"), map[string]string{"quad": "true", "face_limit": "25000"})
			expect(t, values(id, "game_character", "pc_console"), map[string]string{"texture_quality": `"standard"`})
		}
	})
	t.Run("the multiview model keeps smart low poly except where the budget has room for detail", func(t *testing.T) {
		const id = "tripo/tripo-v3.1-multiview/image-to-3d"
		expect(t, values(id, "game_character", ""), map[string]string{"quad": "true", "face_limit": "10000", "smart_low_poly": "true"})
		expect(t, values(id, "game_character", "xr"), map[string]string{"quad": "true", "face_limit": "10000", "smart_low_poly": "true"})
		expect(t, values(id, "game_character", "pc_console"), map[string]string{"quad": "true", "face_limit": "50000", "smart_low_poly": "false"})
	})
	t.Run("Meshy T2.5 stays triangle-only and takes the platform's polycount", func(t *testing.T) {
		for _, id := range []string{"meshy/meshy-t2.5/text-to-3d", "meshy/meshy-t2.5/image-to-3d"} {
			for platform, want := range map[string]string{"": "20000", "mobile": "20000", "pc_console": "30000"} {
				got := values(id, "game_character", platform)
				if got["target_polycount"] != want || got["pose_mode"] != `"a-pose"` {
					t.Fatalf("%s on %q: %v, want target_polycount %s and an A pose", id, platform, got, want)
				}
				if _, set := got["quad"]; set {
					t.Fatalf("%s on %q asks a triangle-only model for quads: %v", id, platform, got)
				}
			}
		}
	})
	t.Run("a model the purpose does not override still takes the platform budget", func(t *testing.T) {
		const full = "tripo/tripo-v3.1-full/text-to-3d"
		for platform, want := range map[string]string{"": "60000", "mobile": "20000", "pc_console": "100000"} {
			if got := values(full, "game_character", platform)["face_limit"]; got != want {
				t.Fatalf("character on %q: face_limit %s, want %s", platform, got, want)
			}
		}
		for platform, want := range map[string]string{"": "20000", "mobile": "8000", "pc_console": "40000"} {
			if got := values(full, "game_prop", platform)["face_limit"]; got != want {
				t.Fatalf("prop on %q: face_limit %s, want %s", platform, got, want)
			}
		}
		// Meshy 7.1 defaults to an 8K texture: the budget lowers it to the
		// largest size the platform allows.
		for platform, want := range map[string]string{"": `"2k"`, "mobile": `"2k"`, "pc_console": `"4k"`} {
			if got := values("meshy/meshy-7.1/image-to-3d", "game_character", platform)["texture_resolution"]; got != want {
				t.Fatalf("character on %q: texture_resolution %s, want %s", platform, got, want)
			}
		}
	})
	t.Run("props lower the multiview face counts the mobile budget would exceed", func(t *testing.T) {
		for _, id := range []string{"tripo/tripo-p2-multiview/image-to-3d", "tripo/tripo-v3.1-multiview/image-to-3d"} {
			expect(t, values(id, "game_prop", ""), map[string]string{"face_limit": "20000"})
			expect(t, values(id, "game_prop", "mobile"), map[string]string{"face_limit": "8000"})
			expect(t, values(id, "game_prop", "pc_console"), map[string]string{"face_limit": "20000"})
		}
		if got := values("meshy/meshy-t2.5/image-to-3d", "game_prop", "mobile")["target_polycount"]; got != "8000" {
			t.Fatalf("prop T2.5 on mobile: target_polycount %s, want 8000", got)
		}
	})
	t.Run("the brief triangle budget still wins over the platform", func(t *testing.T) {
		offering, _ := product.Get("tripo/tripo-v3.1-full/text-to-3d")
		command.Options.ProductionBrief = platformBrief(t, map[string]any{
			"purpose": "game_character", "target_platform": "pc_console", "triangle_budget": 30000,
		})
		_, applied := executor.profileParameterValues(command, offering)
		if string(applied["face_limit"]) != "30000" {
			t.Fatalf("face_limit = %s, want the 30000 override", applied["face_limit"])
		}
	})
}
