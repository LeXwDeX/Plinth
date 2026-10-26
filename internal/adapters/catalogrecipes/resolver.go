// Package catalogrecipes adapts validated Product Schema declarations to the
// SkillRun recipe port. Catalog owns declarations; SkillRun owns execution.
package catalogrecipes

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

type Resolver struct{ catalog *catalog.Catalog }

func New(source *catalog.Catalog) *Resolver { return &Resolver{catalog: source} }

var _ skillrun.RecipeResolver = (*Resolver)(nil)

// ResolveRecipe materializes the immutable recipe against the human-selected
// Offering. Only Schema parameter semantics determine the wire input names.
func (r *Resolver) ResolveRecipe(_ context.Context, id string, selected generation.OfferingID) (skillrun.Recipe, bool, error) {
	offering, ok := r.catalog.Get(selected)
	if !ok || !offering.Enabled {
		return skillrun.Recipe{}, false, nil
	}
	for _, declaration := range r.catalog.Application().Chat.Skills.Recipes {
		if declaration.ID != id {
			continue
		}
		recipe := skillrun.Recipe{ID: declaration.ID, Revision: declaration.Revision}
		for _, declared := range declaration.Steps {
			if declared.Capability != offering.Capability {
				return skillrun.Recipe{}, false, nil
			}
			prompt, source := "", ""
			for _, parameter := range offering.Parameters {
				switch parameter.Semantic {
				case schema.SemanticPrompt:
					if parameter.Type == "text" && prompt == "" {
						prompt = parameter.Name
					}
				case schema.SemanticReferenceImage:
					if parameter.Type == "asset-array" && source == "" {
						source = parameter.Name
					}
				}
			}
			if prompt == "" || source == "" {
				return skillrun.Recipe{}, false, nil
			}
			outputFound := false
			for _, output := range offering.Outputs {
				if output.Role == declared.OutputRole && output.Minimum != nil && *output.Minimum > 0 {
					outputFound = true
				}
			}
			if !outputFound {
				return skillrun.Recipe{}, false, nil
			}
			recipe.Steps = append(recipe.Steps, skillrun.StepSpec{
				Key: declared.Key, Label: declared.Label, Capability: declared.Capability,
				OfferingID: offering.ID, OfferingRevision: offering.Revision,
				PromptTemplate: declared.PromptTemplate, PromptParameter: prompt,
				SourceParameter: source, StaticInput: declared.StaticInput,
				OutputRole: declared.OutputRole, SourceStepKey: declared.SourceStepKey,
			})
		}
		if err := skillrun.ValidateRecipe(recipe); err != nil {
			return skillrun.Recipe{}, false, err
		}
		return recipe, true, nil
	}
	return skillrun.Recipe{}, false, nil
}
