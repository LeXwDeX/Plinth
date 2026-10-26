package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) registerLegacyChatContracts(
	routes catalog.LegacyHTTPRoutesSchema,
) {
	s.mux.HandleFunc(
		"GET "+routes.ChatSessions,
		s.withLegacyPrincipal(s.legacyListChatSessions),
	)
	s.mux.HandleFunc(
		"POST "+routes.ChatSessions,
		s.withLegacyPrincipal(s.legacyCreateChatSession),
	)
	s.mux.HandleFunc(
		"GET "+routes.ChatSession,
		s.withLegacyPrincipal(s.legacyGetChatSession),
	)
	s.mux.HandleFunc(
		"PATCH "+routes.ChatSession,
		s.withLegacyPrincipal(s.legacyRenameChatSession),
	)
	s.mux.HandleFunc(
		"DELETE "+routes.ChatSession,
		s.withLegacyPrincipal(s.legacyDeleteChatSession),
	)
	s.mux.HandleFunc(
		"GET "+routes.ChatMessages,
		s.withLegacyPrincipal(s.legacyListChatMessages),
	)
	s.mux.HandleFunc(
		"POST "+routes.ChatMessages,
		s.withLegacyPrincipal(s.legacySendChatMessage),
	)
	s.mux.HandleFunc(
		"DELETE "+routes.ChatMessage,
		s.withLegacyPrincipal(s.legacyDeleteChatMessage),
	)
	s.mux.HandleFunc(
		"GET "+routes.ChatStream,
		s.withLegacyPrincipal(s.legacyChatStream),
	)
	s.mux.HandleFunc(
		"POST "+routes.ChatTurnCancel,
		s.withLegacyPrincipal(s.legacyCancelChatTurn),
	)
}

func (s *Server) withLegacyPrincipal(next principalHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			s.writeChatError(
				writer,
				http.StatusUnauthorized,
				s.application.Chat.Presentation.Messages.Unauthenticated,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (s *Server) legacyListChatSessions(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	values := request.URL.Query()
	if values.Has("limit") || values.Has("cursor") || values.Has("search") {
		listing := s.application.Chat.API.SessionList
		limit := listing.DefaultPageSize
		if raw := values.Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > listing.MaximumPageSize {
				s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
				return
			}
			limit = parsed
		}
		page, err := s.chat.ListSessionsPage(request.Context(), principal.ID(), chat.SessionListQuery{
			Search: strings.TrimSpace(values.Get("search")),
			Cursor: values.Get("cursor"),
			Limit:  limit,
		})
		if err != nil {
			if errors.Is(err, chat.ErrInvalidSessionPage) {
				s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			} else {
				s.writeChatInternalError(writer, err)
			}
			return
		}
		result := make([]map[string]any, 0, len(page.Sessions))
		for _, session := range page.Sessions {
			result = append(result, s.chatSessionWire(writer, session, true))
		}
		writeJSON(writer, http.StatusOK, map[string]any{"sessions": result, "next_cursor": page.NextCursor, "total": page.Total})
		return
	}
	sessions, err := s.chat.ListSessions(
		request.Context(),
		principal.ID(),
	)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	result := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, s.chatSessionWire(writer, session, true))
	}
	writeJSON(writer, http.StatusOK, result)
}

// legacyGetChatSession reads one session of the caller. A session that does not
// exist and one that belongs to someone else are both 404.
func (s *Server) legacyGetChatSession(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatSession,
		0,
	)
	session, found, err := s.chat.Session(request.Context(), principal.ID(), sessionID)
	switch {
	case err != nil:
		s.writeChatInternalError(writer, err)
	case !found:
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	default:
		wire := s.chatSessionWire(writer, session, true)
		// #318: the models last sent per domain, so a browser without local
		// picks restores them; the session read is owner-scoped above.
		models, retired, err := s.chat.SessionModels(request.Context(), principal.ID(), session.ID)
		if err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
		if field := s.application.Chat.API.ResponseFields.GenerationModels; field != "" && len(models) > 0 {
			wire[field] = models
			// #329 follow-up: a retired model is reported as its successor;
			// this member names the retired one so the workspace says so.
			if field := s.application.Chat.API.ResponseFields.GenerationModelsRetired; field != "" && len(retired) > 0 {
				wire[field] = retired
			}
		}
		writeJSON(writer, http.StatusOK, wire)
	}
}

