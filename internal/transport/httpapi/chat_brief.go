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

// The session production brief and the explicit 3D model pick (#103). The
// brief is a whole replacement of the Schema-declared fields; the explicit
// model is only a lock the generation tools honor. Both reads are owner
// scoped, and a foreign session is the same 404 as a missing one.

func productionBriefWire(brief chat.ProductionBrief) map[string]json.RawMessage {
	if len(brief) == 0 {
		return map[string]json.RawMessage{}
	}
	return map[string]json.RawMessage(brief)
}

func (s *Server) chatBriefWire(sessionID string, brief chat.ProductionBrief) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	return map[string]any{
		fields.ID:    sessionID,
		fields.Brief: productionBriefWire(brief),
	}
}

func (s *Server) writeChatBriefError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	var brief *chat.BriefError
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.As(err, &brief):
		s.writeChatError(writer, http.StatusBadRequest,
			chat.ProductionBriefValidationErrorText(s.application.Chat.Model3DUsage.Brief.InvalidTemplate, err))
	case errors.Is(err, chat.ErrProductionBriefUnavailable):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionBrief returns the caller's own session production brief.
func (s *Server) getChatSessionBrief(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	brief, err := s.chat.SessionBrief(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatBriefError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatBriefWire(sessionID, brief))
}

// putChatSessionBrief replaces the caller's own production brief. The body
// carries one member, named by the Schema, whose value is the whole brief
// object; a missing or null member clears it. A real change is audited by the
// names of the fields that changed, never their text.
func (s *Server) putChatSessionBrief(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := generation.OwnerID(principal.ID())
	sessionID := request.PathValue("sessionID")
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatBriefError(writer, chat.ErrProductionBriefUnavailable)
		return
	}
	raw := body[s.application.Chat.API.Fields.Brief]
	next := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &next); err != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
	}
	previous, err := s.chat.SessionBrief(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatBriefError(writer, err)
		return
	}
	stored, err := s.chat.SetProductionBrief(request.Context(), string(owner), sessionID, next)
	if err != nil {
		s.writeChatBriefError(writer, err)
		return
	}
	if changed := chat.ChangedProductionBriefFields(previous, stored); len(changed) > 0 {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       s.application.Chat.Audit.ProductionBriefAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: s.application.Chat.Audit.SessionResourceType,
			ResourceID:   sessionID,
			Detail:       map[string]any{"fields": changed},
		})
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatBriefWire(sessionID, stored))
}

func (s *Server) chatExplicitModel3DWire(sessionID, model string) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	return map[string]any{
		fields.ID:              sessionID,
		fields.ExplicitModel3D: model,
	}
}

func (s *Server) writeChatExplicitModel3DError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrInvalidExplicitModel3D):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionExplicitModel3D returns the model the person explicitly
// picked in the session, or the empty string when there is none.
func (s *Server) getChatSessionExplicitModel3D(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	model, err := s.chat.SessionExplicitModel3D(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatExplicitModel3DError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatExplicitModel3DWire(sessionID, model))
}

// putChatSessionExplicitModel3D records or clears the person's explicit 3D
// model pick. An empty model clears the lock; nothing is submitted.
func (s *Server) putChatSessionExplicitModel3D(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var model string
	if raw, present := body[s.application.Chat.API.Fields.ExplicitModel3D]; present && string(raw) != "null" {
		if err := json.Unmarshal(raw, &model); err != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
	}
	stored, err := s.chat.SetExplicitModel3D(request.Context(), string(owner), sessionID, model)
	if err != nil {
		s.writeChatExplicitModel3DError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatExplicitModel3DWire(sessionID, stored))
}
