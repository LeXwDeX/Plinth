package catalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #275: the purposes get a platform dimension. The brief's target platform
// picks a variant of the budget and of the parameter overrides; a session
// without a platform keeps the purpose's single budget.

func shippedUsage(t *testing.T) ChatModel3DUsageSchema {
	t.Helper()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat.Model3DUsage
}

type platformBudgetWant struct{ minimum, maximum, texture int }

// The shipped platform budgets are the maintainers' starting values (the Issue
// asks for mobile characters at most 20000 triangles and 2048 textures, PC and
// console characters at most 100000 triangles and 4096 textures, and props
// scaled the same way). A phone, a browser and a standalone headset share the
// mobile-class variant.
func TestShippedPlatformBudgetsPerPurposeAndPlatform(t *testing.T) {
	usage := shippedUsage(t)
	if usage.PlatformField != "target_platform" {
		t.Fatalf("platform_field = %q", usage.PlatformField)
	}
	mobile := func(minimum, maximum int) map[string]platformBudgetWant {
		want := platformBudgetWant{minimum, maximum, 2048}
		return map[string]platformBudgetWant{"mobile": want, "web": want, "xr": want}
	}
	wants := map[string]map[string]platformBudgetWant{
		"game_character": mobile(5000, 20000),
		"game_prop":      mobile(300, 8000),
	}
	wants["game_character"]["pc_console"] = platformBudgetWant{5000, 100000, 4096}
	wants["game_prop"]["pc_console"] = platformBudgetWant{300, 40000, 4096}
	for purpose, platforms := range wants {
		profile := purposeProfile(t, usage, purpose)
		for platform, want := range platforms {
			resolved, choice := usage.ForPlatform(profile, platform)
			if choice == nil || choice.ID != platform {
				t.Fatalf("%s on %s: choice = %+v", purpose, platform, choice)
			}
			option, _ := usage.PlatformOption(platform)
			if choice.Label != option.Label || choice.Label == "" {
				t.Fatalf("%s on %s: label %q, want the brief option label %q", purpose, platform, choice.Label, option.Label)
			}
			budget := resolved.Budget
			if budget.MinTriangles == nil || *budget.MinTriangles != want.minimum ||
				budget.MaxTriangles != want.maximum ||
				budget.MaxTextureSize == nil || *budget.MaxTextureSize != want.texture {
				t.Fatalf("%s on %s: budget = %+v, want %+v", purpose, platform, budget, want)
			}
			// Delivery requirements belong to the purpose, never to a platform.
			if budget.RequireUV != profile.Budget.RequireUV || budget.RequireWatertight != profile.Budget.RequireWatertight ||
				budget.RequireSkin != profile.Budget.RequireSkin {
				t.Fatalf("%s on %s: requirements changed: %+v", purpose, platform, budget)
			}
			if resolved.Platforms != nil {
				t.Fatalf("%s on %s: the resolved purpose still lists its variants", purpose, platform)
			}
		}
	}
}

// A session without a platform keeps today's single budget and overrides, and so
// does one whose platform is a stale free-text value, an undeclared id or a
// purpose that declares no variants.
func TestForPlatformKeepsTheSingleBudgetWithoutAMatchingVariant(t *testing.T) {
	usage := shippedUsage(t)
	for _, purpose := range []string{"game_character", "game_prop"} {
		profile := purposeProfile(t, usage, purpose)
		for _, platform := range []string{"", "  ", "手机 + Switch", "console_switch"} {
			resolved, choice := usage.ForPlatform(profile, platform)
			if choice != nil {
				t.Fatalf("%s on %q: choice = %+v, want none", purpose, platform, choice)
			}
			if !reflect.DeepEqual(resolved.Budget, profile.Budget) ||
				!reflect.DeepEqual(resolved.ParameterOverrides, profile.ParameterOverrides) {
				t.Fatalf("%s on %q changed the single budget or overrides", purpose, platform)
			}
		}
	}
	for _, purpose := range []string{"render", "web_ar", "print"} {
		profile := purposeProfile(t, usage, purpose)
		for _, platform := range []string{"mobile", "pc_console"} {
			resolved, choice := usage.ForPlatform(profile, platform)
			if choice != nil || !reflect.DeepEqual(resolved.Budget, profile.Budget) {
				t.Fatalf("%s declares no platform variants but %s changed it: %+v %+v", purpose, platform, resolved.Budget, choice)
			}
		}
	}
	// A Catalog without a platform field has no platforms at all.
	bare := usage
	bare.PlatformField = ""
	profile := purposeProfile(t, usage, "game_character")
	if resolved, choice := bare.ForPlatform(profile, "mobile"); choice != nil || !reflect.DeepEqual(resolved.Budget, profile.Budget) {
		t.Fatalf("a Catalog without platform_field resolved a platform: %+v %+v", resolved.Budget, choice)
	}
}