func (s *Server) legacyCreateChatSession(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body := s.decodeOptionalChatObject(writer, request)
	title := chatString(body, s.application.Chat.API.Fields.Title)
	session, err := s.chat.CreateSession(
		request.Context(),
		principal.ID(),
		principal.Claims.Name,
		title,
	)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	s.logChatAudit(
		request,
		principal,
		s.application.Chat.Audit.CreateAction,
		s.application.Chat.Audit.SessionResourceType,
		session.ID,
	)
	writeJSON(writer, http.StatusCreated, s.chatSessionWire(writer, session, false))
}

func (s *Server) legacyRenameChatSession(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body, ok := s.decodeRequiredChatObject(writer, request)
	if !ok {
		return
	}
	title := chatString(body, s.application.Chat.API.Fields.Title)
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatSession,
		0,
	)
	session, err := s.chat.RenameSession(
		request.Context(),
		principal.ID(),
		sessionID,
		title,
	)
	switch {
	case errors.Is(err, chat.ErrEmptyTitle):
		s.writeChatError(
			writer,
			http.StatusBadRequest,
			s.application.Chat.Presentation.Messages.EmptyTitle,
		)
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		writeJSON(writer, http.StatusOK, s.chatSessionWire(writer, session, false))
	}
}

func (s *Server) legacyDeleteChatSession(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatSession,
		0,
	)
	err := s.chat.DeleteSession(request.Context(), principal.ID(), sessionID)
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		s.logChatAudit(
			request,
			principal,
			s.application.Chat.Audit.DeleteAction,
			s.application.Chat.Audit.SessionResourceType,
			sessionID,
		)
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) legacyListChatMessages(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatMessages,
		0,
	)
	values := request.URL.Query()
	if values.Has("limit") || values.Has("before") {
		s.listChatMessagePage(writer, request, principal, sessionID)
		return
	}
	messages, err := s.chat.ListMessages(
		request.Context(),
		principal.ID(),
		sessionID,
	)
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		result := make([]map[string]any, 0, len(messages))
		for _, message := range messages {
			result = append(result, s.chatMessageWire(requestLocale(request), message))
		}
		writeJSON(writer, http.StatusOK, result)
	}
}

// listChatMessagePage answers a paged read of one conversation: the newest
// messages before the "before" cursor, oldest first, with the cursor of the
// page before them.
func (s *Server) listChatMessagePage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
	sessionID string,
) {
	values := request.URL.Query()
	listing := s.application.Chat.API.MessageList
	limit := listing.DefaultPageSize
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > listing.MaximumPageSize {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		limit = parsed
	}
	page, err := s.chat.ListMessagesPage(request.Context(), principal.ID(), sessionID, chat.MessageListQuery{
		Before: values.Get("before"),
		Limit:  limit,
	})
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound)
	case errors.Is(err, chat.ErrInvalidMessagePage):
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		result := make([]map[string]any, 0, len(page.Messages))
		for _, message := range page.Messages {
			result = append(result, s.chatMessageWire(requestLocale(request), message))
		}
		writeJSON(writer, http.StatusOK, map[string]any{"messages": result, "next_cursor": page.NextCursor})
	}
}

