package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #360: the system prompt first asks the concept count and only draws after
// the answer; the quantity placeholders are replaced and the block disappears
// in 3D mode, where the sketch tool is not offered.
func TestConceptDirectionsPromptAsksTheQuantityFirst(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{schema: schema, registry: registry}
	prompt := orchestrator.conceptDirectionsPrompt(registry, "2d")
	if !strings.Contains(prompt, schema.ConceptDirections.Quantity.Kind) {
		t.Fatalf("prompt lacks the quantity kind: %q", prompt)
	}
	for _, placeholder := range []string{
		"{minimum}", "{maximum}", "{quantity_minimum}", "{quantity_maximum}",
		"{quantity_kind}", "{sketch_tool}", "{direction_kind}",
	} {
		if strings.Contains(prompt, placeholder) {
			t.Fatalf("prompt kept %s: %q", placeholder, prompt)
		}
	}
	for _, rule := range []string{"这一轮不要调用", "用户一开始就说了张数时不再询问", "N=1"} {
		if !strings.Contains(prompt, rule) {
			t.Fatalf("prompt lacks %q: %q", rule, prompt)
		}
	}
	if prompt := orchestrator.conceptDirectionsPrompt(registry, "3d"); prompt != "" {
		t.Fatalf("3D scope carries concept sketches: %q", prompt)
	}
}
