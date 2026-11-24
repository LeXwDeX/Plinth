package app

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// sessionParameterGroups reads, for the Agent's system prompt (#564), the
// parameter values a session holds per Offering: the person's panel values
// the Offering lets them set and the Agent's set_params values it lets the
// Agent set, in declared order, each shown by its label and option label.
// The image count is left to its own block, and a fixed or hidden parameter,
// a cleared value and a disabled or unknown Offering are left out, so the
// list is what the next call would really run with.
func sessionParameterGroups(
	store *agentparams.Store,
	lookup func(generation.OfferingID) (generation.Offering, bool),
) func(ownerID, sessionID string) []chat.SessionParameterGroup {
	return func(ownerID, sessionID string) []chat.SessionParameterGroup {
		scope := agentparams.ScopeKey(ownerID, sessionID)
		var groups []chat.SessionParameterGroup
		for _, id := range store.OfferingIDs(scope) {
			offering, ok := lookup(generation.OfferingID(id))
			if !ok || !offering.Enabled {
				continue
			}
			states := store.OfferingStates(scope, id)
			group := chat.SessionParameterGroup{Model: offering.ModelName, Capability: string(offering.Capability)}
			if group.Model == "" {
				group.Model = offering.ModelID
			}
			for _, parameter := range offering.Parameters {
				if parameter.Name == offering.EstimatedCreditsUnitParameter || parameter.Fixed ||
					(parameter.Presentation != nil && parameter.Presentation.Surface == schema.SurfaceHidden) {
					continue
				}
				state, held := states[parameter.Name]
				if !held || len(state.Value) == 0 || string(state.Value) == "null" {
					continue
				}
				switch {
				case state.Source == "user" && offering.UserParameterAllowed(parameter):
				case state.Source == "agent" && offering.AgentParameterAllowed(parameter):
				default:
					continue
				}
				group.Parameters = append(group.Parameters, chat.SessionParameterValue{
					Label: parameter.Label, Value: sessionParameterDisplay(parameter, state.Value), Source: state.Source,
				})
			}
			if len(group.Parameters) > 0 {
				groups = append(groups, group)
			}
		}
		return groups
	}
}

// sessionParameterDisplay shows a value by its option label when the
// parameter declares one, and otherwise as written, without JSON quotes.
func sessionParameterDisplay(parameter schema.Parameter, value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) != nil {
		return strings.TrimSpace(string(value))
	}
	for _, option := range parameter.Options {
		if option.Value == text && option.Label != "" {
			return option.Label
		}
	}
	return text
}