// Resolving a platform builds new maps: the Schema is shared by every session
// and must not change when one session picks a platform.
func TestForPlatformMergesOverridesWithoutChangingTheSchema(t *testing.T) {
	usage := shippedUsage(t)
	profile := purposeProfile(t, usage, "game_character")
	before, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	resolved, choice := usage.ForPlatform(profile, "pc_console")
	if choice == nil {
		t.Fatal("pc_console did not resolve")
	}
	const p2 = "tripo/tripo-p2/image-to-3d"
	merged := resolved.ParameterOverrides[p2]
	if _, quad := merged["quad"]; quad || string(merged["face_limit"]) != "50000" || string(merged["texture_quality"]) != `"standard"` {
		t.Fatalf("merged P2 overrides = %v, want the platform's face_limit over the purpose's texture_quality and no quad", merged)
	}
	// The smart low poly switch the purpose turns on is turned off by the
	// platform that has the budget for full detail.
	if string(resolved.ParameterOverrides["tripo/tripo-v3.1-multiview/image-to-3d"]["smart_low_poly"]) != "false" {
		t.Fatalf("multiview overrides = %v", resolved.ParameterOverrides["tripo/tripo-v3.1-multiview/image-to-3d"])
	}
	if string(profile.ParameterOverrides[p2]["face_limit"]) != "20000" {
		t.Fatalf("the shared purpose was modified: %s", profile.ParameterOverrides[p2]["face_limit"])
	}
	merged["face_limit"] = json.RawMessage(`1`)
	after, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("resolving a platform changed the Schema value:\nbefore %s\nafter  %s", before, after)
	}
}

func offeringDeclaresQuad(offering schemacontract.Offering) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Name == "quad" && parameter.Type == "boolean" {
			return true
		}
	}
	return false
}

