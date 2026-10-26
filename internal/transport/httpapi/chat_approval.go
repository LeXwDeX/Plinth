package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chatApprovalWire is the shared body of the approval-mode read and write.
// Field names come from the Product Schema, like the other session DTOs.
func (s *Server) chatApprovalWire(session chat.Session) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	return map[string]any{
		fields.ID:               session.ID,
		fields.ApprovalMode:     s.chat.NormalizeApprovalMode(session.ApprovalMode),
		fields.EstimatedCredits: session.EstimatedCredits,
	}
}

func (s *Server) writeChatApprovalError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrInvalidApprovalMode):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionApproval returns the caller's own session mode and estimated
// credits. A foreign or missing session is the same 404.
func (s *Server) getChatSessionApproval(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	session, found, err := s.chat.Session(request.Context(), string(owner), request.PathValue("sessionID"))
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	if !found {
		s.writeChatApprovalError(writer, chat.ErrSessionNotFound)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatApprovalWire(session))
}

// putChatSessionApproval switches the session approval mode. Auto mode lets
// paid steps run without a person, so it needs the same real browser session
// as a payment decision: a Bearer token or development credential cannot
// raise its own approval level. The change is audited.
func (s *Server) putChatSessionApproval(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := generation.OwnerID(principal.ID())
	if _, human := s.taskHumanPrincipal(request, owner); !human {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatApprovalError(writer, chat.ErrInvalidApprovalMode)
		return
	}
	mode := chatString(body, s.application.Chat.API.Fields.ApprovalMode)
	sessionID := request.PathValue("sessionID")
	session, previous, err := s.chat.SetApprovalMode(request.Context(), string(owner), sessionID, mode)
	if err != nil {
		s.writeChatApprovalError(writer, err)
		return
	}
	if previous != session.ApprovalMode {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       s.application.Chat.Audit.ApprovalModeAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: s.application.Chat.Audit.SessionResourceType,
			ResourceID:   session.ID,
			Detail:       map[string]any{"from": previous, "to": session.ApprovalMode},
		})
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatApprovalWire(session))
}
