package chat

import "strings"

// SessionParameterGroup is one Offering's parameter values held by a session
// (#564): what the person set on the panel and what the Agent set with
// set_params, in the Offering's declared order.
type SessionParameterGroup struct {
	Model      string
	Capability string
	Parameters []SessionParameterValue
}

// SessionParameterValue is one held value, shown by its label. Source is
// "user" or "agent".
type SessionParameterValue struct {
	Label  string
	Value  string
	Source string
}

// sessionParametersPrompt shows the Agent the parameter values the session
// holds per Offering (#564). The system prompt says the parameters a session
// locked are followed; without this block the Agent learns them only from a
// tool result, after it has submitted. A session that holds none renders
// nothing. The words come from application.chat.session_parameters.
func (orchestrator *Orchestrator) sessionParametersPrompt(ownerID, sessionID string) string {
	config := orchestrator.schema.SessionParameters
	if orchestrator.sessionParameters == nil || sessionID == "" || strings.TrimSpace(config.ContextTemplate) == "" {
		return ""
	}
	var lines []string
	for _, group := range orchestrator.sessionParameters(ownerID, sessionID) {
		values := make([]string, 0, len(group.Parameters))
		for _, parameter := range group.Parameters {
			source := config.Sources[parameter.Source]
			if source == "" {
				continue
			}
			values = append(values, strings.NewReplacer(
				"{label}", parameter.Label, "{value}", parameter.Value, "{source}", source,
			).Replace(config.ParameterTemplate))
		}
		if len(values) == 0 {
			continue
		}
		lines = append(lines, strings.NewReplacer(
			"{model}", group.Model, "{capability}", group.Capability,
			"{parameters}", strings.Join(values, config.Separator),
		).Replace(config.OfferingTemplate))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.ReplaceAll(config.ContextTemplate, "{parameters}", strings.Join(lines, "\n"))
}