func (s *Server) legacySendChatMessage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body, ok := s.decodeRequiredChatObject(writer, request)
	if !ok {
		return
	}
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatMessages,
		0,
	)
	requestFields := s.application.Chat.API.Fields
	skill := map[string]json.RawMessage{}
	_ = json.Unmarshal(body[requestFields.Skill], &skill)
	choiceAnswer, choiceOK := chatChoiceAnswer(body, requestFields.ChoiceAnswer)
	if !choiceOK {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.ChoiceRejected)
		return
	}
	parameters, ok := chatParameters(body, requestFields.Parameters)
	if !ok {
		s.writeChatError(
			writer,
			http.StatusBadRequest,
			s.application.Chat.Presentation.Messages.InvalidParameters,
		)
		return
	}
	options := chat.TurnOptions{
		Locale:         requestLocale(request),
		CreativeDomain: chatString(body, requestFields.CreativeDomain),
		AspectRatio:    chatString(body, requestFields.AspectRatio),
		PoseMode:       chatString(body, requestFields.PoseMode),
		AIModel:        chatString(body, requestFields.AIModel),
		Size:           chatString(body, requestFields.Size),
		ImageSize:      chatString(body, requestFields.ImageSize),
		Parameters:     parameters,
		SkillID:        chatString(skill, requestFields.SkillID),
		SkillName:      chatString(skill, requestFields.SkillName),
		ReferenceImageIndices: chatInts(
			body,
			requestFields.ReferenceImageIndices,
		),
		DomainModels: chatDomainModels(body, requestFields.DomainModels),
		InputSource:  chatString(body, requestFields.InputSource),
	}
	assistant, err := s.chat.StartTurn(request.Context(), chat.StartTurn{
		OwnerID:   principal.ID(),
		SessionID: sessionID,
		Content:   chatString(body, s.application.Chat.API.Fields.Content),
		ImageURLs: chatStrings(body, s.application.Chat.API.Fields.ImageURLs),
		Options:   options,
		// Claims only: the service verifies every one against the owner and
		// session before it can reach the Agent.
		Attachments: s.chatAttachmentRefs(body, requestFields.Attachments),
		Selection:   s.chatAttachmentRefs(body, requestFields.Selection),
		Mentions:    s.chatMentions(body, requestFields.Mentions),
		OnMentionsDropped: func(tokens []string) {
			s.logger.InfoContext(request.Context(), "Chat 提及未采用，保持原文", "session_id", sessionID, "count", len(tokens))
		},
		ChoiceAnswer: choiceAnswer,
	})
	if err != nil && s.writeChoiceAnswerError(writer, err) {
		return
	}
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	case errors.Is(err, chat.ErrTurnActive):
		s.writeChatError(writer, http.StatusConflict, s.application.Chat.Presentation.Messages.TurnActive)
	case errors.Is(err, chat.ErrEmptyMessage):
		s.writeChatError(
			writer,
			http.StatusBadRequest,
			s.application.Chat.Presentation.Messages.EmptyMessage,
		)
	case errors.Is(err, chat.ErrInvalidInputSource):
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidInputSource)
	case errors.Is(err, chat.ErrCanvasSourceMissing):
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.CanvasSourceMissing)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		scheduleErr := s.chatScheduler.Schedule(
			request.Context(),
			chat.ProcessTurn{
				OwnerID:            principal.ID(),
				SessionID:          sessionID,
				AssistantMessageID: assistant.ID,
				Options:            options,
			},
		)
		if scheduleErr != nil {
			s.logger.ErrorContext(
				request.Context(),
				"提交 Chat Agent 任务失败",
				"session_id", sessionID,
				"assistant_message_id", assistant.ID,
				"error", scheduleErr,
			)
			s.writeChatError(
				writer,
				http.StatusServiceUnavailable,
				s.application.Chat.Presentation.Messages.AgentBusy,
			)
			return
		}
		responseFields := s.application.Chat.API.ResponseFields
		writeJSON(writer, http.StatusAccepted, map[string]any{
			responseFields.MessageID: assistant.ID,
			responseFields.SessionID: sessionID,
		})
	}
}

