package chat_test

import (
	"context"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestSessionLifecycleIsOwnerScopedAndUsesConfiguredDefault(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()

	created, err := service.CreateSession(
		ctx,
		"owner-a",
		"Configured Owner",
		"   ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "configured default title" {
		t.Fatalf("title = %q", created.Title)
	}
	if _, err := service.RenameSession(
		ctx,
		"owner-b",
		created.ID,
		"not allowed",
	); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("cross-owner rename error = %v", err)
	}
	updated, err := service.RenameSession(
		ctx,
		"owner-a",
		created.ID,
		"  configured title  ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "configured title" {
		t.Fatalf("updated title = %q", updated.Title)
	}

	other, err := service.CreateSession(
		ctx,
		"owner-b",
		"Other",
		"other title",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := service.ListSessions(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != created.ID {
		t.Fatalf("owner-a sessions = %#v; other = %#v", sessions, other)
	}
	if err := service.DeleteSession(ctx, "owner-b", created.ID); !errors.Is(
		err,
		chat.ErrSessionNotFound,
	) {
		t.Fatalf("cross-owner delete error = %v", err)
	}
	if err := service.DeleteSession(ctx, "owner-a", created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestListSessionsPageReportsTheOwnerAndSearchTotal(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	for _, title := range []string{"cat one", "cat two", "dog"} {
		if _, err := service.CreateSession(ctx, "owner-a", "Owner", title); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateSession(ctx, "owner-b", "Other", "cat three"); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListSessionsPage(ctx, "owner-a", chat.SessionListQuery{Search: "cat", Limit: 1})
	if err != nil || len(page.Sessions) != 1 || page.Total != 2 {
		t.Fatalf("page = %d sessions, total %d: %v", len(page.Sessions), page.Total, err)
	}
	empty, err := service.ListSessionsPage(ctx, "owner-a", chat.SessionListQuery{Search: "missing", Limit: 10})
	if err != nil || len(empty.Sessions) != 0 || empty.Total != 0 {
		t.Fatalf("empty page = %d sessions, total %d: %v", len(empty.Sessions), empty.Total, err)
	}
	if _, err := service.ListSessionsPage(ctx, "owner-a", chat.SessionListQuery{Search: "cat", Limit: 0}); !errors.Is(err, chat.ErrInvalidSessionPage) {
		t.Fatalf("invalid limit error = %v", err)
	}
}

func TestStartTurnPersistsConfiguredRolesPendingStateAndCreatedEvent(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(
		ctx,
		"owner-a",
		"Owner",
		"chat",
	)
	if err != nil {
		t.Fatal(err)
	}

	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   " configured content ",
		ImageURLs: []string{"/configured/image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assistant.Role != "configured-assistant" ||
		assistant.Content != "configured-pending" {
		t.Fatalf("assistant = %#v", assistant)
	}
	messages, err := repository.ListMessages(
		ctx,
		"owner-a",
		session.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[0].Role != "configured-user" ||
		messages[0].Content != "configured content" {
		t.Fatalf("messages = %#v", messages)
	}
	events, found, err := repository.ListEvents(
		ctx,
		"owner-a",
		session.ID,
		0,
	)
	if err != nil || !found {
		t.Fatalf("events found=%t error=%v", found, err)
	}
	if len(events) != 1 ||
		events[0].Data["configured-kind"] != "configured-message-created" ||
		events[0].Data["configured-message"] != assistant.ID {
		t.Fatalf("events = %#v", events)
	}
}

func TestStartTurnRejectsSemanticallyEmptyInput(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	_, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: "session-a",
		Content:   " ",
	})
	if !errors.Is(err, chat.ErrEmptyMessage) {
		t.Fatalf("error = %v", err)
	}
}

func TestListMessagesHealsStaleOrphanUsingConfiguredPresentation(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.CreatedAt = time.Date(
		2026,
		7,
		26,
		9,
		58,
		0,
		0,
		time.UTC,
	)
	assistant.ToolCalls = []chat.ToolCall{{
		ID:        "call-a",
		Name:      "tool-a",
		Arguments: []byte(`{"configured-prompt":"paint a castle"}`),
	}}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateTurnState(
		ctx,
		assistant.ID,
		"configured-turn-done",
		assistant.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}

	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[1].Content != "configured interrupted paint" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestListMessagesPageHealsStaleOrphanAndRejectsBadQueries(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.CreatedAt = time.Date(2026, 7, 26, 9, 58, 0, 0, time.UTC)
	assistant.ToolCalls = []chat.ToolCall{{
		ID:        "call-a",
		Name:      "tool-a",
		Arguments: []byte(`{"configured-prompt":"paint a castle"}`),
	}}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateTurnState(ctx, assistant.ID, "configured-turn-done", assistant.CreatedAt); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != assistant.ID ||
		page.Messages[0].Content != "configured interrupted paint" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	older, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{Before: page.NextCursor, Limit: 1})
	if err != nil || len(older.Messages) != 1 || older.Messages[0].Content != "create" || older.NextCursor != "" {
		t.Fatalf("older page = %#v, %v", older, err)
	}
	if _, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{}); !errors.Is(err, chat.ErrInvalidMessagePage) {
		t.Fatalf("zero limit error = %v", err)
	}
	if _, err := service.ListMessagesPage(ctx, "owner-b", session.ID, chat.MessageListQuery{Limit: 1}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign session error = %v", err)
	}
}

func TestListMessagesPreservesPlaceholderForRecoverableTurn(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.CreatedAt = time.Date(
		2026,
		7,
		26,
		9,
		58,
		0,
		0,
		time.UTC,
	)
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}

	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[1].Content != "configured-pending" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestListMessagesKeepsOrphanedPersistedImages(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.ImageURLs = []string{"/configured/persisted-image"}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateTurnState(
		ctx,
		assistant.ID,
		"configured-turn-error",
		assistant.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}

	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[1].Content != "" ||
		len(messages[1].ImageURLs) != 1 ||
		messages[1].ImageURLs[0] != "/configured/persisted-image" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestDurableStreamReplaysThenPublishesConfiguredEvents(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	}); err != nil {
		t.Fatal(err)
	}

	subscription, err := service.Subscribe(
		ctx,
		"owner-a",
		session.ID,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if subscription.Status != "configured-active" ||
		len(subscription.Replay) != 1 {
		t.Fatalf("subscription = %#v", subscription)
	}
	configured := chat.Event{
		SessionID: session.ID,
		Data: map[string]any{
			"configured-kind": "configured-text",
		},
	}
	stored, err := service.AppendEvent(ctx, configured)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-subscription.Events:
		if received.ID != stored.ID ||
			received.Data["configured-kind"] != "configured-text" {
			t.Fatalf("received = %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for configured event")
	}
	if err := service.FinishStream(ctx, session.ID, repository.streams[session.ID].AssistantMessageID, false); err != nil {
		t.Fatal(err)
	}
}

func newChatService(
	t *testing.T,
	repository chat.Repository,
) *chat.Service {
	t.Helper()
	return newChatServiceWith(t, repository, nil)
}

// newChatServiceWith builds the configured test service and lets a test
// adjust the config (handle syntax, attachment verifier) before it is used.
func newChatServiceWith(
	t *testing.T,
	repository chat.Repository,
	adjust func(*chat.ServiceConfig),
) *chat.Service {
	t.Helper()
	current := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	sequence := 0
	config := chat.ServiceConfig{
		Repository: repository,
		ApprovalPolicy: approvalpolicy.New("manual", []string{"auto", "manual"},
			[]string{"text-to-3d"}, []string{"text-to-3d", "text-to-image"}),
		Clock: func() time.Time { return current },
		NewID: func(kind string) string {
			sequence++
			return kind + "-configured-" + string(rune('0'+sequence))
		},
		DefaultTitle:    "configured default title",
		UserRole:        "configured-user",
		AssistantRole:   "configured-assistant",
		PendingContent:  "configured-pending",
		StreamActive:    "configured-active",
		StreamDone:      "configured-done",
		StreamError:     "configured-error",
		TurnQueued:      "configured-turn-queued",
		TurnRunning:     "configured-turn-running",
		TurnWaitingTask: "configured-turn-waiting-task",
		TurnDone:        "configured-turn-done",
		TurnError:       "configured-turn-error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter:                time.Minute,
			PromptArgument:            "configured-prompt",
			PromptPreviewCharacters:   5,
			InterruptedMessage:        "configured interrupted",
			InterruptedPromptTemplate: "configured interrupted {prompt}",
		},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 8,
		EventFields: chat.EventFieldPolicy{
			Type:      "configured-kind",
			MessageID: "configured-message",
		},
		EventTypes: chat.EventTypePolicy{
			MessageCreated: "configured-message-created",
		},
	}
	if adjust != nil {
		adjust(&config)
	}
	service, err := chat.NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryChatRepository struct {
	mu       sync.RWMutex
	sessions map[string]chat.Session
	messages map[string][]chat.Message
	events   map[string][]chat.Event
	streams  map[string]chat.Stream
	turns    map[string]chat.Turn
	notes    map[string]string
	picks    map[string]chat.Attachment
}

func newMemoryChatRepository() *memoryChatRepository {
	return &memoryChatRepository{
		sessions: map[string]chat.Session{},
		messages: map[string][]chat.Message{},
		events:   map[string][]chat.Event{},
		streams:  map[string]chat.Stream{},
		turns:    map[string]chat.Turn{},
	}
}

func (r *memoryChatRepository) ListSessions(
	_ context.Context,
	ownerID string,
) ([]chat.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []chat.Session
	for _, session := range r.sessions {
		if session.OwnerID == ownerID {
			session.Messages = len(r.messages[session.ID])
			result = append(result, session)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (r *memoryChatRepository) ListSessionsPage(
	ctx context.Context,
	ownerID string,
	query chat.SessionListQuery,
) (chat.SessionPage, error) {
	sessions, err := r.ListSessions(ctx, ownerID)
	if err != nil {
		return chat.SessionPage{}, err
	}
	var filtered []chat.Session
	for _, session := range sessions {
		if strings.Contains(strings.ToLower(session.Title), strings.ToLower(query.Search)) {
			filtered = append(filtered, session)
		}
	}
	total := len(filtered)
	if len(filtered) > query.Limit {
		filtered = filtered[:query.Limit]
	}
	return chat.SessionPage{Sessions: filtered, Total: total}, nil
}

func (r *memoryChatRepository) CreateSession(
	_ context.Context,
	session chat.Session,
) (chat.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = session
	return session, nil
}

func (r *memoryChatRepository) RenameSession(
	_ context.Context,
	ownerID string,
	sessionID string,
	title string,
	updatedAt time.Time,
) (chat.Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Session{}, false, nil
	}
	session.Title = title
	session.TitleSource = chat.TitleSourceUser
	session.UpdatedAt = updatedAt
	r.sessions[sessionID] = session
	return session, true, nil
}

func (r *memoryChatRepository) ApplyAutoTitle(
	_ context.Context,
	ownerID string,
	sessionID string,
	title string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID || session.TitleSource != chat.TitleSourcePending {
		return false, nil
	}
	session.Title = title
	session.TitleSource = chat.TitleSourceAuto
	r.sessions[sessionID] = session
	return true, nil
}

func (r *memoryChatRepository) Session(
	_ context.Context,
	ownerID string,
	sessionID string,
) (chat.Session, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Session{}, false, nil
	}
	return session, true, nil
}

func (r *memoryChatRepository) SetSessionApprovalMode(
	_ context.Context,
	ownerID string,
	sessionID string,
	mode string,
) (chat.Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Session{}, false, nil
	}
	session.ApprovalMode = mode
	r.sessions[sessionID] = session
	return session, true, nil
}

func (r *memoryChatRepository) SetSessionAgentScope(
	_ context.Context,
	ownerID string,
	sessionID string,
	scope string,
) (chat.Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Session{}, false, nil
	}
	session.AgentScope = scope
	r.sessions[sessionID] = session
	return session, true, nil
}

func (r *memoryChatRepository) SessionAgentScope(
	_ context.Context,
	ownerID string,
	sessionID string,
) (string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return "", false, nil
	}
	return session.AgentScope, true, nil
}

func (r *memoryChatRepository) SessionNotes(
	_ context.Context,
	ownerID string,
	sessionID string,
) (string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return "", false, nil
	}
	return r.notes[sessionID], true, nil
}

func (r *memoryChatRepository) SetSessionNotes(
	_ context.Context,
	ownerID string,
	sessionID string,
	notes string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return false, nil
	}
	if r.notes == nil {
		r.notes = map[string]string{}
	}
	r.notes[sessionID] = notes
	return true, nil
}

