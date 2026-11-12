package generation

import "encoding/json"

// sourceInputRefusal applies a task parameter's source_input_rules (#294) to the
// Task that would be its source; request is the step's own input. A rule that
// matches refuses the dependency with the rule's Schema message as a UserFacing
// refusal, so it is said before anything is submitted or paid, in the
// requester's language. A source whose frozen input cannot be read is not
// blocked: the upstream stays the authority for it.
//
// The rule sees only the frozen input of the source Task itself. A source made
// by a step that does not carry the condition's parameter (a retexture of a
// quad-topology model has no topology of its own) is not recognised; the
// upstream refuses it when the step runs.
func sourceInputRefusal(parameter Parameter, source Task, request map[string]any) error {
	if len(parameter.SourceInputRules) == 0 {
		return nil
	}
	var frozen map[string]any
	if json.Unmarshal(source.Input, &frozen) != nil {
		return nil
	}
	for _, rule := range parameter.SourceInputRules {
		if rule.Refuses(frozen, request) {
			return &Error{
				Code: ErrInvalidInput, Operation: "resolve_task_dependencies",
				Detail: rule.Message, UserFacing: true, DetailTemplate: rule.Message,
			}
		}
	}
	return nil
}