func (s *Server) legacyDeleteChatMessage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	route := s.application.Runtime.HTTP.LegacyRoutes.ChatMessage
	sessionID := configuredPathValue(request, route, 0)
	messageID := configuredPathValue(request, route, 1)
	err := s.chat.DeleteMessage(
		request.Context(),
		principal.ID(),
		sessionID,
		messageID,
	)
	switch {
	case errors.Is(err, chat.ErrTurnActive):
		s.writeChatError(writer, http.StatusConflict, s.application.Chat.Presentation.Messages.TurnActive)
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
	case errors.Is(err, chat.ErrMessageNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.MessageNotFound,
		)
	case err != nil:
		s.writeChatInternalError(writer, err)
	default:
		s.logChatAudit(
			request,
			principal,
			s.application.Chat.Audit.DeleteAction,
			s.application.Chat.Audit.MessageResourceType,
			messageID,
		)
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) legacyChatStream(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	route := s.application.Runtime.HTTP.LegacyRoutes.ChatStream
	sessionID := configuredPathValue(request, route, 0)
	fromID, _ := strconv.ParseInt(
		request.URL.Query().Get(s.application.Chat.API.Fields.FromID),
		10,
		64,
	)
	if lastID, err := strconv.ParseInt(request.Header.Get("Last-Event-ID"), 10, 64); err == nil && lastID >= 0 && lastID < int64(^uint64(0)>>1) && lastID+1 > fromID {
		fromID = lastID + 1
	}
	if fromID < 0 {
		fromID = 0
	}
	subscription, err := s.chat.Subscribe(
		request.Context(),
		principal.ID(),
		sessionID,
		fromID,
	)
	switch {
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(
			writer,
			http.StatusNotFound,
			s.application.Chat.Presentation.Messages.SessionNotFound,
		)
		return
	case errors.Is(err, chat.ErrStreamNotFound):
		writer.WriteHeader(http.StatusNoContent)
		return
	case err != nil:
		s.writeChatInternalError(writer, err)
		return
	}
	defer subscription.Close()
	for name, value := range s.application.Chat.Stream.Headers {
		writer.Header().Set(name, value)
	}
	controller := http.NewResponseController(writer)
	setDeadline := func() bool {
		err := controller.SetWriteDeadline(time.Now().Add(time.Duration(s.application.Chat.Stream.WriteTimeoutMilliseconds) * time.Millisecond))
		return err == nil || errors.Is(err, http.ErrNotSupported)
	}
	if !setDeadline() {
		return
	}
	defer controller.SetWriteDeadline(time.Time{})
	writer.WriteHeader(http.StatusOK)
	flush := func() bool { err := controller.Flush(); return err == nil || errors.Is(err, http.ErrNotSupported) }
	retryFrame := strings.ReplaceAll(
		s.application.Chat.Stream.RetryFrameTemplate,
		"{milliseconds}",
		strconv.Itoa(s.application.Chat.Stream.RetryMilliseconds),
	)
	if _, err := io.WriteString(writer, retryFrame); err != nil {
		return
	}
	for _, event := range subscription.Replay {
		if !setDeadline() {
			return
		}
		if !s.writeChatEvent(writer, event) {
			return
		}
		if s.chatEventIsDone(event) {
			if !flush() {
				return
			}
			return
		}
	}
	if !flush() {
		return
	}
	if subscription.Status != s.application.Chat.State.Active {
		return
	}
	heartbeat := time.NewTicker(
		time.Duration(
			s.application.Chat.Stream.HeartbeatMilliseconds,
		) * time.Millisecond,
	)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-subscription.Events:
			if !open || !setDeadline() || !s.writeChatEvent(writer, event) {
				return
			}
			if !flush() {
				return
			}
			if s.chatEventIsDone(event) {
				return
			}
		case <-heartbeat.C:
			if !setDeadline() {
				return
			}
			if _, err := io.WriteString(
				writer,
				s.application.Chat.Stream.HeartbeatFrame,
			); err != nil {
				return
			}
			if !flush() {
				return
			}
		}
	}
}

func (s *Server) writeChatEvent(
	writer io.Writer,
	event chat.Event,
) bool {
	encoded, err := json.Marshal(event.Data)
	if err != nil {
		return false
	}
	_, err = io.WriteString(
		writer,
		strings.ReplaceAll(s.application.Chat.Stream.IDFrameTemplate, "{id}", strconv.FormatInt(event.ID, 10))+
			s.application.Chat.Stream.EventPrefix+
			string(encoded)+
			s.application.Chat.Stream.EventSuffix,
	)
	return err == nil
}