func (r *memoryChatRepository) SessionPick(
	_ context.Context,
	ownerID string,
	sessionID string,
) (chat.Attachment, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return chat.Attachment{}, false, nil
	}
	return r.picks[sessionID], true, nil
}

func (r *memoryChatRepository) SetSessionPick(
	_ context.Context,
	ownerID string,
	sessionID string,
	pick chat.Attachment,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return false, nil
	}
	if r.picks == nil {
		r.picks = map[string]chat.Attachment{}
	}
	r.picks[sessionID] = pick
	return true, nil
}

func (r *memoryChatRepository) DeleteSession(
	_ context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.sessions[sessionID]
	if !found || session.OwnerID != ownerID {
		return false, nil
	}
	delete(r.sessions, sessionID)
	delete(r.notes, sessionID)
	delete(r.picks, sessionID)
	delete(r.messages, sessionID)
	delete(r.events, sessionID)
	return true, nil
}

func (r *memoryChatRepository) OwnsSession(
	_ context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ownsSession(ownerID, sessionID), nil
}

func (r *memoryChatRepository) ownsSession(
	ownerID string,
	sessionID string,
) bool {
	session, found := r.sessions[sessionID]
	return found && session.OwnerID == ownerID
}

func (r *memoryChatRepository) ListMessages(
	_ context.Context,
	ownerID string,
	sessionID string,
) ([]chat.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ownsSession(ownerID, sessionID) {
		return nil, chat.ErrSessionNotFound
	}
	result := make([]chat.Message, len(r.messages[sessionID]))
	for index, message := range r.messages[sessionID] {
		result[index] = cloneChatMessage(message)
	}
	return result, nil
}

