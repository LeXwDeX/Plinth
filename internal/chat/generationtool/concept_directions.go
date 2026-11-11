package generationtool

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// conceptSketchLimit refuses a concept sketch beyond the Schema's per-reply
// bound (#242). EarlierCalls is the reply's own call list, so a resumed reply
// keeps counting where it left off; a new reply starts from zero.
func (executor *Executor) conceptSketchLimit(command chat.ToolCommand) string {
	concept := executor.chat.ConceptDirections
	if !concept.Enabled || command.Tool.Name != concept.SketchTool || concept.MaxCount <= 0 {
		return ""
	}
	used := 0
	for _, call := range command.EarlierCalls {
		if call.Name == concept.SketchTool {
			used++
		}
	}
	if used < concept.MaxCount {
		return ""
	}
	if strings.TrimSpace(concept.LimitReached) == "" {
		return "concept sketch limit reached"
	}
	return strings.ReplaceAll(concept.LimitReached, "{maximum}", strconv.Itoa(concept.MaxCount))
}

// conceptSketchParameterValues returns the cheap parameter values the Schema
// declares for this sketch's Offering (#242). Only parameters the Offering
// exposes to the Agent are applied; an Offering the table does not name, or a
// parameter it does not declare, is used as is.
func (executor *Executor) conceptSketchParameterValues(command chat.ToolCommand, offering generation.Offering) map[string]json.RawMessage {
	concept := executor.chat.ConceptDirections
	if !concept.Enabled || command.Tool.Name != concept.SketchTool {
		return nil
	}
	overrides := concept.ParameterOverrides[string(offering.ID)]
	if len(overrides) == 0 {
		return nil
	}
	values := make(map[string]json.RawMessage, len(overrides))
	for name, value := range overrides {
		for _, parameter := range offering.Parameters {
			if parameter.Name != name {
				continue
			}
			if offering.AgentParameterAllowed(parameter) {
				values[name] = value
			}
			break
		}
	}
	return values
}