func (s *Server) chatEventIsDone(event chat.Event) bool {
	return event.Data[s.application.Chat.Stream.EventFields.Type] ==
		s.application.Chat.Stream.EventTypes.Done
}

func (s *Server) chatSessionWire(
	writer http.ResponseWriter,
	session chat.Session,
	includeCount bool,
) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	title := session.Title
	if session.TitleSource == chat.TitleSourcePending {
		// Until the automatic title arrives the session shows the Schema
		// default, in the reader's locale; the stored title stays the default.
		title = traceFromWriter(writer).localeText(title)
	}
	result := map[string]any{
		fields.ID:               session.ID,
		fields.Title:            title,
		fields.CreatedAt:        session.CreatedAt,
		fields.UpdatedAt:        session.UpdatedAt,
		fields.ApprovalMode:     s.chat.NormalizeApprovalMode(session.ApprovalMode),
		fields.AgentScope:       s.chat.NormalizeAgentScope(session.AgentScope),
		fields.EstimatedCredits: session.EstimatedCredits,
	}
	if len(session.PriceTotals) > 0 {
		delete(result, fields.EstimatedCredits)
		result["price_totals"] = session.PriceTotals
	}
	if includeCount {
		result[fields.Count] = map[string]any{
			fields.Messages: session.Messages,
		}
	}
	return result
}

func (s *Server) chatMessageWire(locale string, message chat.Message) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	images, _ := json.Marshal(message.ImageURLs)
	tools := make([]map[string]any, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		item := map[string]any{
			fields.ToolName:      call.Name,
			fields.ToolArguments: string(call.Arguments),
		}
		if call.TaskID != "" {
			item[fields.TaskID] = call.TaskID
		}
		if call.NativeQuote != nil {
			item["native_quote"] = s.quoteIn(locale, generation.Offering{}, call.NativeQuote)
		}
		if call.NativeQuote == nil && call.EstimatedCredits > 0 {
			item[fields.EstimatedCredits] = call.EstimatedCredits
		}
		if call.ApprovalSource != "" {
			item[fields.ApprovalSource] = call.ApprovalSource
		}
		tools = append(tools, item)
	}
	toolJSON, _ := json.Marshal(tools)
	return map[string]any{
		fields.ID:          message.ID,
		fields.SessionID:   message.SessionID,
		fields.Role:        message.Role,
		fields.Content:     message.Content,
		fields.ImageURLs:   string(images),
		fields.Attachments: s.chatAttachmentWire(message.Attachments),
		fields.ToolCalls:   string(toolJSON),
		fields.CreatedAt:   message.CreatedAt,
	}
}

// chatAttachmentWire projects a message's attachments with the Schema's member
// names. It carries platform IDs and handles only.
func (s *Server) chatAttachmentWire(attachments []chat.Attachment) []map[string]any {
	names := s.application.Chat.API.Attachment
	result := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		item := map[string]any{
			names.Handle: attachment.Handle,
			names.Kind:   attachment.Kind,
		}
		for name, value := range map[string]string{
			names.Role:      attachment.Role,
			names.AssetID:   attachment.AssetID,
			names.TaskID:    attachment.TaskID,
			names.CaptureID: attachment.CaptureID,
			names.URL:       attachment.URL,
		} {
			if value != "" {
				item[name] = value
			}
		}
		if attachment.ImageSlot > 0 {
			item[names.ImageSlot] = attachment.ImageSlot
		}
		result = append(result, item)
	}
	return result
}

// maximumAttachmentClaimLength bounds each string of an attachment claim; a
// platform ID or URL is far shorter.
const maximumAttachmentClaimLength = 2048

// maximumCanvasSourceClaims bounds the layer pictures one artboard snapshot
// claim may name (#189): each is verified with storage reads, and resolving a
// source Task needs only the few picture layers a person arranges.
const maximumCanvasSourceClaims = 64

