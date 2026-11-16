package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The values the session's production brief gives the parameters an Offering
// declares as following the purpose (presentation.follows_purpose, #292), read by
// the 加工 panel before a step is confirmed (#306). The resolution is the one the
// plan creation fills the step with (chat.PurposeParameters), so the panel shows
// what the plan will freeze, and a parameter the purpose gives nothing shows the
// Offering's default. The read is owner scoped like the brief: a foreign session
// is the same 404 as a missing one.

type chatPurposeParametersWire struct {
	SessionID  string `json:"session_id"`
	OfferingID string `json:"offering_id"`
	// Parameters maps each parameter of the Offering that follows the purpose to
	// the value it takes; empty for an Offering with no such parameter.
	Parameters map[string]json.RawMessage `json:"parameters"`
}

// getChatSessionPurposeParameters answers for the one Offering the query names.
// An Offering the Schema does not declare, or none, is a bad request before the
// session's brief is read.
func (s *Server) getChatSessionPurposeParameters(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	messages := s.application.Chat.Presentation.Messages
	sessionID := request.PathValue("sessionID")
	offering, found := s.generation.Offering(generation.OfferingID(strings.TrimSpace(request.URL.Query().Get("offering_id"))))
	if !found {
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
		return
	}
	values, err := s.chat.PurposeFollowedValues(request.Context(), string(owner), sessionID, offering)
	if err != nil {
		noteCause(writer, err)
		if errors.Is(err, chat.ErrSessionNotFound) {
			s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
			return
		}
		s.writeChatInternalError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, chatPurposeParametersWire{
		SessionID: sessionID, OfferingID: string(offering.ID), Parameters: values,
	})
}
