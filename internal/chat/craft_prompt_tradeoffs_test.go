package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func craftPromptFixture(t *testing.T, offerings func(string) []generation.Offering, allowed func(string, string) bool) (*Orchestrator, *toolRegistry, catalog.ChatAgentCraftPrompt) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	registry.offerings = offerings
	registry.offeringAllowed = allowed
	var craft catalog.ChatAgentCraftPrompt
	for _, prompt := range chatSchema.Agent.CraftPrompts {
		if prompt.ID == "model3d" {
			craft = prompt
		}
	}
	if craft.ID == "" {
		t.Fatal("model3d craft prompt missing")
	}
	return &Orchestrator{schema: chatSchema}, registry, craft
}

func TestCraftPromptTradeoffsComeFromOfferingKnowledge(t *testing.T) {
	offering := func(id, provider, model string, enabled bool, strengths, weaknesses []string) generation.Offering {
		return generation.Offering{
			ID: generation.OfferingID(id), ProviderID: provider, ModelID: model, ModelName: model,
			Capability: schema.TextTo3D, Enabled: enabled,
			Knowledge: &schema.OfferingKnowledge{Strengths: strengths, Weaknesses: weaknesses},
		}
	}
	offerings := []generation.Offering{
		offering("p2", "tripo", "tripo-p2-text", true, []string{"low poly"}, []string{"less detail"}),
		offering("meshy", "meshy", "meshy-t2.5", true, []string{"fast"}, []string{"coarse"}),
		offering("disabled", "tripo", "disabled-model", false, []string{"never shown"}, nil),
		// The same provider/model pair reached through another capability must
		// not appear twice.
		{ID: "p2-image", ProviderID: "tripo", ModelID: "tripo-p2-text", ModelName: "tripo-p2-text",
			Capability: schema.ImageTo3D, Enabled: true,
			Knowledge: &schema.OfferingKnowledge{Strengths: []string{"low poly"}, Weaknesses: []string{"less detail"}}},
	}
	offeringsByCapability := func(capability string) []generation.Offering {
		if capability == string(schema.TextTo3D) || capability == string(schema.ImageTo3D) {
			return offerings
		}
		return nil
	}
	orchestrator, registry, craft := craftPromptFixture(t, offeringsByCapability,
		func(_, modelID string) bool { return modelID != "disabled-model" })

	lines := registry.offeringTradeoffs(craft)
	if len(lines) != 2 {
		t.Fatalf("tradeoff lines = %v", lines)
	}
	if !strings.Contains(lines[0], "tripo-p2-text") || !strings.Contains(lines[0], "low poly") || !strings.Contains(lines[0], "less detail") {
		t.Fatalf("first line = %q", lines[0])
	}
	if strings.Contains(strings.Join(lines, "\n"), "never shown") {
		t.Fatal("a disabled Offering appeared in the tradeoff lines")
	}

	block := orchestrator.craftPrompt(registry, "3d", "model-3d")
	if strings.Contains(block, "{offering_tradeoffs}") {
		t.Fatal("craft prompt kept the unresolved placeholder")
	}
	for _, line := range lines {
		if !strings.Contains(block, line) {
			t.Fatalf("craft prompt is missing %q", line)
		}
	}
}

func TestCraftPromptDropsTheTradeoffBlockWhenNoOfferingSourceExists(t *testing.T) {
	orchestrator, registry, _ := craftPromptFixture(t, nil, nil)
	block := orchestrator.craftPrompt(registry, "3d", "model-3d")
	if strings.Contains(block, "{offering_tradeoffs}") {
		t.Fatal("craft prompt kept the unresolved placeholder")
	}
	if strings.Contains(block, "\n\n\n") {
		t.Fatalf("empty tradeoff line left a double blank line: %q", block)
	}
	if !strings.Contains(block, "【3D 制作要领】") {
		t.Fatal("the rest of the craft prompt disappeared")
	}
}