// chatAttachmentRefs reads untrusted attachment claims. Only the Schema's
// member names are read, every value is bounded, and unknown members are
// ignored; the service decides what is true.
func (s *Server) chatAttachmentRefs(body map[string]json.RawMessage, field string) []chat.AttachmentRef {
	if field == "" {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body[field], &items); err != nil {
		return nil
	}
	limit := s.application.Chat.Context.MaximumAttachmentsPerMessage + s.application.Chat.Context.MaximumSelection
	refs := make([]chat.AttachmentRef, 0, len(items))
	for _, item := range items {
		if limit > 0 && len(refs) >= limit {
			break
		}
		refs = append(refs, s.chatAttachmentRef(item))
	}
	return refs
}

// chatMentions reads the untrusted `@` references of a message: a token and the
// claim about the picture it names, with the Schema's member names.
func (s *Server) chatMentions(body map[string]json.RawMessage, field string) []chat.MentionRef {
	if field == "" {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body[field], &items); err != nil {
		return nil
	}
	limit := s.application.Chat.Context.MaximumAttachmentsPerMessage + s.application.Chat.Context.MaximumSelection
	tokenName := s.application.Chat.API.Attachment.Token
	mentions := make([]chat.MentionRef, 0, len(items))
	for _, item := range items {
		if limit > 0 && len(mentions) >= limit {
			break
		}
		var token string
		if err := json.Unmarshal(item[tokenName], &token); err != nil {
			continue
		}
		mentions = append(mentions, chat.MentionRef{Token: token, Ref: s.chatAttachmentRef(item)})
	}
	return mentions
}

// chatAttachmentRef reads one untrusted claim with the Schema's member names.
func (s *Server) chatAttachmentRef(item map[string]json.RawMessage) chat.AttachmentRef {
	names := s.application.Chat.API.Attachment
	text := func(name string) string {
		var value string
		if err := json.Unmarshal(item[name], &value); err != nil || len(value) > maximumAttachmentClaimLength {
			return ""
		}
		return value
	}
	var slot, layers int
	_ = json.Unmarshal(item[names.ImageSlot], &slot)
	if names.CanvasLayers != "" {
		// Only a count; the service bounds it and reads it for the canvas snapshot alone (#272).
		_ = json.Unmarshal(item[names.CanvasLayers], &layers)
	}
	var sources []string
	if names.CanvasSources != "" {
		// Picture URLs of the snapshot's layers (#189); the service verifies
		// each one and keeps only the session's Tasks.
		var claimed []string
		if err := json.Unmarshal(item[names.CanvasSources], &claimed); err == nil {
			for _, source := range claimed {
				if source != "" && len(source) <= maximumAttachmentClaimLength && len(sources) < maximumCanvasSourceClaims {
					sources = append(sources, source)
				}
			}
		}
	}
	return chat.AttachmentRef{
		Kind:          text(names.Kind),
		URL:           text(names.URL),
		AssetID:       text(names.AssetID),
		TaskID:        text(names.TaskID),
		CaptureID:     text(names.CaptureID),
		ImageSlot:     slot,
		CanvasLayers:  layers,
		CanvasSources: sources,
	}
}

func (s *Server) decodeOptionalChatObject(
	writer http.ResponseWriter,
	request *http.Request,
) map[string]json.RawMessage {
	if request.Body == nil {
		return map[string]json.RawMessage{}
	}
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
	var body map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return map[string]json.RawMessage{}
	}
	return body
}

func (s *Server) decodeRequiredChatObject(
	writer http.ResponseWriter,
	request *http.Request,
) (map[string]json.RawMessage, bool) {
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
	decoder := json.NewDecoder(request.Body)
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		s.writeChatInternalError(writer, err)
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeChatInternalError(writer, err)
		return nil, false
	}
	return body, true
}

func chatString(body map[string]json.RawMessage, field string) string {
	var value string
	_ = json.Unmarshal(body[field], &value)
	return value
}

func chatStrings(body map[string]json.RawMessage, field string) []string {
	var value []string
	_ = json.Unmarshal(body[field], &value)
	return value
}

