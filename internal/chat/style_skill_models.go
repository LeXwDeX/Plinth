package chat

import (
	"slices"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// StyleSkillModels are the models that can apply a style Skill now (#620):
// the model IDs, in Catalog order and each named once, of the enabled
// Offerings available at runtime that declare the Product Schema's Skill
// feature (chat.skills.feature). The Agent is told these before it picks a
// model for a style Skill, so whether a model applies one is read from the
// Schema rather than learnt by submitting. An empty feature lists none.
func StyleSkillModels(offerings []generation.Offering, feature string, available func(generation.OfferingID) bool) []string {
	if feature == "" {
		return nil
	}
	var models []string
	for _, offering := range offerings {
		if !offering.Enabled || !slices.Contains(offering.Features, feature) {
			continue
		}
		if available != nil && !available(offering.ID) {
			continue
		}
		if offering.ModelID != "" && !slices.Contains(models, offering.ModelID) {
			models = append(models, offering.ModelID)
		}
	}
	return models
}