// #275 ordered the game purposes by output quality, and its reason for putting
// the Tripo models first was that a character's deformation wants quad topology
// and they could output quads. #294: a real P2 task with quad=true
// (2026-11-12) delivered an FBX and no GLB, which neither the stage preview nor
// inspect_model can read, so the character no longer turns Tripo's quad output
// on and the order stands on detail alone. P2 and the v3.1 multiview model still
// lead, keep their output in triangles and keep the face limit inside the budget
// of every platform.
func TestGameCharacterOrdersByDetailAndKeepsTripoOnTriangles(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	profile := purposeProfile(t, usage, "game_character")
	wantOrder := map[string][]string{
		"text-to-3d": {"tripo/tripo-p2/text-to-3d", "meshy/meshy-t2.5/text-to-3d",
			"hyper3d/rodin-gen-2.5/text-to-3d", "meshy/meshy-7.1/text-to-3d"},
		"image-to-3d": {"tripo/tripo-p2/image-to-3d", "meshy/meshy-t2.5/image-to-3d",
			"hyper3d/rodin-gen-2.5/image-to-3d", "meshy/meshy-7.1/image-to-3d"},
		// The higher-fidelity multiview model first, P2 multiview after it.
		"multiview-to-3d": {"tripo/tripo-v3.1-multiview/image-to-3d", "tripo/tripo-p2-multiview/image-to-3d"},
	}
	for shape, want := range wantOrder {
		if got := profile.RecommendedOfferings[shape]; !slices.Equal(got, want) {
			t.Fatalf("%s recommends %v, want %v", shape, got, want)
		}
	}
	for shape, ids := range profile.RecommendedOfferings {
		for _, id := range ids {
			if _, found := product.Get(schemacontract.OfferingID(id)); !found {
				t.Fatalf("%s recommends %q, which is not in the Catalog", shape, id)
			}
		}
	}
	// Meshy T2.5 Smart Topology outputs triangles only (a quad request is a 400
	// upstream), so it is recommended after P2 and never asked for quads.
	t2 := "meshy/meshy-t2.5/image-to-3d"
	offering, _ := product.Get(schemacontract.OfferingID(t2))
	if offeringDeclaresQuad(offering) {
		t.Fatalf("%s declares quad output; the character profile order assumes it cannot", t2)
	}
	// The three P2 Offerings still offer quad output, off by default, for a person
	// who asks for the FBX; the purpose just does not ask for it.
	for _, id := range []string{"tripo/tripo-p2/text-to-3d", "tripo/tripo-p2/image-to-3d", "tripo/tripo-p2-multiview/image-to-3d"} {
		p2, _ := product.Get(schemacontract.OfferingID(id))
		if !offeringDeclaresQuad(p2) {
			t.Fatalf("%s no longer declares the quad parameter", id)
		}
		for _, parameter := range p2.Parameters {
			if parameter.Name == "quad" && string(parameter.Default) != "false" {
				t.Fatalf("%s: quad defaults to %s, want false", id, parameter.Default)
			}
		}
	}

	platforms := []string{""}
	for _, variant := range profile.Platforms {
		platforms = append(platforms, variant.IDs...)
	}
	recommended := map[string]bool{}
	for _, ids := range profile.RecommendedOfferings {
		for _, id := range ids {
			recommended[id] = true
		}
	}
	for _, platform := range platforms {
		resolved, _ := usage.ForPlatform(profile, platform)
		triangles := resolved.Budget.MaxTriangles
		for id := range recommended {
			offering, _ := product.Get(schemacontract.OfferingID(id))
			override := resolved.ParameterOverrides[id]
			if quad, set := override["quad"]; set {
				t.Fatalf("platform %q: %s sets quad = %s; a P2 task with quad output delivers an FBX only (#294)", platform, id, quad)
			}
			if !offeringDeclaresQuad(offering) {
				continue
			}
			// A Tripo model on triangle output delivers at most face_limit
			// triangles, which is one face per triangle for the budget machinery
			// too: the limit is set (an unset one would be left to the upstream's
			// own choice) and it fits the platform's budget.
			var faces int
			if err := json.Unmarshal(override["face_limit"], &faces); err != nil || faces <= 0 {
				t.Fatalf("platform %q: %s has no face_limit: %s", platform, id, override["face_limit"])
			}
			if faces > triangles {
				t.Fatalf("platform %q: %s asks for %d triangles, over the %d-triangle budget", platform, id, faces, triangles)
			}
		}
	}

	// Props do not deform: no prop override turns quad on.
	prop := purposeProfile(t, usage, "game_prop")
	for _, platform := range []string{"", "mobile", "pc_console"} {
		resolved, _ := usage.ForPlatform(prop, platform)
		for id, override := range resolved.ParameterOverrides {
			if _, set := override["quad"]; set {
				t.Fatalf("game_prop on %q sets quad for %s", platform, id)
			}
		}
	}
}

// The reasons say what the order is based on, in the Schema's words; the old
// cost-first sentence is gone.
func TestGameProfileReasonsStateTheQualityBasis(t *testing.T) {
	usage := shippedUsage(t)
	character := purposeProfile(t, usage, "game_character").Reason
	prop := purposeProfile(t, usage, "game_prop").Reason
	for name, text := range map[string]string{"game_character": character, "game_prop": prop} {
		if strings.Contains(text, "先用成本更低") {
			t.Fatalf("%s still orders by cost first: %q", name, text)
		}
		if !strings.Contains(text, "尚未用真实上游样本核对") {
			t.Fatalf("%s does not record that real quality is unverified: %q", name, text)
		}
	}
	for _, want := range []string{"四边面", "只出三角面", "成本只在质量相同时"} {
		if !strings.Contains(character, want) {
			t.Fatalf("game_character reason omits %q: %q", want, character)
		}
	}
	if !strings.Contains(prop, "质量相同时才按成本") || !strings.Contains(prop, "没有可依据的质量差异") {
		t.Fatalf("game_prop reason does not say cost only breaks a tie: %q", prop)
	}
}

