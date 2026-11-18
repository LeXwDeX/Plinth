package generationtool

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// conceptSketchLimit refuses a concept sketch beyond the count in force
// (#242, #360): the count the person chose on the quantity card of this reply,
// or the Schema bound when no choice was recorded. EarlierCalls is the reply's
// own call list, so a resumed reply keeps counting where it left off; a new
// reply starts from zero. A sketch in the reply that only opened the quantity
// card is refused too: the person has not chosen anything yet.
func (executor *Executor) conceptSketchLimit(ctx context.Context, command chat.ToolCommand) string {
	concept := executor.chat.ConceptDirections
	if !concept.Enabled || command.Tool.Name != concept.SketchTool || concept.MaxCount <= 0 {
		return ""
	}
	limit := concept.MaxCount
	quantity := concept.Quantity
	if executor.choices != nil && strings.TrimSpace(quantity.Kind) != "" {
		pageSize := executor.chat.Choice.ListLimit
		if pageSize <= 0 {
			pageSize = 1
		}
		if choices, err := executor.choices.ListChoices(ctx, command.OwnerID, command.SessionID, pageSize); err == nil {
			if chat.ConceptQuantityPending(choices, quantity, command.AssistantMessageID) {
				return quantity.SketchRefusal
			}
		}
		if chosen, ok := chat.SelectedConceptQuantity(ctx, executor.choices, concept, executor.chat.Roles.User,
			pageSize, command.OwnerID, command.SessionID, command.History); ok {
			limit = chosen
		}
	}
	used := 0
	for _, call := range command.EarlierCalls {
		if call.Name == concept.SketchTool {
			used++
		}
	}
	if used < limit {
		return ""
	}
	if strings.TrimSpace(concept.LimitReached) == "" {
		return "concept sketch limit reached"
	}
	return strings.ReplaceAll(concept.LimitReached, "{maximum}", strconv.Itoa(limit))
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
