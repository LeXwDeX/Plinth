package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func usageNumber(value float64) *float64 { return &value }

func usageOffering(parameters ...schemacontract.Parameter) schemacontract.Offering {
	return schemacontract.Offering{
		ID: "test/model/text-to-3d", ModelID: "test-model",
		Capability: schemacontract.TextTo3D, Domain: schemacontract.DomainModel3D,
		Enabled: true, Parameters: parameters,
	}
}

// #103: an override of a purpose must survive the same normalization a paid
// submission runs, and must name a public parameter of the 3D Offering.
func TestProfileParameterSimulationRefusesUnsafeOverrides(t *testing.T) {
	quality := schemacontract.Parameter{
		Name: "quality", Type: "select", Default: json.RawMessage(`"standard"`),
		Options: []schemacontract.ParameterOption{
			{Value: "standard", Label: "Standard"},
			{Value: "high", Label: "High"},
			{Value: "extreme", Label: "Extreme"},
		},
	}
	fixed := schemacontract.Parameter{
		Name: "version", Type: "text", Fixed: true, Default: json.RawMessage(`"v3"`),
	}
	faceLimit := schemacontract.Parameter{
		Name: "face_limit", Type: "number", Integer: true,
		Minimum: usageNumber(100), Maximum: usageNumber(20000),
	}
	forced := schemacontract.Parameter{Name: "quality", Type: "select", Default: json.RawMessage(`"standard"`),
		Options: []schemacontract.ParameterOption{
			{Value: "standard", Label: "Standard"},
			{Value: "high", Label: "High"},
			{Value: "extreme", Label: "Extreme"},
		},
		ForcedWhen: []schemacontract.ForcedValue{{
			When:  schemacontract.ParameterCondition{Parameter: "premium", Operator: "truthy"},
			Value: json.RawMessage(`"extreme"`),
		}},
	}
	premium := schemacontract.Parameter{Name: "premium", Type: "boolean", Default: json.RawMessage(`true`)}
	hidden := schemacontract.Parameter{
		Name: "secret", Type: "text",
		Presentation: &schemacontract.ParameterPresentation{Surface: schemacontract.SurfaceHidden},
	}
	tests := []struct {
		name      string
		offering  schemacontract.Offering
		overrides map[string]json.RawMessage
		want      string
	}{
		{
			name:      "a parameter the Offering does not declare",
			offering:  usageOffering(quality),
			overrides: map[string]json.RawMessage{"missing": json.RawMessage(`1`)},
			want:      "不属于",
		},
		{
			name:      "a fixed parameter",
			offering:  usageOffering(fixed),
			overrides: map[string]json.RawMessage{"version": json.RawMessage(`"v4"`)},
			want:      "不是可公开调整的参数",
		},
		{
			name:      "a hidden parameter",
			offering:  usageOffering(hidden),
			overrides: map[string]json.RawMessage{"secret": json.RawMessage(`"x"`)},
			want:      "不是可公开调整的参数",
		},
		{
			name:      "a value outside the declared range",
			offering:  usageOffering(faceLimit),
			overrides: map[string]json.RawMessage{"face_limit": json.RawMessage(`30000`)},
			want:      "不能大于",
		},
		{
			name:      "a value rewrite forced by another parameter",
			offering:  usageOffering(forced, premium),
			overrides: map[string]json.RawMessage{"quality": json.RawMessage(`"standard"`)},
			want:      "改写或丢弃",
		},
		{
			name:      "a null override",
			offering:  usageOffering(quality),
			overrides: map[string]json.RawMessage{"quality": json.RawMessage(`null`)},
			want:      "null",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := simulateProfileParameters(test.offering, test.overrides)
			if err == nil {
				t.Fatal("simulateProfileParameters accepted an unsafe override")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestProfileParameterSimulationAcceptsAPublicValue(t *testing.T) {
	offering := usageOffering(schemacontract.Parameter{
		Name: "face_limit", Type: "number", Integer: true,
		Minimum: usageNumber(100), Maximum: usageNumber(20000),
	})
	if err := simulateProfileParameters(offering, map[string]json.RawMessage{"face_limit": json.RawMessage(`20000`)}); err != nil {
		t.Fatalf("public override refused: %v", err)
	}
}

// #103 review C3: the service locates the purpose and the triangle override by
// field type, so the validator must accept renamed fields and refuse only the
// shapes the service cannot serve.
func TestProductionBriefValidationLocatesFieldsByType(t *testing.T) {
	brief := func(fields ...ChatProductionBriefField) ChatProductionBriefSchema {
		return ChatProductionBriefSchema{
			Fields:                 fields,
			ContextTemplate:        "{brief}",
			EmptyText:              "none",
			FieldTemplate:          "{label}: {value}",
			FlagTemplate:           "{label}",
			Separator:              "; ",
			PurposeTemplate:        "{label} ({budget})",
			BudgetTemplate:         "{minimum}-{maximum}{requirements}",
			OpenBudgetTemplate:     "{maximum}{requirements}",
			OverrideBudgetTemplate: "{value}{requirements}",
			RequirementsTemplate:   "{items}",
			RequirementSeparator:   ", ",
			UVText:                 "uv",
			WatertightText:         "watertight",
			SkinText:               "skin",
			SavedMessage:           "saved",
			InvalidTemplate:        "invalid: {reason}",
		}
	}
	profile := ChatProductionBriefField{Name: "use", Type: "profile", Label: "Use"}
	text := ChatProductionBriefField{Name: "look", Type: "text", Label: "Look", MaximumCharacters: 20}
	flag := ChatProductionBriefField{Name: "animate", Type: "boolean", Label: "Animate"}
	integer := ChatProductionBriefField{Name: "faces", Type: "integer", Label: "Faces", Minimum: 100, Maximum: 1000}
	renamed := brief(profile, text, flag, integer)
	if err := validateProductionBrief(renamed); err != nil {
		t.Fatalf("renamed fields refused: %v", err)
	}
	if got := renamed.FieldNameOfType("profile"); got != "use" {
		t.Fatalf("profile field = %q", got)
	}
	if got := renamed.FieldNameOfType("integer"); got != "faces" {
		t.Fatalf("integer field = %q", got)
	}
	for _, test := range []struct {
		name  string
		brief ChatProductionBriefSchema
		want  string
	}{
		{name: "no profile field", brief: brief(text, flag, integer), want: "profile"},
		{name: "two profile fields", brief: brief(profile, ChatProductionBriefField{Name: "use2", Type: "profile", Label: "Use"}), want: "profile"},
		{name: "no integer field", brief: brief(profile, text, flag), want: "integer"},
		{name: "two integer fields", brief: brief(profile, text, integer, ChatProductionBriefField{Name: "faces2", Type: "integer", Label: "Faces"}), want: "integer"},
		{name: "an unimplemented type", brief: brief(profile, ChatProductionBriefField{Name: "ratio", Type: "number", Label: "Ratio"}), want: "未实现"},
		{name: "an unbounded text", brief: brief(profile, ChatProductionBriefField{Name: "look", Type: "text", Label: "Look"}), want: "maximum_characters"},
		{name: "an unbounded integer", brief: brief(profile, ChatProductionBriefField{Name: "faces", Type: "integer", Label: "Faces"}), want: "最小/最大值"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateProductionBrief(test.brief)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

// #103 review C3: a Schema that declares the brief refusal messages must
// declare all of them with their placeholders, so no refusal falls back to a
// hardcoded sentence in one language.
func TestProductionBriefMessagesAreCompleteAndPlaceheld(t *testing.T) {
	valid := ChatProductionBriefMessages{
		UnknownField:      `unknown "{field}"`,
		NotObject:         "object required",
		NotString:         `"{field}" must be text`,
		NotBoolean:        `"{field}" must be a boolean`,
		NotInteger:        `"{field}" must be an integer`,
		ProfileUndeclared: `purpose "{value}" is not declared`,
		TextTooLong:       `"{field}" exceeds {maximum}`,
		OutOfRange:        `"{field}" must be {minimum}-{maximum}`,
		UnsupportedType:   `"{field}" has unsupported type "{type}"`,
	}
	if err := validateProductionBriefMessages(valid); err != nil {
		t.Fatalf("valid messages refused: %v", err)
	}
	missing := valid
	missing.OutOfRange = ""
	if err := validateProductionBriefMessages(missing); err == nil || !strings.Contains(err.Error(), "out_of_range") {
		t.Fatalf("a partially declared set = %v", err)
	}
	badPlaceholder := valid
	badPlaceholder.TextTooLong = `"{field}" is too long`
	if err := validateProductionBriefMessages(badPlaceholder); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("a missing placeholder = %v", err)
	}
	if err := validateProductionBriefMessages(ChatProductionBriefMessages{}); err != nil {
		t.Fatalf("a historical Catalog without messages was refused: %v", err)
	}
}

// #103 review C3: the usage-profile kind is located by its option source, so
// it may sit at any position in choice.kinds.
func TestChoiceKindsAcceptTheUsageProfileKindAtAnyPosition(t *testing.T) {
	kind := func(id string) ChatChoiceKindSchema {
		return ChatChoiceKindSchema{
			ID: id, OptionSource: "usage_profiles", ToolName: "update_production_brief",
			Argument: "purpose", Question: "Purpose?", Message: "Choose a purpose.",
		}
	}
	schema := ChatSchema{
		AgentTools: []ChatAgentToolSchema{
			{Name: "ask_choice", Parameters: json.RawMessage(
				`{"type":"object","properties":{"kind":{"type":"string","enum":["first","usage_profile"]}}}`)},
			{Name: "update_production_brief", Parameters: json.RawMessage(
				`{"type":"object","properties":{"purpose":{"type":"string"}}}`)},
		},
		Choice: ChatChoiceSchema{Kinds: []ChatChoiceKindSchema{kind("first"), kind("usage_profile")}},
	}
	if err := validateChoiceKinds(schema); err != nil {
		t.Fatalf("usage_profile at the second position refused: %v", err)
	}
}

func TestModel3DUsageOfferingsRefuseUnknownReferences(t *testing.T) {
	text := schemacontract.Offering{
		ID: "test/text/text-to-3d", ModelID: "text-model",
		Capability: schemacontract.TextTo3D, Domain: schemacontract.DomainModel3D, Enabled: true,
	}
	image := schemacontract.Offering{
		ID: "test/image/image-to-3d", ModelID: "image-model",
		Capability: schemacontract.ImageTo3D, Domain: schemacontract.DomainModel3D, Enabled: true,
	}
	byID := generationMap{"test/text/text-to-3d": text, "test/image/image-to-3d": image}
	profile := func() ChatModel3DUsageProfile {
		return ChatModel3DUsageProfile{
			ID: "p", Label: "P", Description: "d", Reason: "r",
			RecommendedOfferings: map[string][]string{"text-to-3d": {"test/text/text-to-3d"}},
			Budget:               ChatModel3DUsageBudget{MaxTriangles: 1000},
		}
	}
	tests := []struct {
		name   string
		mutate func(*ChatModel3DUsageProfile)
		want   string
	}{
		{
			name: "an Offering that does not exist",
			mutate: func(p *ChatModel3DUsageProfile) {
				p.RecommendedOfferings["text-to-3d"] = []string{"missing/offering"}
			},
			want: "不存在",
		},
		{
			name: "a recommendation of the wrong capability",
			mutate: func(p *ChatModel3DUsageProfile) {
				p.RecommendedOfferings["text-to-3d"] = []string{"test/image/image-to-3d"}
			},
			want: "不是 text-to-3d",
		},
		{
			name: "a multiview recommendation without the views argument",
			mutate: func(p *ChatModel3DUsageProfile) {
				p.RecommendedOfferings["multiview-to-3d"] = []string{"test/image/image-to-3d"}
			},
			want: "未声明多视图参数",
		},
		{
			name: "an override of an Offering that does not exist",
			mutate: func(p *ChatModel3DUsageProfile) {
				p.ParameterOverrides = map[string]map[string]json.RawMessage{
					"missing/offering": {"face_limit": json.RawMessage(`1`)},
				}
			},
			want: "parameter_overrides",
		},
		{
			name: "pipeline parameters without an Offering",
			mutate: func(p *ChatModel3DUsageProfile) {
				p.Pipeline = []ChatModel3DUsagePipelineStep{{
					Capability: "rigging",
					Parameters: map[string]json.RawMessage{"face_limit": json.RawMessage(`1`)},
				}}
			},
			want: "未指定 Offering",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := profile()
			test.mutate(&current)
			usage := ChatModel3DUsageSchema{DefaultProfile: "p", Profiles: []ChatModel3DUsageProfile{current}}
			err := validateModel3DUsageOfferings(usage, byID, "views")
			if err == nil {
				t.Fatal("validateModel3DUsageOfferings accepted an invalid profile")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

// TestShippedUsageBudgetsAndScales pins the five initial purposes: their
// delivery budget and the metre range inspect_model uses to judge scale.
func TestShippedUsageBudgetsAndScales(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	type want struct {
		min, max       int
		hasMin         bool
		texture        int
		uv, watertight bool
		skin           bool
		scaleMin       float64
		scaleMax       float64
		hasScale       bool
	}
	wants := map[string]want{
		"render":         {max: 2000000, texture: 8192},
		"game_character": {min: 5000, max: 60000, hasMin: true, texture: 2048, uv: true, scaleMin: 0.3, scaleMax: 5, hasScale: true},
		"game_prop":      {min: 300, max: 20000, hasMin: true, texture: 2048, uv: true, scaleMin: 0.02, scaleMax: 20, hasScale: true},
		"web_ar":         {max: 100000, texture: 2048, uv: true, scaleMin: 0.05, scaleMax: 10, hasScale: true},
		"print":          {max: 2000000, texture: 4096, watertight: true, scaleMin: 0.01, scaleMax: 2, hasScale: true},
	}
	if len(usage.Profiles) != len(wants) {
		t.Fatalf("profiles = %d, want %d", len(usage.Profiles), len(wants))
	}
	for _, profile := range usage.Profiles {
		expected, exists := wants[profile.ID]
		if !exists {
			t.Fatalf("unexpected purpose %q", profile.ID)
		}
		budget := profile.Budget
		if budget.MaxTriangles != expected.max || budget.MaxTextureSize == nil ||
			*budget.MaxTextureSize != expected.texture || budget.RequireUV != expected.uv ||
			budget.RequireWatertight != expected.watertight || budget.RequireSkin != expected.skin {
			t.Errorf("%s budget = %+v", profile.ID, budget)
		}
		if expected.hasMin {
			if budget.MinTriangles == nil || *budget.MinTriangles != expected.min {
				t.Errorf("%s min_triangles = %v, want %d", profile.ID, budget.MinTriangles, expected.min)
			}
		} else if budget.MinTriangles != nil {
			t.Errorf("%s min_triangles = %v, want none", profile.ID, *budget.MinTriangles)
		}
		if expected.hasScale {
			if profile.ScaleRangeM == nil || profile.ScaleRangeM.Min != expected.scaleMin ||
				profile.ScaleRangeM.Max != expected.scaleMax {
				t.Errorf("%s scale_range_m = %+v, want %v-%v", profile.ID, profile.ScaleRangeM, expected.scaleMin, expected.scaleMax)
			}
		} else if profile.ScaleRangeM != nil {
			t.Errorf("%s scale_range_m = %+v, want none", profile.ID, profile.ScaleRangeM)
		}
	}
}

func TestModelReviewProductionFixesMustMatchOfferings(t *testing.T) {
	resize := schemacontract.Offering{
		ID: "test/resize", Capability: schemacontract.Capability("resize"),
		Parameters: []schemacontract.Parameter{{
			Name: "origin_at", Type: "select", Default: json.RawMessage(`"bottom"`),
			Options: []schemacontract.ParameterOption{{Value: "bottom"}, {Value: "center"}},
		}},
	}
	fixed := schemacontract.Offering{
		ID: "test/fixed", Capability: schemacontract.Capability("resize"),
		Parameters: []schemacontract.Parameter{{
			Name: "origin_at", Type: "select", Fixed: true, Default: json.RawMessage(`"bottom"`),
			Options: []schemacontract.ParameterOption{{Value: "bottom"}},
		}},
	}
	byID := generationMap{
		"test/resize": resize,
		"test/fixed":  fixed,
	}
	review := func(candidates ...ChatModelInspectFixCapabilitySchema) ChatModelReviewSchema {
		return ChatModelReviewSchema{Inspect: ChatModelInspectSchema{
			Production: ChatModelInspectProductionSchema{Fixes: []ChatModelInspectFixSchema{{
				Check: ModelReviewUsageCheckPivotBottomCenter,
				Options: []ChatModelInspectFixOptionSchema{{
					Capabilities: candidates,
				}},
			}}},
		}}
	}
	tests := []struct {
		name       string
		candidates []ChatModelInspectFixCapabilitySchema
		offerings  generationMap
		want       string
	}{
		{
			name:       "a capability without an Offering",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "missing"}},
			offerings:  byID,
			want:       "没有支持该修复的 Offering",
		},
		{
			name:       "a parameter the Offering does not declare",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "resize", Parameter: "missing", Value: "x"}},
			offerings:  byID,
			want:       "没有支持该修复的 Offering",
		},
		{
			name:       "a select value the Offering does not offer",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "resize", Parameter: "origin_at", Value: "top"}},
			offerings:  byID,
			want:       "没有支持该修复的 Offering",
		},
		{
			name:       "an accepted select value",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "resize", Parameter: "origin_at", Value: "bottom"}},
			offerings:  byID,
		},
		{
			name:       "a fixed parameter already at the value",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "resize", Parameter: "origin_at", Value: "bottom"}},
			offerings:  generationMap{"test/fixed": fixed},
		},
		{
			name:       "a fixed parameter at another value",
			candidates: []ChatModelInspectFixCapabilitySchema{{Capability: "resize", Parameter: "origin_at", Value: "center"}},
			offerings:  generationMap{"test/fixed": fixed},
			want:       "没有支持该修复的 Offering",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateModelReviewProductionOfferings(review(test.candidates...), test.offerings)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validateModelReviewProductionOfferings = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestModel3DUsageShapeRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ApplicationSchema)
		want   string
	}{
		{
			name:   "a default profile that does not exist",
			mutate: func(a *ApplicationSchema) { a.Chat.Model3DUsage.DefaultProfile = "missing" },
			want:   "default_profile",
		},
		{
			name: "an unknown input shape",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[0].RecommendedOfferings["texture-to-3d"] = []string{"tripo/tripo-p2/text-to-3d"}
			},
			want: "未知输入形态",
		},
		{
			name: "a budget without a maximum",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[0].Budget.MaxTriangles = 0
			},
			want: "max_triangles",
		},
		{
			name: "a minimum above the maximum",
			mutate: func(a *ApplicationSchema) {
				minimum := 60001
				a.Chat.Model3DUsage.Profiles[1].Budget.MinTriangles = &minimum
			},
			want: "min_triangles",
		},
		{
			name: "a scale range that starts at zero",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[1].ScaleRangeM = &ChatModel3DUsageScaleRange{Min: 0, Max: 5}
			},
			want: "scale_range_m",
		},
		{
			name: "a scale range whose maximum is below its minimum",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[1].ScaleRangeM = &ChatModel3DUsageScaleRange{Min: 5, Max: 1}
			},
			want: "scale_range_m",
		},
		{
			name: "a pipeline capability the application does not declare",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[1].Pipeline[0].Capability = "missing-capability"
			},
			want: "未声明",
		},
		{
			name: "a duplicate profile id",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[1].ID = a.Chat.Model3DUsage.Profiles[0].ID
			},
			want: "重复",
		},
		{
			name: "an incomplete purpose description",
			mutate: func(a *ApplicationSchema) {
				a.Chat.Model3DUsage.Profiles[0].Description = " "
			},
			want: "description",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			product, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := product.Application()
			test.mutate(&application)
			err = ValidateApplicationSchema(application)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid model3d_usage declaration")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}
