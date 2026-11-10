package chat

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func describedModelEnum(t *testing.T, registry *toolRegistry, name string) ([]string, bool) {
	t.Helper()
	for _, tool := range registry.describe("3d") {
		if tool.Name != name {
			continue
		}
		var parameters struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		return parameters.Properties["model"].Enum, true
	}
	return nil, false
}

func knowledgeOffering(id, modelID string, capability schema.Capability, enabled bool, parameters ...schema.Parameter) generation.Offering {
	return generation.Offering{ID: generation.OfferingID(id), ModelID: modelID, Capability: capability,
		Enabled: enabled, Parameters: parameters}
}

func TestToolRegistryDerivesModelEnumFromAvailableOfferings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := compileToolRegistry(product.Application().Chat, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	prompt := schema.Parameter{Name: "prompt", Semantic: schema.SemanticPrompt}
	reference := schema.Parameter{Name: "image", Semantic: schema.SemanticReferenceImage}
	views := schema.Parameter{Name: "views", Semantic: schema.SemanticReferenceViews}
	offerings := map[string][]generation.Offering{
		"text-to-3d": {
			knowledgeOffering("tripo/tripo-p2/text-to-3d", "tripo-p2-text", schema.TextTo3D, true, prompt),
			knowledgeOffering("meshy/meshy-7.1/text-to-3d", "meshy-7.1", schema.TextTo3D, false, prompt),
			knowledgeOffering("hyper3d/rodin-gen-2.5/text-to-3d", "rodin-gen-2.5", schema.TextTo3D, true, prompt),
		},
		"image-to-3d": {
			knowledgeOffering("tripo/tripo-v3.1-full/image-to-3d", "tripo-v3.1-full", schema.ImageTo3D, true, reference),
			knowledgeOffering("tripo/tripo-p2/image-to-3d", "tripo-p2", schema.ImageTo3D, true, reference),
			knowledgeOffering("meshy/meshy-7.1-multiview/image-to-3d", "meshy-7.1-multiview", schema.ImageTo3D, true, reference),
			knowledgeOffering("tripo/geometry/smartsegment-image", "tripo-smartsegment-image", schema.ImageTo3D, true, reference),
			knowledgeOffering("tripo/tripo-v3.1-multiview/image-to-3d", "tripo-v3.1-multiview", schema.ImageTo3D, true, reference, views),
			knowledgeOffering("tripo/tripo-p2-multiview/image-to-3d", "tripo-p2-multiview", schema.ImageTo3D, true, reference, views),
		},
	}
	registry.offerings = func(capability string) []generation.Offering { return offerings[capability] }
	registry.offeringAllowed = func(capability, modelID string) bool {
		for _, offering := range offerings[capability] {
			if offering.ModelID == modelID && offering.Enabled {
				return true
			}
		}
		return false
	}

	if got, found := describedModelEnum(t, registry, "create_model_3d"); !found || !slices.Equal(got, []string{"tripo-p2-text", "rodin-gen-2.5"}) {
		t.Fatalf("create_model_3d enum = %v (found %v)", got, found)
	}
	if got, _ := describedModelEnum(t, registry, "create_model_3d_from_image"); !slices.Equal(got, []string{"tripo-v3.1-full", "tripo-p2"}) {
		t.Fatalf("create_model_3d_from_image enum = %v", got)
	}
	if got, _ := describedModelEnum(t, registry, "create_model_3d_from_views"); !slices.Equal(got, []string{"tripo-v3.1-multiview", "tripo-p2-multiview"}) {
		t.Fatalf("create_model_3d_from_views enum = %v", got)
	}

	// An Offering availability check can change between turns without editing
	// the Schema: disabling P2 removes it from the model enum.
	registry.offeringAllowed = func(capability, modelID string) bool { return modelID != "tripo-p2-text" }
	if got, _ := describedModelEnum(t, registry, "create_model_3d"); !slices.Equal(got, []string{"rodin-gen-2.5"}) {
		t.Fatalf("narrowed enum = %v", got)
	}

	// No available choice at all withholds the tool.
	registry.offerings = func(string) []generation.Offering { return nil }
	if _, found := describedModelEnum(t, registry, "create_model_3d"); found {
		t.Fatal("a model tool with no derived choice stayed offered")
	}
}

func TestToolRegistryKeepsHistoricalDeclaredModelEnum(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	for index := range chatSchema.Tools {
		tool := &chatSchema.Tools[index]
		if tool.Name != "create_model_3d" {
			continue
		}
		tool.ModelChoices = nil
		var parameters map[string]any
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		properties := parameters["properties"].(map[string]any)
		model := properties["model"].(map[string]any)
		model["enum"] = []any{"legacy-model"}
		encoded, err := json.Marshal(parameters)
		if err != nil {
			t.Fatal(err)
		}
		tool.Parameters = encoded
	}
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := describedModelEnum(t, registry, "create_model_3d"); !slices.Equal(got, []string{"legacy-model"}) {
		t.Fatalf("historical enum = %v", got)
	}
}
