package chat

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func describedTemplates(t *testing.T, r *toolRegistry) ([]string, bool) {
	t.Helper()
	for _, described := range r.describe("3d") {
		if described.Name != "create_creative_lab_design" {
			continue
		}
		var parameters struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(described.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		return parameters.Properties["template"].Enum, true
	}
	return nil, false
}

// #189: the model is offered exactly the Creative Lab templates whose Offering
// is enabled and available this turn; with none, the tool is not offered.
func TestCreativeLabDesignToolOffersOnlyEnabledTemplates(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r, err := compileToolRegistry(c.Application().Chat, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	declared, offered := describedTemplates(t, r)
	if !offered || len(declared) != 9 {
		t.Fatalf("without an availability source the declared enum is kept: %v", declared)
	}
	enabled := map[string]bool{"creative-lab-figure-prototype": true, "creative-lab-fidget-collapsible-generate": true}
	r.offeringAllowed = func(capability, model string) bool {
		return enabled[model] && (capability == "creative-lab-prototype" || capability == "creative-lab-build")
	}
	got, offered := describedTemplates(t, r)
	if !offered || !slices.Equal(got, []string{"creative-lab-figure-prototype", "creative-lab-fidget-collapsible-generate"}) {
		t.Fatalf("offered templates = %v (offered=%v)", got, offered)
	}
	if _, offered := describedTemplates(t, r); !offered || r.scopeBlocks(r.byName["create_creative_lab_design"], "2d", false) == false {
		t.Fatal("the design tool belongs to the 3D scope")
	}
	enabled = map[string]bool{}
	if _, offered := describedTemplates(t, r); offered || r.permits(r.byName["create_creative_lab_design"], false) {
		t.Fatal("a design tool without any enabled template is still offered")
	}
}
