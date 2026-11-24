package chat

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
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
// Schema's offering_capabilities. A count parameter the Offering's current
// parameter combination omits (its own omit_when, read against the declared
// defaults and the session's values) is named too: the count is written but
// never sent, so the model will not produce it.
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
				if reasons := orchestrator.omittedCountReasons(offering, parameter, ownerID, sessionID); len(reasons) > 0 {
					limits.WriteString(strings.NewReplacer(
						"{model}", model, "{count}", strconv.Itoa(count),
						"{reason}", strings.Join(reasons, prompt.ModelSeparator),
					).Replace(prompt.OmittedTemplate))
					continue
				}
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

// omittedCountReasons reports, for an Offering's count parameter, each of its
// omit_when conditions that the Offering's current values match, rendered by
// the controlling parameter's label and the value's option label (#564). The
// values are the declared defaults overlaid by the session's own values; a
// value cleared to the default ("null") keeps the default. No reason means
// the count is sent.
func (orchestrator *Orchestrator) omittedCountReasons(offering generation.Offering, count schema.Parameter, ownerID, sessionID string) []string {
	if len(count.OmitWhen) == 0 {
		return nil
	}
	values := map[string]any{}
	for _, parameter := range offering.Parameters {
		var value any
		if len(parameter.Default) > 0 && json.Unmarshal(parameter.Default, &value) == nil && value != nil {
			values[parameter.Name] = value
		}
	}
	if orchestrator.offeringParameters != nil {
		for name, encoded := range orchestrator.offeringParameters(ownerID, sessionID, string(offering.ID)) {
			var value any
			if json.Unmarshal(encoded, &value) == nil && value != nil {
				values[name] = value
			}
		}
	}
	template := orchestrator.schema.ImageCount.ReasonTemplate
	var reasons []string
	for _, condition := range count.OmitWhen {
		if !schema.ConditionMatches(condition, values) {
			continue
		}
		label, shown := condition.Parameter, ""
		var expected any
		_ = json.Unmarshal(condition.Value, &expected)
		if text, ok := expected.(string); ok {
			shown = text
		}
		for _, parameter := range offering.Parameters {
			if parameter.Name != condition.Parameter {
				continue
			}
			if parameter.Label != "" {
				label = parameter.Label
			}
			for _, option := range parameter.Options {
				if option.Value == shown && option.Label != "" {
					shown = option.Label
				}
			}
		}
		reasons = append(reasons, strings.NewReplacer("{parameter}", label, "{value}", shown).Replace(template))
	}
	return reasons
}