// ListMessagesPage pages by position; the cursor is the index of the oldest
// message already read.
func (r *memoryChatRepository) ListMessagesPage(
	ctx context.Context,
	ownerID string,
	sessionID string,
	query chat.MessageListQuery,
) (chat.MessagePage, error) {
	messages, err := r.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return chat.MessagePage{}, err
	}
	end := len(messages)
	if query.Before != "" {
		parsed, err := strconv.Atoi(query.Before)
		if err != nil || parsed < 0 || parsed > len(messages) {
			return chat.MessagePage{}, chat.ErrInvalidMessagePage
		}
		end = parsed
	}
	start := max(end-query.Limit, 0)
	page := chat.MessagePage{Messages: messages[start:end]}
	if start > 0 {
		page.NextCursor = strconv.Itoa(start)
	}
	return page, nil
}

func (r *memoryChatRepository) StartTurn(
	_ context.Context,
	ownerID string,
	user chat.Message,
	assistant chat.Message,
	event chat.Event,
	stream chat.Stream,
	turn chat.Turn,
) (chat.Message, chat.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsSession(ownerID, user.SessionID) {
		return chat.Message{}, chat.Event{}, chat.ErrSessionNotFound
	}
	r.messages[user.SessionID] = append(
		r.messages[user.SessionID],
		user,
		assistant,
	)
	r.events[event.SessionID] = nil
	r.streams[event.SessionID] = stream
	r.turns[assistant.ID] = turn
	event.ID = int64(len(r.events[event.SessionID]))
	r.events[event.SessionID] = append(r.events[event.SessionID], event)
	return assistant, event, nil
}