// The character's reason records the two things the real checks showed
// (2026-11-12, #294) in both languages, so the order and the numbers are not read
// as the old quad-first argument: P2 with quad output delivers an FBX only, which
// is why Tripo's quad output stays off, and a Meshy 7.1 quad target delivers about
// 2.25 triangles per target face, which is why its target is 0.4 of the budget.
func TestGameCharacterReasonStatesTheRealFindings(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	profile := purposeProfile(t, application.Chat.Model3DUsage, "game_character")
	english := application.Localization.Bundles["en"].Fields["chat.model3d_usage.profiles[id=game_character].reason"]
	for language, want := range map[string]struct {
		text  string
		parts []string
	}{
		"zh": {profile.Reason, []string{"2026-11-12", "quad=true", "只交付 FBX", "没有可预览的 GLB", "不开 Tripo 的四边面", "22540", "2.25", "0.4 倍"}},
		"en": {english, []string{"2026-11-12", "quad=true", "FBX only", "no previewable GLB", "do not turn Tripo's quad output on", "22540", "2.25", "0.4 times"}},
	} {
		if want.text == "" {
			t.Fatalf("%s: the game_character reason is missing", language)
		}
		for _, part := range want.parts {
			if !strings.Contains(want.text, part) {
				t.Errorf("%s reason omits %q: %q", language, part, want.text)
			}
		}
		// The retired argument must be gone.
		for _, retired := range []string{"默认开启四边面", "quads are turned on by default"} {
			if strings.Contains(want.text, retired) {
				t.Errorf("%s reason still says %q", language, retired)
			}
		}
	}
}

// ---- validation -------------------------------------------------------------

func usageDocument(document map[string]any) map[string]any {
	return document["application"].(map[string]any)["chat"].(map[string]any)["model3d_usage"].(map[string]any)
}

func usageProfileDocument(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, item := range usageDocument(document)["profiles"].([]any) {
		profile := item.(map[string]any)
		if profile["id"] == id {
			return profile
		}
	}
	t.Fatalf("profile %q missing", id)
	return nil
}

func platformVariantDocument(t *testing.T, document map[string]any, profileID string, index int) map[string]any {
	t.Helper()
	return usageProfileDocument(t, document, profileID)["platforms"].([]any)[index].(map[string]any)
}

func productionDocument(document map[string]any) map[string]any {
	chat := document["application"].(map[string]any)["chat"].(map[string]any)
	return chat["model_review"].(map[string]any)["inspect"].(map[string]any)["production"].(map[string]any)
}

func productionFixDocument(t *testing.T, document map[string]any, check string) map[string]any {
	t.Helper()
	for _, item := range productionDocument(document)["fixes"].([]any) {
		fix := item.(map[string]any)
		if fix["check"] == check {
			return fix
		}
	}
	t.Fatalf("fix %q missing", check)
	return nil
}

