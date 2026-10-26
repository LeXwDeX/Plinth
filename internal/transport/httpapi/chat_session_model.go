package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The session's chat model (#322 Scope 5). Owner scoped like the other
// session settings: a foreign or missing session is the same 404. The models
// listed are the ones the administrator offers now; each carries its ID and
// upstream model name, never a connection value.

func (s *Server) chatModelWire(sessionID string, view chat.ChatModelView) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	options := make([]map[string]any, 0, len(view.Options))
	for _, option := range view.Options {
		options = append(options, map[string]any{fields.ID: option.ID, fields.ChatModelName: option.Model})
	}
	return map[string]any{
		fields.ID:                sessionID,
		fields.ChatModel:         view.Selected,
		fields.ChatModelChosen:   view.Chosen,
		fields.ChatModelDefault:  view.Default,
		fields.ChatModelFallback: view.Fallback,
		fields.ChatModels:        options,
	}
}

func (s *Server) writeChatModelError(writer http.ResponseWriter, err error) {
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrInvalidChatModel):
		noteCause(writer, err)
		s.writeChatError(writer, http.StatusBadRequest, messages.ChatModelInvalid)
	case errors.Is(err, chat.ErrChatModelUnavailable):
		noteCause(writer, err)
		s.writeChatError(writer, http.StatusConflict, messages.ChatModelUnavailable)
	default:
		s.writeChatContextError(writer, err)
	}
}

func (s *Server) getChatSessionModel(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	view, err := s.chat.SessionChatModel(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatModelError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatModelWire(sessionID, view))
}

// putChatSessionModel stores the owner's model for the session: one the
// administrator offers now, or "" to follow the default. It applies to the
// turns that start afterwards.
func (s *Server) putChatSessionModel(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	sessionID := request.PathValue("sessionID")
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatModelError(writer, errors.Join(chat.ErrInvalidChatModel, err))
		return
	}
	var model string
	raw, present := body[s.application.Chat.API.Fields.ChatModel]
	if !present || json.Unmarshal(raw, &model) != nil {
		s.writeChatModelError(writer, chat.ErrInvalidChatModel)
		return
	}
	view, err := s.chat.SetSessionChatModel(request.Context(), principal.ID(), sessionID, model)
	if err != nil {
		s.writeChatModelError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatModelWire(sessionID, view))
}
