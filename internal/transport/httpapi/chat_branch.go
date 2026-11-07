package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func (s *Server) branchChatSession(w http.ResponseWriter, r *http.Request, principal access.Principal) {
	var body struct {
		MessageID string `json:"messageId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.MessageID) == "" {
		s.writeChatError(w, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		s.writeChatError(w, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	session, err := s.chat.BranchSession(r.Context(), principal.ID(), r.PathValue("sessionID"), body.MessageID)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound), errors.Is(err, chat.ErrMessageNotFound):
		s.writeChatError(w, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrTurnActive):
		s.writeChatError(w, http.StatusConflict, messages.BranchTurnActive)
	case errors.Is(err, chat.ErrBranchPendingApproval):
		s.writeChatError(w, http.StatusConflict, messages.BranchPendingApproval)
	case errors.Is(err, chat.ErrBranchScene):
		s.writeChatError(w, http.StatusConflict, messages.BranchScene)
	case err != nil:
		s.writeChatInternalError(w, err)
	default:
		w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
		writeJSON(w, http.StatusCreated, map[string]any{
			"session": s.chatSessionWire(r.Context(), w, session, true),
			s.application.Chat.API.ResponseFields.BranchCanvasCopied:   session.BranchCanvasCopied,
			s.application.Chat.API.ResponseFields.BranchSceneCopied:    session.BranchSceneCopied,
			s.application.Chat.API.ResponseFields.BranchCapturesCopied: session.BranchCapturesCopied,
		})
	}
}
