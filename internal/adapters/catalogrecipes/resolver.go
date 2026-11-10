// Package catalogrecipes adapts validated Product Schema declarations to the
// SkillRun recipe port. Catalog owns declarations; SkillRun owns execution.
package catalogrecipes

import (
	"context"
	"strings"

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
// The three-view recipe's subject placeholders are replaced with one declared
// subject type's constraints (#243); an unknown type materializes nothing.
func (r *Resolver) ResolveRecipe(_ context.Context, id string, selected generation.OfferingID,
	subjectType string,
) (skillrun.Recipe, bool, error) {
	offering, ok := r.catalog.Get(selected)
	if !ok || !offering.Enabled {
		return skillrun.Recipe{}, false, nil
	}
	threeView := r.catalog.Application().Chat.ThreeView
	for _, declaration := range r.catalog.Application().Chat.Skills.Recipes {
		if declaration.ID != id {
			continue
		}
		frontConstraints, viewConstraints := subjectConstraints(threeView, id, subjectType)
		if frontConstraints == "" && viewConstraints == "" && usesSubjectPlaceholders(declaration) {
			return skillrun.Recipe{}, false, nil
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
			promptTemplate := strings.NewReplacer(
				"{subject_front_constraints}", frontConstraints,
				"{subject_view_constraints}", viewConstraints,
			).Replace(declared.PromptTemplate)
			recipe.Steps = append(recipe.Steps, skillrun.StepSpec{
				Key: declared.Key, Label: declared.Label, Capability: declared.Capability,
				OfferingID: offering.ID, OfferingRevision: offering.Revision,
				PromptTemplate: promptTemplate, PromptParameter: prompt,
				SourceParameter: source, StaticInput: declared.StaticInput,
				OutputRole: declared.OutputRole, SourceStepKey: declared.SourceStepKey, Optional: declared.Optional,
			})
		}
		if err := skillrun.ValidateRecipe(recipe); err != nil {
			return skillrun.Recipe{}, false, err
		}
		return recipe, true, nil
	}
	return skillrun.Recipe{}, false, nil
}

// subjectConstraints picks the declared subject type of the three-view recipe.
// An empty request means the declaration's default; a recipe other than the
// three-view one has no subject constraints.
func subjectConstraints(threeView catalog.ChatThreeViewSchema, recipeID, requested string) (front, view string) {
	if threeView.RecipeID != recipeID || len(threeView.SubjectTypes) == 0 {
		return "", ""
	}
	wanted := strings.TrimSpace(requested)
	if wanted == "" {
		wanted = threeView.DefaultSubjectType
	}
	for _, subject := range threeView.SubjectTypes {
		if subject.ID == wanted {
			return subject.FrontConstraints, subject.ViewConstraints
		}
	}
	return "", ""
}

func usesSubjectPlaceholders(declaration catalog.ChatSkillRecipeSchema) bool {
	for _, step := range declaration.Steps {
		if strings.Contains(step.PromptTemplate, "{subject_front_constraints}") ||
			strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
			return true
		}
	}
	return false
}
