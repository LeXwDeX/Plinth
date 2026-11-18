package chat

import (
	"context"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The concept quantity card (#360) asks how many low-cost sketches to draw
// before anything is submitted. It is an ordinary choice card, so the answer
// is persisted with the session: State, Kind and AnswerOptionID are enough to
// recover the chosen count server side, and AnswerMessageID binds the answer
// to the user message that started the next reply.

// ConceptQuantityCount reads the chosen count of an answered concept quantity
// card. Only an option id inside the declared bounds counts.
func ConceptQuantityCount(choice Choice, quantity catalog.ChatConceptQuantitySchema) (int, bool) {
	if choice.State != ChoiceAnswered {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(choice.AnswerOptionID))
	if err != nil || count < quantity.MinCount || count > quantity.MaxCount {
		return 0, false
	}
	return count, true
}

// SelectedConceptQuantity reports the count the person chose on the quantity
// card that answered the user message starting this reply. A stale card, a
// card of another kind, or a typed answer the Agent honored without the card
// reports nothing, so the caller keeps its default behavior.
func SelectedConceptQuantity(
	ctx context.Context,
	store ChoiceStore,
	concept catalog.ChatConceptDirectionsSchema,
	userRole string,
	limit int,
	ownerID, sessionID string,
	history []Message,
) (int, bool) {
	quantity := concept.Quantity
	if !concept.Enabled || store == nil || strings.TrimSpace(quantity.Kind) == "" {
		return 0, false
	}
	if limit <= 0 {
		limit = 1
	}
	currentUser := ""
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == userRole {
			currentUser = history[index].ID
			break
		}
	}
	if currentUser == "" {
		return 0, false
	}
	choices, err := store.ListChoices(ctx, ownerID, sessionID, limit)
	if err != nil {
		return 0, false
	}
	for _, choice := range choices {
		if choice.Kind != quantity.Kind || choice.AnswerSource != ChoiceAnsweredOnCard ||
			choice.AnswerMessageID != currentUser {
			continue
		}
		if count, ok := ConceptQuantityCount(choice, quantity); ok {
			return count, true
		}
	}
	return 0, false
}

// ConceptQuantityPending reports whether this very reply opened the quantity
// card and the person has not answered it yet. A sketch in that reply would be
// a paid generation nobody asked for.
func ConceptQuantityPending(choices []Choice, quantity catalog.ChatConceptQuantitySchema, assistantMessageID string) bool {
	if strings.TrimSpace(quantity.Kind) == "" || assistantMessageID == "" {
		return false
	}
	for _, choice := range choices {
		if choice.State == ChoicePending && choice.Kind == quantity.Kind &&
			choice.AssistantMessageID == assistantMessageID {
			return true
		}
	}
	return false
}

// selectedConceptQuantity is the service's bound lookup over its own choice
// store and role names (#360).
func (service *Service) selectedConceptQuantity(ctx context.Context, command ToolCommand) (int, bool) {
	return SelectedConceptQuantity(ctx, service.choices, service.conceptDirections, service.userRole,
		service.choiceLimit, command.OwnerID, command.SessionID, command.History)
}

// conceptQuantityAnswerText follows the user message that answered the
// quantity card: the Agent reads the chosen count and how many sketches to
// draw, instead of a tool call continuation.
func (orchestrator *Orchestrator) conceptQuantityAnswerText(choice Choice, messageID string) (string, bool) {
	concept := orchestrator.schema.ConceptDirections
	quantity := concept.Quantity
	if !concept.Enabled || choice.Kind != quantity.Kind ||
		choice.AnswerSource != ChoiceAnsweredOnCard || choice.AnswerMessageID != messageID {
		return "", false
	}
	count, ok := ConceptQuantityCount(choice, quantity)
	if !ok {
		return "", false
	}
	return strings.ReplaceAll(quantity.AnswerTemplate, "{count}", strconv.Itoa(count)), true
}

// conceptQuantityPendingPrompt tells the Agent about the open quantity card
// with its counts, so a typed reply maps to one exact number.
func (orchestrator *Orchestrator) conceptQuantityPendingPrompt(choices []Choice) (string, bool) {
	concept := orchestrator.schema.ConceptDirections
	quantity := concept.Quantity
	if !concept.Enabled || strings.TrimSpace(quantity.PendingTemplate) == "" {
		return "", false
	}
	for _, choice := range choices {
		if choice.State != ChoicePending || choice.Kind != quantity.Kind {
			continue
		}
		continuation, ok := choice.PendingContinuation()
		if !ok {
			continue
		}
		return strings.ReplaceAll(quantity.PendingTemplate, "{continuation}", continuation), true
	}
	return "", false
}