func TestPlatformDeclarationsAreValidated(t *testing.T) {
	const p2 = "tripo/tripo-p2/text-to-3d"
	tests := []struct {
		name   string
		change func(t *testing.T, document map[string]any)
		want   string
	}{
		{
			name: "a platform that is not an option of the field",
			change: func(t *testing.T, document map[string]any) {
				platformVariantDocument(t, document, "game_character", 1)["ids"] = []any{"console_switch"}
			},
			want: "不是字段",
		},
		{
			name: "a platform declared by two variants",
			change: func(t *testing.T, document map[string]any) {
				platformVariantDocument(t, document, "game_character", 1)["ids"] = []any{"pc_console", "mobile"}
			},
			want: "重复",
		},
		{
			name: "a variant without platforms",
			change: func(t *testing.T, document map[string]any) {
				platformVariantDocument(t, document, "game_character", 0)["ids"] = []any{}
			},
			want: "ids 不能为空",
		},
		{
			name: "a variant without any bound",
			change: func(t *testing.T, document map[string]any) {
				platformVariantDocument(t, document, "game_character", 0)["budget"] = map[string]any{}
			},
			want: "至少要声明",
		},
		{
			name: "a variant maximum below the purpose minimum",
			change: func(t *testing.T, document map[string]any) {
				platformVariantDocument(t, document, "game_character", 0)["budget"] = map[string]any{"max_triangles": 4000}
			},
			want: "min_triangles",
		},
		{
			name: "a variant budget below a purpose override it keeps",
			change: func(t *testing.T, document map[string]any) {
				// The purpose asks Meshy T2.5 for 20000 triangles; a 10000
				// budget with no override for it would report its own value.
				platformVariantDocument(t, document, "game_character", 0)["budget"] = map[string]any{"max_triangles": 10000}
			},
			want: "超出用途预算",
		},
		{
			name: "a variant override above the triangle face range",
			change: func(t *testing.T, document map[string]any) {
				variant := platformVariantDocument(t, document, "game_character", 1)
				variant["parameter_overrides"].(map[string]any)[p2] = map[string]any{"face_limit": 60000}
			},
			// P2 accepts at most 50000 triangles (the purpose no longer asks for quads).
			want: "不能大于 50000",
		},
		{
			name: "a variant override above the quad face range",
			change: func(t *testing.T, document map[string]any) {
				variant := platformVariantDocument(t, document, "game_character", 1)
				variant["parameter_overrides"].(map[string]any)[p2] = map[string]any{"face_limit": 30000, "quad": true}
			},
			// P2 accepts at most 25000 faces once quad output is on: the range follows
			// the combination, so a purpose that asked for quads again would be held to it.
			want: "48–25000",
		},
		{
			name: "a variant override of a model the purpose does not recommend",
			change: func(t *testing.T, document map[string]any) {
				variant := platformVariantDocument(t, document, "game_character", 1)
				variant["parameter_overrides"].(map[string]any)["tripo/tripo-v3.1-full/text-to-3d"] = map[string]any{"face_limit": 20000}
			},
			want: "不在用途的推荐或覆盖列表中",
		},
		{
			name: "a variant override of a parameter the model does not have",
			change: func(t *testing.T, document map[string]any) {
				variant := platformVariantDocument(t, document, "game_character", 1)
				variant["parameter_overrides"].(map[string]any)[p2] = map[string]any{"no_such_parameter": 1}
			},
			want: "不属于 Offering",
		},
		{
			name: "variants without a platform field",
			change: func(t *testing.T, document map[string]any) {
				delete(usageDocument(document), "platform_field")
			},
			want: "platform_field",
		},
		{
			name: "a platform field that is not a select",
			change: func(t *testing.T, document map[string]any) {
				usageDocument(document)["platform_field"] = "style"
			},
			want: "select",
		},
		{
			name: "a platform field the brief does not declare",
			change: func(t *testing.T, document map[string]any) {
				usageDocument(document)["platform_field"] = "no_such_field"
			},
			want: "select",
		},
		{
			name: "no sentence naming the platform budget",
			change: func(t *testing.T, document map[string]any) {
				delete(productionDocument(document), "platform_budget_text")
			},
			want: "platform_budget_text",
		},
		{
			name: "a platform sentence without the platform",
			change: func(t *testing.T, document map[string]any) {
				productionDocument(document)["platform_budget_text"] = "按目标平台的预算判定。"
			},
			want: "{platform}",
		},
		{
			name: "an unavailable template without options",
			change: func(t *testing.T, document map[string]any) {
				fix := productionFixDocument(t, document, "texture_size")
				fix["template"] = "贴图超过用途上限。"
				fix["options"] = []any{}
			},
			want: "unavailable_template 需要声明 options",
		},
		{
			name: "an unavailable template with a placeholder no one fills",
			change: func(t *testing.T, document map[string]any) {
				productionFixDocument(t, document, "texture_size")["unavailable_template"] = "没有 {capabilities}。"
			},
			want: "未填充的占位符",
		},
		{
			name: "a texture fix that does not name the bound",
			change: func(t *testing.T, document map[string]any) {
				productionFixDocument(t, document, "texture_size")["template"] = "可用 {capabilities} 降采样。"
			},
			want: "必须包含 {maximum}",
		},
		{
			name: "a bound placeholder in the value of a fix that declares none",
			change: func(t *testing.T, document map[string]any) {
				fix := productionFixDocument(t, document, "pivot_bottom_center")
				capability := fix["options"].([]any)[0].(map[string]any)["capabilities"].([]any)[0].(map[string]any)
				capability["value"] = "{maximum}"
			},
			want: "未填充的占位符",
		},
		{
			name: "a bound placeholder aimed at a parameter that is not a number",
			change: func(t *testing.T, document map[string]any) {
				fix := productionFixDocument(t, document, "texture_size")
				capability := fix["options"].([]any)[0].(map[string]any)["capabilities"].([]any)[0].(map[string]any)
				capability["parameter"] = "format"
			},
			want: "没有支持该修复的 Offering",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) { test.change(t, document) })
			if err == nil {
				t.Fatal("the Catalog accepted an invalid platform declaration")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

// A fix value written as {maximum} is filled with the purpose's bound when the
// hint is built, so the Catalog only needs a public parameter that can take a
// number: a number parameter, or a select whose options are numbers.
func TestFixBoundPlaceholderNeedsAParameterThatTakesNumbers(t *testing.T) {
	review := ChatModelReviewSchema{Inspect: ChatModelInspectSchema{Production: ChatModelInspectProductionSchema{
		Fixes: []ChatModelInspectFixSchema{{
			Check: ModelReviewUsageCheckTextureSize,
			Options: []ChatModelInspectFixOptionSchema{{Capabilities: []ChatModelInspectFixCapabilitySchema{
				{Capability: "format-conversion", Parameter: "texture_size", Value: "{maximum}"},
			}}},
		}},
	}}}
	tests := []struct {
		name      string
		parameter schemacontract.Parameter
		accepted  bool
	}{
		{"a number", schemacontract.Parameter{Name: "texture_size", Type: "number"}, true},
		{"a select of numbers", schemacontract.Parameter{Name: "texture_size", Type: "select",
			Options: []schemacontract.ParameterOption{{Value: "1024"}, {Value: "2048"}}}, true},
		{"a select of names", schemacontract.Parameter{Name: "texture_size", Type: "select",
			Options: []schemacontract.ParameterOption{{Value: "small"}, {Value: "large"}}}, false},
		{"a boolean", schemacontract.Parameter{Name: "texture_size", Type: "boolean"}, false},
		{"a fixed number", schemacontract.Parameter{Name: "texture_size", Type: "number", Fixed: true,
			Default: json.RawMessage(`2048`)}, false},
		{"a number the Agent may not set", schemacontract.Parameter{Name: "texture_size", Type: "number",
			Presentation: &schemacontract.ParameterPresentation{Surface: schemacontract.SurfaceHidden}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offerings := generationMap{"test/convert": schemacontract.Offering{
				ID: "test/convert", Capability: schemacontract.Capability("format-conversion"),
				Parameters: []schemacontract.Parameter{test.parameter},
			}}
			err := validateModelReviewProductionOfferings(review, offerings)
			if test.accepted && err != nil {
				t.Fatalf("validateModelReviewProductionOfferings = %v, want nil", err)
			}
			if !test.accepted && (err == nil || !strings.Contains(err.Error(), "没有支持该修复的 Offering")) {
				t.Fatalf("error = %v, want no supporting Offering", err)
			}
		})
	}
}

// The texture_size fix names the downsample capability through the generic
// {capabilities} mechanism, so it appears by itself once an Offering that can
// resize textures is enabled, and it says what to do while none is.
func TestTextureSizeFixOffersTheDownsampleCapabilityOnceEnabled(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var fix ChatModelInspectFixSchema
	for _, candidate := range product.Application().Chat.ModelReview.Inspect.Production.Fixes {
		if candidate.Check == ModelReviewUsageCheckTextureSize {
			fix = candidate
		}
	}
	if len(fix.Options) != 1 || len(fix.Options[0].Capabilities) != 1 {
		t.Fatalf("texture_size fix options = %+v", fix.Options)
	}
	capability := fix.Options[0].Capabilities[0]
	if capability.Capability != "format-conversion" || capability.Parameter != "texture_size" || capability.Value != "{maximum}" {
		t.Fatalf("texture_size fix capability = %+v", capability)
	}
	if !strings.Contains(fix.Template, "{capabilities}") || !strings.Contains(fix.Template, "{maximum}") ||
		!strings.Contains(fix.UnavailableTemplate, "重新生成") || !strings.Contains(fix.UnavailableTemplate, "{maximum}") {
		t.Fatalf("texture_size fix = %+v", fix)
	}
	// The capability is declared by an Offering of the Catalog, enabled or not
	// (a disabled one is a declaration the runtime hint filters out): a number
	// parameter that a conversion step can set.
	found := false
	for _, offering := range product.byID {
		if string(offering.Capability) != capability.Capability {
			continue
		}
		for _, parameter := range offering.Parameters {
			found = found || (parameter.Name == capability.Parameter && parameter.Type == "number")
		}
	}
	if !found {
		t.Fatalf("no Offering declares %s with a number parameter %s", capability.Capability, capability.Parameter)
	}
}
