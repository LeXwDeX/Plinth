package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Session thinking level, context usage and context compaction (#322). The
// member names come from the Product Schema like the other session DTOs.
// Every route is owner scoped: a foreign or missing session is the same 404.

// ChatContextCompactor compacts a session's context at the person's request;
// the chat orchestrator implements it. Without one the route is unavailable.
type ChatContextCompactor interface {
	CompactContext(ctx context.Context, ownerID, sessionID string) (chat.ContextView, error)
}

func (s *Server) chatReasoningWire(sessionID string, view chat.ReasoningView) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	levels := view.Levels
	if levels == nil {
		levels = []string{}
	}
	return map[string]any{
		fields.ID:               sessionID,
		fields.ReasoningEffort:  view.Effort,
		fields.ReasoningLevels:  levels,
		fields.ReasoningDefault: view.Default,
	}
}

func (s *Server) chatContextWire(sessionID string, view chat.ContextView) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	compactions := make([]map[string]any, 0, len(view.Compactions))
	for _, compaction := range view.Compactions {
		compactions = append(compactions, map[string]any{
			fields.ID:                compaction.ID,
			fields.CreatedAt:         compaction.CreatedAt.UTC().Format(time.RFC3339Nano),
			fields.CompactionTrigger: compaction.Trigger,
			fields.CompactedRounds:   compaction.Rounds,
			fields.ThroughMessageID:  compaction.ThroughMessageID,
		})
	}
	var tokens, window any
	if view.Tokens != nil {
		tokens = *view.Tokens
	}
	if view.Window > 0 {
		window = view.Window
	}
	return map[string]any{
		fields.ID:            sessionID,
		fields.ContextTokens: tokens,
		fields.ContextWindow: window,
		fields.Compactions:   compactions,
	}
}

func (s *Server) writeChatContextError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrInvalidReasoningEffort):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	case errors.Is(err, chat.ErrReasoningUnavailable):
		s.writeChatError(writer, http.StatusConflict, messages.ReasoningUnavailable)
	case errors.Is(err, chat.ErrCompactionUnavailable):
		s.writeChatError(writer, http.StatusConflict, messages.CompactionUnavailable)
	case errors.Is(err, chat.ErrCompactionTurnActive), errors.Is(err, chat.ErrCompactionConflict):
		s.writeChatError(writer, http.StatusConflict, messages.CompactionTurnActive)
	case errors.Is(err, chat.ErrNothingToCompact):
		text := traceFromWriter(writer).localeText(messages.CompactionNothing)
		rounds := strconv.Itoa(s.application.Chat.ContextCompaction.KeepRecentRounds)
		s.writeChatError(writer, http.StatusUnprocessableEntity, strings.ReplaceAll(text, "{rounds}", rounds))
	case errors.Is(err, chat.ErrCompactionFailed):
		s.writeChatError(writer, http.StatusBadGateway, messages.CompactionFailed)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionReasoning returns the thinking level the session's next turn
// sends and the levels its model offers.
func (s *Server) getChatSessionReasoning(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	view, err := s.chat.SessionReasoning(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatContextError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatReasoningWire(sessionID, view))
}

// putChatSessionReasoning stores the owner's level for the session. It must
// be one of the levels the session's model offers; it applies to the turns
// that start afterwards and approves nothing.
func (s *Server) putChatSessionReasoning(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := principal.ID()
	sessionID := request.PathValue("sessionID")
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeChatContextError(writer, errors.Join(chat.ErrInvalidReasoningEffort, err))
		return
	}
	level := chatString(body, s.application.Chat.API.Fields.ReasoningEffort)
	view, err := s.chat.SetSessionReasoning(request.Context(), owner, sessionID, level)
	if err != nil {
		s.writeChatContextError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatReasoningWire(sessionID, view))
}

// getChatSessionContext returns the session's context usage, its model's
// window and its compaction records.
func (s *Server) getChatSessionContext(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	view, err := s.chat.SessionContextView(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatContextError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatContextWire(sessionID, view))
}

// postChatSessionContextCompaction compacts the session's context now. It is
// refused while a turn of the session runs; a failed summary call leaves the
// session as it was.
func (s *Server) postChatSessionContextCompaction(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	sessionID := request.PathValue("sessionID")
	if s.chatContext == nil || !s.application.Chat.ContextCompaction.Enabled {
		s.writeChatContextError(writer, chat.ErrCompactionUnavailable)
		return
	}
	view, err := s.chatContext.CompactContext(request.Context(), principal.ID(), sessionID)
	if err != nil {
		s.writeChatContextError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatContextWire(sessionID, view))
}
