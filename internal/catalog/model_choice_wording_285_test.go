package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestModelChoiceWordingAgrees is #285: the system prompt, the plan step's
// model field and list_offerings state one rule. A locked model is never
// switched; without a lock the Agent chooses by the brief's purpose and says
// why. No text may forbid the Agent from choosing at all.
func TestModelChoiceWordingAgrees(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	texts := map[string]string{"system_prompt": chat.Agent.SystemPrompt}
	for _, tool := range chat.AgentTools {
		switch tool.Name {
		case "list_offerings":
			texts["list_offerings"] = tool.Description
		case "propose_production_plan":
			var parameters struct {
				Properties struct {
					Steps struct {
						Items struct {
							Properties map[string]struct {
								Description string `json:"description"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"steps"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				t.Fatal(err)
			}
			texts["plan step model"] = parameters.Properties.Steps.Items.Properties["model"].Description
		}
	}
	if len(texts) != 3 || texts["plan step model"] == "" || texts["list_offerings"] == "" {
		t.Fatalf("model choice texts not found: %v", texts)
	}
	for name, text := range texts {
		for _, forbidden := range []string{"never pick a model on your own", "The human chooses the model"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s still forbids the Agent from choosing: %q", name, forbidden)
			}
		}
	}
	for name, wants := range map[string][]string{
		"plan step model": {"locked", "purpose"},
		"list_offerings":  {"locked", "purpose", "why"},
		"system_prompt":   {"锁定", "用途", "理由"},
	} {
		for _, want := range wants {
			if !strings.Contains(texts[name], want) {
				t.Errorf("%s does not state %q", name, want)
			}
		}
	}
}
