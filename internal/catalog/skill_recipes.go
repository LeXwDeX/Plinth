package catalog

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func validateSkillRecipes(recipes []ChatSkillRecipeSchema) error {
	seen := map[string]bool{}
	for _, recipe := range recipes {
		if strings.TrimSpace(recipe.ID) == "" || strings.TrimSpace(recipe.Revision) == "" ||
			seen[recipe.ID] || len(recipe.Steps) == 0 || len(recipe.Steps) > 16 {
			return fmt.Errorf("invalid skill recipe %q", recipe.ID)
		}
		seen[recipe.ID] = true
		prior := map[string]bool{}
		for _, step := range recipe.Steps {
			if strings.TrimSpace(step.Key) == "" || prior[step.Key] ||
				strings.TrimSpace(step.Label) == "" || step.Capability != schema.ImageToImage ||
				strings.TrimSpace(step.PromptTemplate) == "" || strings.TrimSpace(step.OutputRole) == "" ||
				(step.SourceStepKey != "" && !prior[step.SourceStepKey]) {
				return fmt.Errorf("invalid step %q in skill recipe %q", step.Key, recipe.ID)
			}
			for key, value := range step.StaticInput {
				if strings.TrimSpace(key) == "" || len(value) == 0 || !json.Valid(value) {
					return fmt.Errorf("invalid static input %q in skill recipe %q", key, recipe.ID)
				}
			}
			prior[step.Key] = true
		}
	}
	return nil
}
