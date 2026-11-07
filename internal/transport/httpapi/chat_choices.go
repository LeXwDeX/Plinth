package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The in-chat choice cards of a session (#321). The wire carries the card's
// public facts only: Offering IDs, public names and Schema quotes, the
// default and the answer. The next-call arguments stay on the server.

type choiceOptionWire struct {
	ID           string `json:"id"`
	ProviderName string `json:"provider_name,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	// Label and Description are the Schema words of a generic card (#103);
	// Recommended marks the option the Agent suggests. A provider-choice card
	// (#321) leaves them empty.
	Label            string         `json:"label,omitempty"`
	Description      string         `json:"description,omitempty"`
	Recommended      bool           `json:"recommended,omitempty"`
	CreditsMetered   bool           `json:"credits_metered,omitempty"`
	EstimatedCredits int            `json:"estimated_credits,omitempty"`
	NativeQuote      map[string]any `json:"native_quote,omitempty"`
}

type choiceAnswerWire struct {
	OptionID   string `json:"option_id"`
	UseDefault bool   `json:"use_default"`
	Source     string `json:"source"`
	AnsweredAt string `json:"answered_at"`
}

type choiceWire struct {
	ID                 string `json:"id"`
	AssistantMessageID string `json:"assistant_message_id"`
	Tool               string `json:"tool"`
	Kind               string `json:"kind,omitempty"`
	// Question is the Schema question of a generic card (#103); a
	// provider-choice card has none.
	Question        string             `json:"question,omitempty"`
	Capability      string             `json:"capability"`
	Options         []choiceOptionWire `json:"options"`
	DefaultOptionID string             `json:"default_option_id"`
	AllowDefault    bool               `json:"allow_default"`
	State           string             `json:"state"`
	Answer          *choiceAnswerWire  `json:"answer,omitempty"`
	CreatedAt       string             `json:"created_at"`
}

func choiceToWire(choice chat.Choice, question string) choiceWire {
	wire := choiceWire{ID: choice.ID, AssistantMessageID: choice.AssistantMessageID, Tool: choice.ToolName,
		Kind: choice.Kind, Question: question, Capability: choice.Capability, DefaultOptionID: choice.DefaultOptionID, AllowDefault: choice.AllowDefault,
		State: string(choice.State), CreatedAt: choice.CreatedAt.UTC().Format(time.RFC3339), Options: []choiceOptionWire{}}
	for _, option := range choice.Options {
		view := choiceOptionWire{ID: option.ID, ProviderName: option.ProviderName, ModelName: option.ModelName,
			Label: option.Label, Description: option.Description, Recommended: option.Recommended,
			CreditsMetered: option.CreditsMetered, EstimatedCredits: option.EstimatedCredits}
		if quote := option.NativeQuote; quote != nil {
			encoded, _ := json.Marshal(quote)
			_ = json.Unmarshal(encoded, &view.NativeQuote)
		}
		wire.Options = append(wire.Options, view)
	}
	if choice.State == chat.ChoiceAnswered {
		wire.Answer = &choiceAnswerWire{OptionID: choice.AnswerOptionID, UseDefault: choice.AnswerDefault,
			Source: choice.AnswerSource, AnsweredAt: choice.AnsweredAt.UTC().Format(time.RFC3339)}
	}
	return wire
}

// choiceKindQuestion is the Schema-declared default question of a generic
// card's kind. The Agent's per-card question is not persisted with the card,
// so the card shows this declared one.
func (s *Server) choiceKindQuestion(kind string) string {
	for _, declared := range s.application.Chat.Choice.Kinds {
		if declared.ID == kind {
			return declared.Question
		}
	}
	return ""
}

// listChatSessionChoices returns the caller's own cards of the session,
// newest first. A foreign or missing session is the same 404.
func (s *Server) listChatSessionChoices(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	choices, err := s.chat.ListChoices(request.Context(), string(owner), request.PathValue("sessionID"))
	if errors.Is(err, chat.ErrSessionNotFound) {
		noteCause(writer, err)
		s.writeChatError(writer, http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound)
		return
	}
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	wires := make([]choiceWire, 0, len(choices))
	for _, choice := range choices {
		question := ""
		if choice.Kind != "" {
			question = s.choiceKindQuestion(choice.Kind)
		}
		wires = append(wires, choiceToWire(choice, question))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{"choices": wires})
}

// chatChoiceAnswer reads the answer a turn carries, or nil when it carries none.
func chatChoiceAnswer(body map[string]json.RawMessage, field string) (*chat.ChoiceAnswer, bool) {
	raw, present := body[field]
	if field == "" || !present || string(raw) == "null" {
		return nil, true
	}
	var wire struct {
		ChoiceID   string `json:"choice_id"`
		OptionID   string `json:"option_id"`
		UseDefault bool   `json:"use_default"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.ChoiceID == "" || (wire.OptionID == "") == !wire.UseDefault {
		return nil, false
	}
	return &chat.ChoiceAnswer{ChoiceID: wire.ChoiceID, OptionID: wire.OptionID, UseDefault: wire.UseDefault}, true
}

// writeChoiceAnswerError maps a refused answer; ok is false for other errors.
func (s *Server) writeChoiceAnswerError(writer http.ResponseWriter, err error) bool {
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrChoiceClosed):
		noteCause(writer, err)
		s.writeChatError(writer, http.StatusConflict, messages.ChoiceAnswered)
	case errors.Is(err, chat.ErrChoiceRejected), errors.Is(err, chat.ErrChoiceNotFound):
		noteCause(writer, err)
		s.writeChatError(writer, http.StatusBadRequest, messages.ChoiceRejected)
	default:
		return false
	}
	return true
}
