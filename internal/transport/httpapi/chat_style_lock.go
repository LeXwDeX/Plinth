package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) chatStyleLockWire(sessionID string, lock chat.StyleLock) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	names := s.application.Chat.API.StyleLock
	wire := map[string]any{
		fields.ID: sessionID,
		fields.StyleLock: map[string]any{
			names.Enabled:    lock.Enabled,
			names.Text:       lock.Text,
			names.References: append([]string(nil), lock.References...),
		},
	}
	if names.Source != "" {
		wire[fields.StyleLock].(map[string]any)[names.Source] = lock.Source
	}
	return wire
}

func (s *Server) writeChatStyleLockError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrStyleLockTextTooLong):
		var tooLong *chat.StyleLockTextTooLongError
		if errors.As(err, &tooLong) {
			s.writeChatError(writer, http.StatusBadRequest, renderStyleLockLimitMessage(messages.StyleLockTextTooLong, tooLong.Length, tooLong.Maximum))
			return
		}
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	case errors.Is(err, chat.ErrStyleLockReferenceLimit):
		var limit *chat.StyleLockReferenceLimitError
		if errors.As(err, &limit) {
			s.writeChatError(writer, http.StatusBadRequest, renderStyleLockLimitMessage(messages.StyleLockReferenceLimit, limit.Count, limit.Maximum))
			return
		}
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	case errors.Is(err, chat.ErrStyleLockReferenceInvalid):
		s.writeChatError(writer, http.StatusBadRequest, messages.StyleLockReferenceInvalid)
	case errors.Is(err, chat.ErrStyleLockSourceInvalid):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

func renderStyleLockLimitMessage(template string, length, maximum int) string {
	return strings.NewReplacer(
		"{length}", strconv.Itoa(length),
		"{maximum}", strconv.Itoa(maximum),
	).Replace(template)
}

func (s *Server) getChatSessionStyleLock(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	lock, err := s.chat.StyleLock(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatStyleLockError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatStyleLockWire(sessionID, lock))
}

func (s *Server) putChatSessionStyleLock(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	names := s.application.Chat.API.StyleLock
	for name := range body {
		if name != names.Enabled && name != names.Text && name != names.References && name != names.Source {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
	}
	var enabled bool
	var text string
	var references []string
	var source string
	if raw, ok := body[names.Enabled]; !ok || string(raw) == "null" || json.Unmarshal(raw, &enabled) != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, ok := body[names.Text]; !ok || string(raw) == "null" || json.Unmarshal(raw, &text) != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, ok := body[names.References]; !ok || string(raw) == "null" || json.Unmarshal(raw, &references) != nil || references == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if names.Source != "" {
		if raw, ok := body[names.Source]; ok && (string(raw) == "null" || json.Unmarshal(raw, &source) != nil) {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
	}
	lock, err := s.chat.SetStyleLockWithSource(request.Context(), string(owner), request.PathValue("sessionID"), source, enabled, text, references)
	if err != nil {
		s.writeChatStyleLockError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatStyleLockWire(request.PathValue("sessionID"), lock))
}
