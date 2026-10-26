package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chatPickWire is the shared body of the pick read and write. The picked
// object uses the attachment member names of the Schema and carries platform
// IDs and its handle only; a session without a pick returns a null member.
func (s *Server) chatPickWire(pick chat.Attachment, picked bool) map[string]any {
	member := s.application.Chat.API.ResponseFields.Pick
	if !picked {
		return map[string]any{member: nil}
	}
	return map[string]any{member: s.chatAttachmentWire([]chat.Attachment{pick})[0]}
}

func (s *Server) writeChatPickError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrPickRejected):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionPick returns the caller's own session pick. A foreign or
// missing session is the same 404.
func (s *Server) getChatSessionPick(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	pick, picked, err := s.chat.SessionPick(request.Context(), string(owner), request.PathValue("sessionID"))
	if err != nil {
		s.writeChatPickError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatPickWire(pick, picked))
}

// putChatSessionPick makes the claimed candidate the session's current
// object. The claim is verified for the owner and session; anything that is
// not a generated picture of the session is refused without echoing IDs.
func (s *Server) putChatSessionPick(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var claim map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&claim); err != nil || claim == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	pick, err := s.chat.PickCandidate(request.Context(), string(owner), request.PathValue("sessionID"), s.chatAttachmentRef(claim))
	if err != nil {
		s.writeChatPickError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatPickWire(pick, true))
}

// deleteChatSessionPick forgets the caller's own session pick.
func (s *Server) deleteChatSessionPick(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	if err := s.chat.ClearSessionPick(request.Context(), string(owner), request.PathValue("sessionID")); err != nil {
		s.writeChatPickError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatPickWire(chat.Attachment{}, false))
}
