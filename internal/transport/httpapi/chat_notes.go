package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chatNotesWire is the shared body of the settings-summary read and write.
// Member names come from the Product Schema, like the other session DTOs.
func (s *Server) chatNotesWire(sessionID, notes string) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	return map[string]any{
		fields.ID:    sessionID,
		fields.Notes: notes,
	}
}

func (s *Server) writeChatNotesError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrSessionNotesTooLong):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionNotes returns the caller's own session settings summary. A
// foreign or missing session is the same 404.
func (s *Server) getChatSessionNotes(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	notes, err := s.chat.SessionNotes(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatNotesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatNotesWire(sessionID, notes))
}

// putChatSessionNotes replaces the caller's own session settings summary with
// the text the user edited. It obeys the same Schema bound as the Agent tool.
func (s *Server) putChatSessionNotes(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var notes string
	if err := json.Unmarshal(body[s.application.Chat.API.Fields.Notes], &notes); err != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	sessionID := request.PathValue("sessionID")
	stored, err := s.chat.SetSessionNotes(request.Context(), string(owner), sessionID, notes)
	if err != nil {
		s.writeChatNotesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatNotesWire(sessionID, stored))
}