func (r *memoryChatRepository) ListSessionTurns(
	_ context.Context,
	ownerID string,
	sessionID string,
	states []string,
) ([]chat.Turn, error) {
	turns, err := r.ListTurns(context.Background(), states)
	if err != nil {
		return nil, err
	}
	filtered := turns[:0]
	for _, turn := range turns {
		if turn.OwnerID == ownerID && turn.SessionID == sessionID {
			filtered = append(filtered, turn)
		}
	}
	return filtered, nil
}

func (r *memoryChatRepository) RecentSessionTurnOptions(
	_ context.Context,
	ownerID string,
	sessionID string,
	limit int,
) ([]chat.TurnOptions, error) {
	r.mu.RLock()
	var turns []chat.Turn
	for _, turn := range r.turns {
		if turn.OwnerID == ownerID && turn.SessionID == sessionID {
			turns = append(turns, turn)
		}
	}
	r.mu.RUnlock()
	sort.Slice(turns, func(i, j int) bool { return turns[i].CreatedAt.After(turns[j].CreatedAt) })
	var result []chat.TurnOptions
	for index, turn := range turns {
		if index >= limit {
			break
		}
		result = append(result, turn.Options)
	}
	return result, nil
}

func (r *memoryChatRepository) ListTurns(
	_ context.Context,
	states []string,
) ([]chat.Turn, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allowed := map[string]bool{}
	for _, state := range states {
		allowed[state] = true
	}
	var result []chat.Turn
	for _, turn := range r.turns {
		if allowed[turn.State] {
			result = append(result, turn)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (r *memoryChatRepository) GetTurn(_ context.Context, ownerID, sessionID, assistantID string) (chat.Turn, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	turn, ok := r.turns[assistantID]
	if !ok || turn.OwnerID != ownerID || turn.SessionID != sessionID {
		return chat.Turn{}, chat.ErrMessageNotFound
	}
	return turn, nil
}

func (r *memoryChatRepository) ListTurnsPage(_ context.Context, states []string, cursor chat.TurnPageCursor, limit int) ([]chat.Turn, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allowed := map[string]bool{}
	for _, state := range states {
		allowed[state] = true
	}
	var result []chat.Turn
	for _, turn := range r.turns {
		if !allowed[turn.State] || !cursor.CreatedAt.IsZero() &&
			(turn.CreatedAt.Before(cursor.CreatedAt) || turn.CreatedAt.Equal(cursor.CreatedAt) && turn.AssistantMessageID <= cursor.AssistantMessageID) {
			continue
		}
		result = append(result, turn)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].AssistantMessageID < result[j].AssistantMessageID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *memoryChatRepository) CompareAndSwapTurnCheckpoint(_ context.Context, ownerID, sessionID, assistantID string,
	version int64, oldState, newState string, checkpoint chat.TurnCheckpoint, allowStopped bool, updatedAt time.Time,
) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, ok := r.turns[assistantID]
	if !ok || turn.OwnerID != ownerID || turn.SessionID != sessionID || turn.CheckpointVersion != version ||
		turn.State != oldState || turn.StopRequested && !allowStopped {
		return 0, chat.ErrTurnCheckpointConflict
	}
	turn.CheckpointVersion++
	turn.Checkpoint = checkpoint
	turn.State = newState
	turn.UpdatedAt = updatedAt
	r.turns[assistantID] = turn
	return turn.CheckpointVersion, nil
}

func (r *memoryChatRepository) UpdateTurnState(
	_ context.Context,
	assistantMessageID string,
	state string,
	updatedAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, found := r.turns[assistantMessageID]
	if !found {
		return chat.ErrMessageNotFound
	}
	turn.State = state
	turn.UpdatedAt = updatedAt
	r.turns[assistantMessageID] = turn
	return nil
}

func (r *memoryChatRepository) RequestTurnStop(
	_ context.Context, ownerID, sessionID string, states []string, updatedAt time.Time,
) (chat.Turn, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	allowed := map[string]bool{}
	for _, state := range states {
		allowed[state] = true
	}
	for id, turn := range r.turns {
		if turn.OwnerID != ownerID || turn.SessionID != sessionID || !allowed[turn.State] {
			continue
		}
		turn.StopRequested = true
		turn.UpdatedAt = updatedAt
		r.turns[id] = turn
		return turn, true, nil
	}
	return chat.Turn{}, false, nil
}

func (r *memoryChatRepository) TurnStopRequested(
	_ context.Context, ownerID, sessionID, assistantMessageID string,
) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	turn, found := r.turns[assistantMessageID]
	if !found || turn.OwnerID != ownerID || turn.SessionID != sessionID {
		return false, chat.ErrMessageNotFound
	}
	return turn.StopRequested, nil
}

func (r *memoryChatRepository) ReadStream(
	_ context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
	now time.Time,
) (chat.Stream, []chat.Event, bool, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ownsSession(ownerID, sessionID) {
		return chat.Stream{}, nil, false, false, nil
	}
	stream, found := r.streams[sessionID]
	if !found ||
		(stream.CleanupAt != nil && !stream.CleanupAt.After(now)) {
		return chat.Stream{}, nil, true, false, nil
	}
	var result []chat.Event
	for _, event := range r.events[sessionID] {
		if event.ID >= fromID {
			result = append(result, event)
		}
	}
	return stream, result, true, true, nil
}

func (r *memoryChatRepository) FinishStream(
	_ context.Context,
	sessionID string,
	assistantMessageID string,
	status string,
	cleanupAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stream := r.streams[sessionID]
	if stream.AssistantMessageID != assistantMessageID {
		return chat.ErrStreamNotFound
	}
	stream.Status = status
	stream.CleanupAt = &cleanupAt
	r.streams[sessionID] = stream
	return nil
}

func (r *memoryChatRepository) DeleteMessage(
	_ context.Context,
	ownerID string,
	sessionID string,
	messageID string,
) (bool, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsSession(ownerID, sessionID) {
		return false, false, nil
	}
	for index, message := range r.messages[sessionID] {
		if message.ID == messageID {
			r.messages[sessionID] = append(
				r.messages[sessionID][:index],
				r.messages[sessionID][index+1:]...,
			)
			return true, true, nil
		}
	}
	return true, false, nil
}

func (r *memoryChatRepository) UpdateMessage(
	_ context.Context,
	ownerID string,
	message chat.Message,
	updatedAt time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsSession(ownerID, message.SessionID) {
		return false, nil
	}
	for index, current := range r.messages[message.SessionID] {
		if current.ID == message.ID {
			// Match SQLite serialization: a caller can keep mutating its copy
			// after UpdateMessage without racing a concurrent ListMessages.
			r.messages[message.SessionID][index] = cloneChatMessage(message)
			session := r.sessions[message.SessionID]
			session.UpdatedAt = updatedAt
			r.sessions[message.SessionID] = session
			return true, nil
		}
	}
	return false, nil
}

func (r *memoryChatRepository) InsertMessageIfAbsent(
	_ context.Context,
	ownerID string,
	message chat.Message,
	updatedAt time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsSession(ownerID, message.SessionID) {
		return false, chat.ErrSessionNotFound
	}
	for _, current := range r.messages[message.SessionID] {
		if current.ID == message.ID {
			return false, nil
		}
	}
	r.messages[message.SessionID] = append(r.messages[message.SessionID], cloneChatMessage(message))
	session := r.sessions[message.SessionID]
	session.UpdatedAt = updatedAt
	r.sessions[message.SessionID] = session
	return true, nil
}

func (r *memoryChatRepository) AppendEvent(
	_ context.Context,
	event chat.Event,
) (chat.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.ID = int64(len(r.events[event.SessionID]))
	r.events[event.SessionID] = append(r.events[event.SessionID], event)
	return event, nil
}

func (r *memoryChatRepository) ListEvents(
	_ context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
) ([]chat.Event, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ownsSession(ownerID, sessionID) {
		return nil, false, nil
	}
	var result []chat.Event
	for _, event := range r.events[sessionID] {
		if event.ID >= fromID {
			result = append(result, event)
		}
	}
	return result, true, nil
}

func cloneChatMessage(message chat.Message) chat.Message {
	message.ImageURLs = append([]string(nil), message.ImageURLs...)
	message.ToolCalls = append([]chat.ToolCall(nil), message.ToolCalls...)
	return message
}
