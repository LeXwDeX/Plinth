package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestDefaultCatalogDerivesEveryModelArgumentEnum(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	tools := product.Application().Chat.Tools
	for _, name := range []string{"create_model_3d", "create_model_3d_from_image", "create_model_3d_from_views"} {
		var tool ChatToolSchema
		for _, candidate := range tools {
			if candidate.Name == name {
				tool = candidate
			}
		}
		if tool.Name == "" || tool.ModelChoices == nil {
			t.Fatalf("%s does not declare model_choices", name)
		}
		var parameters struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		if len(parameters.Properties[tool.ModelArgument].Enum) != 0 {
			t.Fatalf("%s still writes a model enum", name)
		}
	}
}

// TestChatToolModelChoicesRejections exercises the loader through document
// mutations, so both the schema validation and the Offering match are real.
func TestChatToolModelChoicesRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(tool map[string]any)
		want   string
	}{
		"missing model_choices in a knowledge catalog": {
			change: func(tool map[string]any) { delete(tool, "model_choices") },
			want:   "必须声明 model_choices",
		},
		"handwritten enum": {
			change: func(tool map[string]any) {
				properties := tool["parameters"].(map[string]any)["properties"].(map[string]any)
				model := properties["model"].(map[string]any)
				model["enum"] = []any{"tripo-v3.1-full"}
			},
			want: "不得再写 enum",
		},
		"unknown source": {
			change: func(tool map[string]any) {
				tool["model_choices"].(map[string]any)["from"] = "all_offerings"
			},
			want: "available_offerings",
		},
		"semantic required and excluded": {
			change: func(tool map[string]any) {
				choices := tool["model_choices"].(map[string]any)
				choices["require_parameters"] = []any{"prompt"}
				choices["exclude_parameters"] = []any{"prompt"}
			},
			want: "不能同时出现",
		},
		"unknown excluded Offering": {
			change: func(tool map[string]any) {
				tool["model_choices"].(map[string]any)["exclude_offerings"] = []any{"no/such/offering"}
			},
			want: "不存在",
		},
		"no matching Offering": {
			change: func(tool map[string]any) {
				tool["model_choices"].(map[string]any)["require_parameters"] = []any{"mask"}
			},
			want: "没有任何已启用且匹配",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				chat := document["application"].(map[string]any)["chat"].(map[string]any)
				for _, item := range chat["tools"].([]any) {
					tool := item.(map[string]any)
					if tool["name"] == "create_model_3d" {
						tc.change(tool)
						return
					}
				}
				t.Fatal("fixture tool missing")
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// #243: the multiview tool selects every enabled multiview Offering, including
// an order-only one whose pictures carry the views by position (Meshy 7.1
// multiview). A plain multi-reference Offering is not a multiview step and
// stays out; a single-picture 3D tool keeps the order-only model out too.
func TestMultiviewModelChoicesIncludeOrderOnlyOfferings(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var multiview schemacontract.Offering
	for _, offering := range product.List("") {
		if offering.ID == "meshy/meshy-7.1-multiview/image-to-3d" {
			multiview = offering
		}
	}
	if multiview.ID == "" || !multiview.Enabled {
		t.Fatalf("meshy multiview fixture = %+v", multiview)
	}
	for _, parameter := range multiview.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceViews {
			t.Fatalf("meshy multiview now declares reference-views: it no longer exercises the order-only path")
		}
	}
	var fromViews, fromImage ChatToolSchema
	for _, candidate := range product.Application().Chat.Tools {
		switch candidate.Name {
		case "create_model_3d_from_views":
			fromViews = candidate
		case "create_model_3d_from_image":
			fromImage = candidate
		}
	}
	if fromViews.Name == "" || fromImage.Name == "" {
		t.Fatal("3D tools are not declared")
	}
	choices := DerivedModelChoices(fromViews, product.List(""), nil)
	for _, want := range []string{"tripo-v3.1-multiview", "tripo-p2-multiview", "meshy-7.1-multiview"} {
		if !slices.Contains(choices, want) {
			t.Fatalf("create_model_3d_from_views choices %v lack %q", choices, want)
		}
	}
	if slices.Contains(choices, "rodin-gen-2.5") {
		t.Fatalf("a plain multi-reference Offering was selected as multiview: %v", choices)
	}
	if slices.Contains(DerivedModelChoices(fromImage, product.List(""), nil), "meshy-7.1-multiview") {
		t.Fatal("the single-picture 3D tool selects an order-only multiview Offering")
	}
}

func TestDerivedModelChoicesFiltersEnabledAvailableAndSemantics(t *testing.T) {
	tool := ChatToolSchema{
		ModelArgument: "model",
		ModelChoices: &ChatToolModelChoicesSchema{
			From:              "available_offerings",
			RequireParameters: []schemacontract.ParameterSemantic{schemacontract.SemanticReferenceImage},
		},
		Execution: ChatToolExecutionSchema{Capabilities: []schemacontract.Capability{schemacontract.ImageTo3D}},
	}
	prompt := schemacontract.Parameter{Name: "prompt", Semantic: schemacontract.SemanticPrompt}
	reference := schemacontract.Parameter{Name: "image", Semantic: schemacontract.SemanticReferenceImage}
	offerings := []schemacontract.Offering{
		{ID: "a", ModelID: "model-a", Capability: schemacontract.ImageTo3D, Enabled: true, Parameters: []schemacontract.Parameter{reference}},
		{ID: "b", ModelID: "model-b", Capability: schemacontract.ImageTo3D, Enabled: false, Parameters: []schemacontract.Parameter{reference}},
		{ID: "c", ModelID: "model-c", Capability: schemacontract.ImageTo3D, Enabled: true, Parameters: []schemacontract.Parameter{reference, prompt}},
		{ID: "d", ModelID: "model-a", Capability: schemacontract.ImageTo3D, Enabled: true, Parameters: []schemacontract.Parameter{reference}},
		{ID: "e", ModelID: "model-e", Capability: schemacontract.TextTo3D, Enabled: true, Parameters: []schemacontract.Parameter{reference}},
	}
	got := DerivedModelChoices(tool, offerings, func(id schemacontract.OfferingID) bool { return id != "a" && id != "d" })
	if !slices.Equal(got, []string{"model-c"}) {
		t.Fatalf("choices = %v, want [model-c]", got)
	}
	all := DerivedModelChoices(tool, offerings, nil)
	if !slices.Equal(all, []string{"model-a", "model-c"}) {
		t.Fatalf("deduplicated choices = %v", all)
	}
	excluded := ChatToolModelChoicesSchema{From: "available_offerings", RequireParameters: []schemacontract.ParameterSemantic{schemacontract.SemanticReferenceImage}, ExcludeOfferings: []string{"a"}}
	tool.ModelChoices = &excluded
	if got := DerivedModelChoices(tool, offerings, nil); !slices.Equal(got, []string{"model-c", "model-a"}) {
		t.Fatalf("excluded choices = %v", got)
	}
}
