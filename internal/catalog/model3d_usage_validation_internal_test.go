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
