package chat

import (
	"strconv"
	"strings"
)

// imageCountPrompt adds the person's own 2D image count to the system prompt
// (#486). The count is one value for the whole session, read from storage on
// every request like the approval mode, so a person who switches Offering is
// still working to the number they set and the Agent never has to guess one or
// ask for it again.
//
// A session with no count on record renders nothing: the Offering's own
// declared default is what the turn will use, and naming a number nobody chose
// would be the guess this block exists to prevent. The words come from
// application.chat.image_count.context_template.
//
// #564: the server writes the count into every call, clamped to each
// Offering's declared range, so the block also names the models whose range
// does not hold it (with the count their calls really run) and the models
// that take no count, from the enabled and available Offerings of the
// Schema's offering_capabilities.
func (orchestrator *Orchestrator) imageCountPrompt(registry *toolRegistry, ownerID, sessionID string) string {
	prompt := orchestrator.schema.ImageCount
	if orchestrator.imageCount == nil || sessionID == "" ||
		strings.TrimSpace(prompt.ContextTemplate) == "" {
		return ""
	}
	count, ok := orchestrator.imageCount(ownerID, sessionID)
	if !ok {
		return ""
	}
	block := strings.ReplaceAll(prompt.ContextTemplate, "{count}", strconv.Itoa(count))
	if registry == nil || registry.offerings == nil || len(prompt.OfferingCapabilities) == 0 {
		return block
	}
	seen := map[string]bool{}
	var limits strings.Builder
	var uncounted []string
	for _, capability := range prompt.OfferingCapabilities {
		for _, offering := range registry.offeringsFor(string(capability)) {
			if offering.Capability != capability || !offering.Enabled {
				continue
			}
			if registry.offeringAllowed != nil && !registry.offeringAllowed(string(capability), offering.ModelID) {
				continue
			}
			key := offering.ProviderID + "\x00" + offering.ModelID
			if seen[key] {
				continue
			}
			seen[key] = true
			model := offering.ModelName
			if model == "" {
				model = offering.ModelID
			}
			counted := false
			for _, parameter := range offering.Parameters {
				if parameter.Name != offering.EstimatedCreditsUnitParameter || parameter.Name == "" ||
					parameter.Fixed || !offering.UserParameterAllowed(parameter) {
					continue
				}
				counted = true
				if runs := parameter.ClampedCount(count); runs != count {
					limits.WriteString(strings.NewReplacer(
						"{model}", model, "{count}", strconv.Itoa(runs),
					).Replace(prompt.LimitTemplate))
				}
			}
			if !counted {
				uncounted = append(uncounted, model)
			}
		}
	}
	block += limits.String()
	if len(uncounted) > 0 {
		block += strings.ReplaceAll(prompt.UncountedTemplate, "{models}",
			strings.Join(uncounted, prompt.ModelSeparator))
	}
	return block
}
