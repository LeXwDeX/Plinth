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

// chatAgentScopeWire is the shared body of the Agent-scope read and write.
// Member names come from the Product Schema, like the other session DTOs.
func (s *Server) chatAgentScopeWire(session chat.Session) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	return map[string]any{
		fields.ID:         session.ID,
		fields.AgentScope: s.chat.NormalizeAgentScope(session.AgentScope),
	}
}

func (s *Server) writeChatAgentScopeError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrInvalidAgentScope):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionAgentScope returns the caller's own session Agent scope. A
// foreign or missing session is the same 404.
func (s *Server) getChatSessionAgentScope(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	session, found, err := s.chat.Session(request.Context(), string(owner), request.PathValue("sessionID"))
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	if !found {
		s.writeChatAgentScopeError(writer, chat.ErrSessionNotFound)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatAgentScopeWire(session))
}

// putChatSessionAgentScope switches the session Agent scope. The scope is a
// limit the user puts on the Agent, applied by the server to the turns that
// start afterwards; it approves nothing and never reaches a running turn. A
// real change is audited.
func (s *Server) putChatSessionAgentScope(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := generation.OwnerID(principal.ID())
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatAgentScopeError(writer, chat.ErrInvalidAgentScope)
		return
	}
	scope := chatString(body, s.application.Chat.API.Fields.AgentScope)
	session, previous, err := s.chat.SetAgentScope(request.Context(), string(owner), request.PathValue("sessionID"), scope)
	if err != nil {
		s.writeChatAgentScopeError(writer, err)
		return
	}
	if current := s.chat.NormalizeAgentScope(session.AgentScope); previous != current {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       s.application.Chat.Audit.AgentScopeAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: s.application.Chat.Audit.SessionResourceType,
			ResourceID:   session.ID,
			Detail:       map[string]any{"from": previous, "to": current},
		})
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatAgentScopeWire(session))
}