func chatInts(body map[string]json.RawMessage, field string) []int {
	var value []int
	_ = json.Unmarshal(body[field], &value)
	return value
}

// chatParameters 只接受对象形式的参数映射；标量、数组或 null 都按无效请求处理。
func chatParameters(
	body map[string]json.RawMessage,
	field string,
) (map[string]json.RawMessage, bool) {
	if field == "" {
		return nil, true
	}
	raw, exists := body[field]
	if !exists {
		return nil, true
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parameters); err != nil || parameters == nil {
		return nil, false
	}
	return parameters, true
}

func (s *Server) writeChatError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	body := map[string]string{
		s.application.Chat.API.ResponseFields.Error: traceFromWriter(writer).localeText(message),
	}
	// #240: the error the person sees carries the ID the access log records.
	if trace := traceFromWriter(writer); trace != nil {
		trace.noteProblem("chat_error", message, nil)
		body["request_id"] = trace.id
	}
	writeJSON(writer, status, body)
}

func (s *Server) writeChatInternalError(writer http.ResponseWriter, causes ...error) {
	for _, cause := range causes {
		noteCause(writer, cause)
	}
	s.writeChatError(
		writer,
		http.StatusInternalServerError,
		s.application.Chat.Presentation.Messages.InternalError,
	)
}

// legacyCancelChatTurn 用户停止当前会话的活动轮次：服务端中断 LLM 与
// 工具执行并尽力取消已绑定的生成任务，而不是只断开前端 SSE。
func (s *Server) legacyCancelChatTurn(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	sessionID := configuredPathValue(
		request,
		s.application.Runtime.HTTP.LegacyRoutes.ChatTurnCancel,
		0,
	)
	if sessionID == "" {
		s.writeChatError(
			writer,
			http.StatusBadRequest,
			s.application.Chat.Presentation.Messages.InvalidParameters,
		)
		return
	}
	active, err := s.chatScheduler.CancelTurn(
		request.Context(),
		principal.ID(),
		sessionID,
	)
	if err != nil {
		s.logger.ErrorContext(
			request.Context(),
			"停止对话轮次失败",
			"session_id", sessionID,
			"error", err,
		)
		s.writeChatInternalError(writer, err)
		return
	}
	s.logChatAudit(
		request,
		principal,
		s.application.Chat.Audit.DeleteAction,
		s.application.Chat.Audit.MessageResourceType,
		sessionID,
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		s.application.Chat.API.Fields.TurnCanceled: active,
	})
}

func (s *Server) logChatAudit(
	request *http.Request,
	principal access.Principal,
	action string,
	resourceType string,
	resourceID string,
) {
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       action,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
}

func configuredPathValue(
	request *http.Request,
	route string,
	index int,
) string {
	names := configuredPathParameters(route)
	if index < 0 || index >= len(names) {
		return ""
	}
	return request.PathValue(names[index])
}

func configuredPathParameters(route string) []string {
	var names []string
	for {
		start := strings.IndexByte(route, '{')
		if start < 0 {
			break
		}
		route = route[start+1:]
		end := strings.IndexByte(route, '}')
		if end < 0 {
			break
		}
		names = append(names, route[:end])
		route = route[end+1:]
	}
	return names
}

// maximumDomainModels bounds the per-domain model map of one request.
const maximumDomainModels = 8

// chatDomainModels reads the model the user chose in each workspace domain.
// Only plain string entries are kept; the tools match them against real
// Offerings, so an unknown domain or model id has no effect.
func chatDomainModels(body map[string]json.RawMessage, field string) map[string]string {
	if field == "" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body[field], &raw); err != nil {
		return nil
	}
	result := make(map[string]string, len(raw))
	for domain, encoded := range raw {
		var model string
		if len(result) >= maximumDomainModels || json.Unmarshal(encoded, &model) != nil ||
			model == "" || len(model) > maximumAttachmentClaimLength || len(domain) > maximumAttachmentClaimLength {
			continue
		}
		result[domain] = model
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
