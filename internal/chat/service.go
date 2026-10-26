package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	ErrSessionNotFound = errors.New("chat: session not found")
	ErrMessageNotFound = errors.New("chat: message not found")
	ErrEmptyTitle      = errors.New("chat: empty title")
	ErrEmptyMessage    = errors.New("chat: empty message")
	// ErrInvalidInputSource rejects an input source the Schema does not declare;
	// ErrCanvasSourceMissing a canvas turn without a verified snapshot (#266).
	ErrInvalidInputSource     = errors.New("chat: invalid input source")
	ErrCanvasSourceMissing    = errors.New("chat: canvas input source without a snapshot")
	ErrTurnActive             = errors.New("chat: turn already active")
	ErrStreamNotFound         = errors.New("chat: stream not found")
	ErrInvalidSessionPage     = errors.New("chat: invalid session page query")
	ErrInvalidMessagePage     = errors.New("chat: invalid message page query")
	ErrTurnCheckpointConflict = errors.New("chat: turn checkpoint conflict")
	ErrInvalidApprovalMode    = errors.New("chat: invalid approval mode")
	ErrInvalidAgentScope      = errors.New("chat: invalid agent scope")
)

type Repository interface {
	ListSessions(context.Context, string) ([]Session, error)
	ListSessionsPage(context.Context, string, SessionListQuery) (SessionPage, error)
	CreateSession(context.Context, Session) (Session, error)
	RenameSession(
		context.Context,
		string,
		string,
		string,
		time.Time,
	) (Session, bool, error)
	DeleteSession(context.Context, string, string) (bool, error)
	Session(context.Context, string, string) (Session, bool, error)
	// ApplyAutoTitle sets the title only while it is still pending and
	// reports whether it did. It is one conditional write, so a rename
	// racing the summary wins.
	ApplyAutoTitle(ctx context.Context, owner, session, title string) (bool, error)
	OwnsSession(context.Context, string, string) (bool, error)
	// SetSessionApprovalMode changes only the mode of the owner's session and
	// reports whether the session exists. It does not touch updated_at.
	SetSessionApprovalMode(ctx context.Context, owner, session, mode string) (Session, bool, error)
	// SetSessionAgentScope changes only the Agent scope of the owner's session
	// and reports whether the session exists. It does not touch updated_at.
	// SessionAgentScope reads it (found is false for a missing or foreign
	// session).
	SetSessionAgentScope(ctx context.Context, owner, session, scope string) (Session, bool, error)
	SessionAgentScope(ctx context.Context, owner, session string) (string, bool, error)
	// SessionNotes and SetSessionNotes read and replace the session's settings
	// summary. Both are scoped to the owner; found is false for a missing or
	// foreign session. Writing does not touch updated_at.
	SessionNotes(ctx context.Context, owner, session string) (string, bool, error)
	SetSessionNotes(ctx context.Context, owner, session, notes string) (bool, error)
	// SessionPick and SetSessionPick read and replace the candidate the user
	// picked as the session's current object (platform IDs only; a zero
	// Attachment clears it). Both are scoped to the owner; found is false for a
	// missing or foreign session. Writing does not touch updated_at.
	SessionPick(ctx context.Context, owner, session string) (Attachment, bool, error)
	SetSessionPick(ctx context.Context, owner, session string, pick Attachment) (bool, error)
	ListMessages(context.Context, string, string) ([]Message, error)
	// ListMessagesPage reads one page of the owner's session, newest first by
	// the same order as ListMessages, returned oldest first.
	ListMessagesPage(context.Context, string, string, MessageListQuery) (MessagePage, error)
	StartTurn(
		context.Context,
		string,
		Message,
		Message,
		Event,
		Stream,
		Turn,
	) (Message, Event, error)
	DeleteMessage(
		context.Context,
		string,
		string,
		string,
	) (bool, bool, error)
	UpdateMessage(
		context.Context,
		string,
		Message,
		time.Time,
	) (bool, error)
	// InsertMessageIfAbsent adds a message outside a turn, scoped to the
	// owner's session. A message with the same ID in that session is left
	// untouched and reported as not created, so a retried post is harmless.
	InsertMessageIfAbsent(ctx context.Context, ownerID string, message Message, at time.Time) (bool, error)
	AppendEvent(context.Context, Event) (Event, error)
	ListEvents(
		context.Context,
		string,
		string,
		int64,
	) ([]Event, bool, error)
	ReadStream(
		context.Context,
		string,
		string,
		int64,
		time.Time,
	) (Stream, []Event, bool, bool, error)
	FinishStream(context.Context, string, string, string, time.Time) error
	ListTurns(context.Context, []string) ([]Turn, error)
	GetTurn(context.Context, string, string, string) (Turn, error)
	ListTurnsPage(context.Context, []string, TurnPageCursor, int) ([]Turn, error)
	CompareAndSwapTurnCheckpoint(context.Context, string, string, string, int64, string, string, TurnCheckpoint, bool, time.Time) (int64, error)
	ListSessionTurns(context.Context, string, string, []string) ([]Turn, error)
	// RecentSessionTurnOptions lists the options of the session's newest
	// turns, newest first, at most the given number, in any state.
	RecentSessionTurnOptions(context.Context, string, string, int) ([]TurnOptions, error)
	UpdateTurnState(
		context.Context,
		string,
		string,
		time.Time,
	) error
	RequestTurnStop(context.Context, string, string, []string, time.Time) (Turn, bool, error)
	TurnStopRequested(context.Context, string, string, string) (bool, error)
}

type EventFieldPolicy struct {
	Type      string
	MessageID string
}

type EventTypePolicy struct {
	MessageCreated string
}

type PlaceholderHealingPolicy struct {
	StaleAfter                time.Duration
	PromptArgument            string
	PromptPreviewCharacters   int
	InterruptedMessage        string
	InterruptedPromptTemplate string
}

type ServiceConfig struct {
	Repository           Repository
	Clock                func() time.Time
	NewID                func(string) string
	DefaultTitle         string
	UserRole             string
	AssistantRole        string
	PendingContent       string
	StreamActive         string
	StreamDone           string
	StreamError          string
	TurnQueued           string
	TurnRunning          string
	TurnWaitingTask      string
	TurnDone             string
	TurnError            string
	PlaceholderHealing   PlaceholderHealingPolicy
	StreamCleanupTTL     time.Duration
	SubscriberBufferSize int
	EventFields          EventFieldPolicy
	EventTypes           EventTypePolicy
	// ApprovalPolicy comes from application.chat.approval_policy. A zero
	// policy offers only manual mode.
	ApprovalPolicy approvalpolicy.Policy
	// AgentScope comes from application.chat.agent_scope. A zero policy
	// declares no scope and leaves the Agent unrestricted.
	AgentScope agentscope.Policy
	// Handles is the attachment handle syntax from chat.context; Attachments
	// verifies the structured claims of a turn. Without either, messages carry
	// no structured attachments and the Agent keeps using history indices.
	Handles     HandlePolicy
	Attachments AttachmentVerifier
	// MaximumSessionNotes bounds the session settings summary, in characters
	// (chat.context.maximum_session_notes_characters). Zero disables notes.
	MaximumSessionNotes int
	// CanvasInputSource is chat.context.canvas_input_source (#266). Empty: the
	// service accepts no input source.
	CanvasInputSource string
	// Models names the sessions' chat model and reads its thinking profile
	// and context window (#322). A zero policy offers no level and no window.
	Models ModelPolicy
	// RetiredModels are the generation models no longer offered and the model
	// that replaces each (Schema runtime.frontend.retired_generation_models,
	// #329 follow-up). A turn's pick of a retired model is moved to its
	// successor and the turn records the switch.
	RetiredModels []catalog.RetiredModelSuccessor
}

type Service struct {
	repository           Repository
	clock                func() time.Time
	newID                func(string) string
	defaultTitle         string
	userRole             string
	assistantRole        string
	pendingContent       string
	eventFields          EventFieldPolicy
	eventTypes           EventTypePolicy
	streamActive         string
	streamDone           string
	streamError          string
	turnQueued           string
	turnRunning          string
	turnWaitingTask      string
	turnDone             string
	turnError            string
	placeholderHealing   PlaceholderHealingPolicy
	streamCleanupTTL     time.Duration
	subscriberBufferSize int
	streamMu             sync.Mutex
	streamOperations     map[string]*streamOperation
	subscribers          map[string]map[chan Event]struct{}
	approvalPolicy       approvalpolicy.Policy
	agentScope           agentscope.Policy
	handles              HandlePolicy
	attachments          AttachmentVerifier
	maximumNotes         int
	canvasInputSource    string
	retiredModels        map[string]catalog.RetiredModelSuccessor

	harnessSnapshotResolver HarnessSnapshotResolver

	// choices keeps the session's in-chat choice cards (#321); choiceLimit
	// bounds one session read.
	choices     ChoiceStore
	choiceLimit int

	// models and contextStore serve the thinking level, the context usage
	// and the compactions of sessions (#322).
	models              ModelPolicy
	contextStore        ContextStore
	compactionListLimit int
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("chat: repository is required")
	}
	for name, value := range map[string]string{
		"default title":           config.DefaultTitle,
		"user role":               config.UserRole,
		"assistant role":          config.AssistantRole,
		"pending content":         config.PendingContent,
		"event type field":        config.EventFields.Type,
		"event message field":     config.EventFields.MessageID,
		"message-created event":   config.EventTypes.MessageCreated,
		"active stream state":     config.StreamActive,
		"done stream state":       config.StreamDone,
		"error stream state":      config.StreamError,
		"queued turn state":       config.TurnQueued,
		"running turn state":      config.TurnRunning,
		"waiting task turn state": config.TurnWaitingTask,
		"done turn state":         config.TurnDone,
		"error turn state":        config.TurnError,
		"healing prompt field":    config.PlaceholderHealing.PromptArgument,
		"interrupted message":     config.PlaceholderHealing.InterruptedMessage,
		"interrupted template":    config.PlaceholderHealing.InterruptedPromptTemplate,
	} {
		if value == "" {
			return nil, fmt.Errorf("chat: %s is required", name)
		}
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = randomID
	}
	if config.StreamCleanupTTL <= 0 {
		return nil, errors.New("chat: stream cleanup TTL must be positive")
	}
	if config.SubscriberBufferSize <= 0 {
		return nil, errors.New("chat: subscriber buffer size must be positive")
	}
	if config.PlaceholderHealing.StaleAfter <= 0 ||
		config.PlaceholderHealing.PromptPreviewCharacters <= 0 {
		return nil, errors.New(
			"chat: placeholder healing limits must be positive",
		)
	}
	if !strings.Contains(
		config.PlaceholderHealing.InterruptedPromptTemplate,
		"{prompt}",
	) {
		return nil, errors.New(
			"chat: interrupted prompt template requires {prompt}",
		)
	}
	return &Service{
		repository:           config.Repository,
		clock:                config.Clock,
		newID:                config.NewID,
		defaultTitle:         config.DefaultTitle,
		userRole:             config.UserRole,
		assistantRole:        config.AssistantRole,
		pendingContent:       config.PendingContent,
		eventFields:          config.EventFields,
		eventTypes:           config.EventTypes,
		streamActive:         config.StreamActive,
		streamDone:           config.StreamDone,
		streamError:          config.StreamError,
		turnQueued:           config.TurnQueued,
		turnRunning:          config.TurnRunning,
		turnWaitingTask:      config.TurnWaitingTask,
		turnDone:             config.TurnDone,
		turnError:            config.TurnError,
		placeholderHealing:   config.PlaceholderHealing,
		streamCleanupTTL:     config.StreamCleanupTTL,
		subscriberBufferSize: config.SubscriberBufferSize,
		subscribers:          map[string]map[chan Event]struct{}{},
		approvalPolicy:       config.ApprovalPolicy,
		agentScope:           config.AgentScope,
		handles:              config.Handles,
		attachments:          config.Attachments,
		maximumNotes:         config.MaximumSessionNotes,
		canvasInputSource:    config.CanvasInputSource,
		retiredModels:        retiredModelIndex(config.RetiredModels),
		models:               config.Models,
	}, nil
}

func (service *Service) ListSessions(
	ctx context.Context,
	ownerID string,
) ([]Session, error) {
	return service.repository.ListSessions(ctx, ownerID)
}

func (service *Service) ListSessionsPage(
	ctx context.Context,
	ownerID string,
	query SessionListQuery,
) (SessionPage, error) {
	if ownerID == "" || query.Limit < 1 || query.Limit > 100 || len(query.Search) > 200 {
		return SessionPage{}, ErrInvalidSessionPage
	}
	return service.repository.ListSessionsPage(ctx, ownerID, query)
}

func (service *Service) CreateSession(
	ctx context.Context,
	ownerID string,
	ownerName string,
	title string,
) (Session, error) {
	title = strings.TrimSpace(title)
	titleSource := TitleSourceUser
	if title == "" || title == service.defaultTitle {
		title = service.defaultTitle
		titleSource = TitleSourcePending
	}
	now := service.clock()
	return service.repository.CreateSession(ctx, Session{
		ID:          service.newID("session"),
		OwnerID:     ownerID,
		OwnerName:   ownerName,
		Title:       title,
		TitleSource: titleSource,
		CreatedAt:   now,
		UpdatedAt:   now,
		// Every new session starts in the Schema default mode.
		ApprovalMode: service.approvalPolicy.NormalizeMode(service.approvalPolicy.DefaultMode),
		// ... and in the Schema default Agent scope (auto).
		AgentScope: service.agentScope.Normalize(service.agentScope.DefaultMode),
	})
}

// SetAgentScope switches a session's Agent scope. The scope must be one the
// Schema offers. It returns the updated session and the previous scope so the
// caller can audit a real change. The scope applies to the turns that start
// afterwards; a turn already running keeps the scope it started with.
func (service *Service) SetAgentScope(
	ctx context.Context,
	ownerID string,
	sessionID string,
	scope string,
) (Session, string, error) {
	if !service.agentScope.Allows(scope) {
		return Session{}, "", ErrInvalidAgentScope
	}
	previous, found, err := service.repository.SessionAgentScope(ctx, ownerID, sessionID)
	if err != nil {
		return Session{}, "", err
	}
	if !found {
		return Session{}, "", ErrSessionNotFound
	}
	session, found, err := service.repository.SetSessionAgentScope(ctx, ownerID, sessionID, scope)
	if err != nil {
		return Session{}, "", err
	}
	if !found {
		return Session{}, "", ErrSessionNotFound
	}
	return session, service.agentScope.Normalize(previous), nil
}

// NormalizeAgentScope maps a stored scope to one the Schema offers.
func (service *Service) NormalizeAgentScope(scope string) string {
	return service.agentScope.Normalize(scope)
}

// AgentScopePolicy exposes the Schema scope policy to the orchestrator.
func (service *Service) AgentScopePolicy() agentscope.Policy {
	return service.agentScope
}

// SetApprovalMode switches a session's approval mode. The mode must be one the
// Schema offers. It returns the updated session and the previous mode so the
// caller can audit a real change.
func (service *Service) SetApprovalMode(
	ctx context.Context,
	ownerID string,
	sessionID string,
	mode string,
) (Session, string, error) {
	if !service.approvalPolicy.Allows(mode) {
		return Session{}, "", ErrInvalidApprovalMode
	}
	previous, found, err := service.repository.Session(ctx, ownerID, sessionID)
	if err != nil {
		return Session{}, "", err
	}
	if !found {
		return Session{}, "", ErrSessionNotFound
	}
	session, found, err := service.repository.SetSessionApprovalMode(ctx, ownerID, sessionID, mode)
	if err != nil {
		return Session{}, "", err
	}
	if !found {
		return Session{}, "", ErrSessionNotFound
	}
	return session, service.approvalPolicy.NormalizeMode(previous.ApprovalMode), nil
}

// SessionApprovalMode implements executiongrant.SessionModeReader. It is
// owner-scoped; an unknown or foreign session is an error, never a default.
func (service *Service) SessionApprovalMode(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (string, error) {
	session, found, err := service.repository.Session(ctx, ownerID, sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return service.approvalPolicy.NormalizeMode(session.ApprovalMode), nil
}

func (service *Service) RenameSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
	title string,
) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, ErrEmptyTitle
	}
	session, found, err := service.repository.RenameSession(
		ctx,
		ownerID,
		sessionID,
		title,
		service.clock(),
	)
	if err != nil {
		return Session{}, err
	}
	if !found {
		return Session{}, ErrSessionNotFound
	}
	return session, nil
}

// ApplyAutoTitle stores the automatic title unless the session is gone, is not
// the owner's, or its title was already set (by a person or an earlier
// summary). It reports whether the title was written.
func (service *Service) ApplyAutoTitle(
	ctx context.Context,
	ownerID string,
	sessionID string,
	title string,
) (bool, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return false, ErrEmptyTitle
	}
	return service.repository.ApplyAutoTitle(ctx, ownerID, sessionID, title)
}

func (service *Service) DeleteSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) error {
	deleted, err := service.repository.DeleteSession(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrSessionNotFound
	}
	return nil
}

// Session 读取单个会话（B16：标题限流需要校验当前标题仍是默认值）。
func (service *Service) Session(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (Session, bool, error) {
	return service.repository.Session(ctx, ownerID, sessionID)
}

// sessionModelTurnWindow is how many recent turns SessionModels reads; a
// domain the person has not sent in for that long is not restored.
const sessionModelTurnWindow = 20

// SessionModels returns, per workspace domain, the model the owner last sent
// with in this session (#318): the domain in view carries AIModel, the other
// domains DomainModels. Newer turns win per domain; a turn that chose no
// model for a domain leaves the older choice in place. A recorded retired
// model is returned as its successor, and retired names, per domain, the
// retired model that was recorded (#329 follow-up), so the workspace can say
// it switched.
func (service *Service) SessionModels(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (models map[string]string, retired map[string]string, err error) {
	recent, err := service.repository.RecentSessionTurnOptions(ctx, ownerID, sessionID, sessionModelTurnWindow)
	if err != nil {
		return nil, nil, err
	}
	models, retired = map[string]string{}, map[string]string{}
	remember := func(options TurnOptions, domain, model string) {
		if domain == "" || model == "" {
			return
		}
		if _, newer := models[domain]; newer {
			return
		}
		models[domain] = service.currentModel(model)
		if models[domain] != model {
			retired[domain] = model
		} else if switched, ok := options.RetiredModelFor(domain); ok && switched.To == model {
			// The turn named the retired model and was moved when it started.
			retired[domain] = switched.From
		}
	}
	for _, options := range recent {
		remember(options, options.CreativeDomain, options.AIModel)
		for domain, model := range options.DomainModels {
			if domain != options.CreativeDomain {
				remember(options, domain, model)
			}
		}
	}
	if len(models) == 0 {
		models = nil
	}
	if len(retired) == 0 {
		retired = nil
	}
	return models, retired, nil
}

func (service *Service) OwnsSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	return service.repository.OwnsSession(ctx, ownerID, sessionID)
}

func (service *Service) ListMessages(
	ctx context.Context,
	ownerID string,
	sessionID string,
) ([]Message, error) {
	messages, err := service.repository.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	return service.healPlaceholders(ctx, ownerID, sessionID, messages)
}

// ListMessagesPage reads one page of the conversation for the workspace. The
// page gets the same placeholder healing as ListMessages. Callers that need the
// whole conversation (Agent context, picks, attachments) use ListMessages.
func (service *Service) ListMessagesPage(
	ctx context.Context,
	ownerID string,
	sessionID string,
	query MessageListQuery,
) (MessagePage, error) {
	if ownerID == "" || query.Limit < 1 {
		return MessagePage{}, ErrInvalidMessagePage
	}
	page, err := service.repository.ListMessagesPage(ctx, ownerID, sessionID, query)
	if err != nil {
		return MessagePage{}, err
	}
	page.Messages, err = service.healPlaceholders(ctx, ownerID, sessionID, page.Messages)
	if err != nil {
		return MessagePage{}, err
	}
	return page, nil
}

// healPlaceholders replaces the pending placeholder of an assistant message
// whose turn is no longer active, so a reader never waits on a turn that ended.
func (service *Service) healPlaceholders(
	ctx context.Context,
	ownerID string,
	sessionID string,
	messages []Message,
) ([]Message, error) {
	turns, err := service.repository.ListSessionTurns(
		ctx,
		ownerID,
		sessionID,
		[]string{service.turnQueued, service.turnRunning, service.turnWaitingTask},
	)
	if err != nil {
		return nil, err
	}
	activeTurns := make(map[string]struct{}, len(turns))
	for _, turn := range turns {
		activeTurns[turn.AssistantMessageID] = struct{}{}
	}
	now := service.clock()
	for index := range messages {
		message := &messages[index]
		if message.Role != service.assistantRole ||
			message.Content != service.pendingContent {
			continue
		}
		if _, active := activeTurns[message.ID]; active {
			continue
		}
		replacement := ""
		if len(message.ImageURLs) == 0 {
			if now.Sub(message.CreatedAt) <=
				service.placeholderHealing.StaleAfter {
				continue
			}
			replacement = service.interruptedContent(*message)
		}
		message.Content = replacement
		updated, err := service.repository.UpdateMessage(
			ctx,
			ownerID,
			*message,
			now,
		)
		if err != nil {
			return nil, err
		}
		if !updated {
			return nil, ErrMessageNotFound
		}
	}
	return messages, nil
}

func (service *Service) interruptedContent(message Message) string {
	for _, call := range message.ToolCalls {
		var arguments map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			continue
		}
		var prompt string
		if err := json.Unmarshal(
			arguments[service.placeholderHealing.PromptArgument],
			&prompt,
		); err != nil {
			continue
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		prompt = truncateRunes(
			prompt,
			service.placeholderHealing.PromptPreviewCharacters,
		)
		return strings.ReplaceAll(
			service.placeholderHealing.InterruptedPromptTemplate,
			"{prompt}",
			prompt,
		)
	}
	return service.placeholderHealing.InterruptedMessage
}

type StartTurn struct {
	OwnerID   string
	SessionID string
	Content   string
	ImageURLs []string
	Options   TurnOptions
	// Attachments are the client's claims about the message's images (capture
	// lineage, asset and task IDs) and Selection the objects selected on the
	// workspace. Both are untrusted: the service verifies each against the
	// owner and session and drops what does not hold.
	Attachments []AttachmentRef
	Selection   []AttachmentRef
	// Mentions are the `@` references the person wrote in Content. Each names a
	// picture without a handle yet (a picture waiting in the message, a
	// generated version, a canvas layer); the service verifies the claim like
	// any other, gives its object a handle in the usual order and rewrites the
	// token in the stored text to that handle. Untrusted like the claims.
	Mentions []MentionRef
	// OnMentionsDropped, when set, receives the tokens that were left as plain
	// text because they were not written in Content or their claim did not hold.
	OnMentionsDropped func(tokens []string)
	// ChoiceAnswer answers an open choice card of the session with this turn
	// (#321). The card is checked against the owner and session and answered
	// once; the Agent then reads the exact next call from the card.
	ChoiceAnswer *ChoiceAnswer
}

func (service *Service) StartTurn(
	ctx context.Context,
	command StartTurn,
) (Message, error) {
	// Client-supplied execution context never becomes a durable turn snapshot.
	command.Options.Harness = nil
	command.Options.Selection = nil
	command.Options.SelectionPicked = false
	command.Content = strings.TrimSpace(command.Content)
	if command.Content == "" && len(command.ImageURLs) == 0 {
		return Message{}, ErrEmptyMessage
	}
	// The turn runs in the scope the stored session has now. The client cannot
	// name a scope for a turn: whatever Options carries is replaced.
	scope, found, err := service.repository.SessionAgentScope(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return Message{}, ErrSessionNotFound
	}
	command.Options.AgentScope = service.agentScope.Normalize(scope)
	// A pick of a retired generation model runs on its successor; the turn
	// records the switch (#329 follow-up).
	service.moveRetiredModels(&command.Options)
	// The turn's model and thinking level are frozen from the stored session,
	// never taken from the request (#322).
	// The model is the session's choice while the administrator offers it,
	// else the default (#322 Scope 5); the level falls back to what that
	// model offers.
	model, effort, err := service.TurnModel(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return Message{}, err
	}
	command.Options.Model = model.Model
	command.Options.ModelConnection = model.ID
	command.Options.ReasoningEffort = effort
	if command.Options.InputSource != "" && command.Options.InputSource != service.canvasInputSource {
		return Message{}, ErrInvalidInputSource
	}
	verified := service.verifyTurnAttachments(ctx, command)
	if command.OnMentionsDropped != nil && len(verified.droppedMentions) > 0 {
		command.OnMentionsDropped(verified.droppedMentions)
	}
	now := service.clock()
	user := Message{
		ID:        service.newID("message"),
		SessionID: command.SessionID,
		Role:      service.userRole,
		Content:   command.Content,
		ImageURLs: append([]string(nil), command.ImageURLs...),
		CreatedAt: now,
	}
	assistant := Message{
		ID:        service.newID("message"),
		SessionID: command.SessionID,
		Role:      service.assistantRole,
		Content:   service.pendingContent,
		CreatedAt: now,
	}
	event := Event{
		SessionID: command.SessionID,
		Data: map[string]any{
			service.eventFields.Type:      service.eventTypes.MessageCreated,
			service.eventFields.MessageID: assistant.ID,
		},
		CreatedAt: now,
	}
	unlock, err := service.lockStreamSession(ctx, command.SessionID)
	if err != nil {
		return Message{}, err
	}
	defer unlock()
	if err := service.applyTurnAttachments(ctx, command, verified, &user, &command.Options); err != nil {
		return Message{}, err
	}
	if command.Options.InputSource != "" && !canvasSnapshotSelected(command.Options) {
		// The person chose the canvas: without its verified snapshot the turn
		// does not run, rather than continue from another picture or none.
		return Message{}, ErrCanvasSourceMissing
	}
	command.Options.Harness, err = service.freezeHarnessSnapshot(ctx, command.OwnerID, command.SessionID, command.Options.Selection)
	if err != nil {
		return Message{}, err
	}
	answeredChoice := ""
	if command.ChoiceAnswer != nil {
		if err := service.answerChoice(ctx, command, user.ID, now); err != nil {
			return Message{}, err
		}
		answeredChoice = command.ChoiceAnswer.ChoiceID
	}
	stored, storedEvent, err := service.repository.StartTurn(
		ctx,
		command.OwnerID,
		user,
		assistant,
		event,
		Stream{
			SessionID:          command.SessionID,
			AssistantMessageID: assistant.ID,
			Status:             service.streamActive,
		},
		Turn{
			OwnerID:            command.OwnerID,
			SessionID:          command.SessionID,
			AssistantMessageID: assistant.ID,
			Options:            command.Options,
			State:              service.turnQueued,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	)
	if err == nil {
		service.streamMu.Lock()
		service.publishLocked(storedEvent)
		service.streamMu.Unlock()
	} else if answeredChoice != "" {
		// The turn did not start: the card stays answerable.
		_ = service.choices.ReopenChoice(ctx, command.OwnerID, command.SessionID, answeredChoice, user.ID)
	}
	return stored, err
}

// SetChoices installs the store of the in-chat choice cards (#321). Without
// it a turn that carries an answer is refused.
func (service *Service) SetChoices(store ChoiceStore, limit int) {
	service.choices, service.choiceLimit = store, limit
}

// ListChoices returns the session's cards, newest first. A missing or
// foreign session is ErrSessionNotFound.
func (service *Service) ListChoices(ctx context.Context, ownerID, sessionID string) ([]Choice, error) {
	if service.choices == nil {
		return nil, nil
	}
	owned, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrSessionNotFound
	}
	return service.choices.ListChoices(ctx, ownerID, sessionID, service.choiceLimit)
}

// answerChoice answers the card this turn carries, bound to the turn's user
// message. Only an open card of this owner and session, and only one of its
// options (or its default when it offers one), is accepted.
func (service *Service) answerChoice(ctx context.Context, command StartTurn, messageID string, now time.Time) error {
	answer := *command.ChoiceAnswer
	if service.choices == nil || strings.TrimSpace(answer.ChoiceID) == "" {
		return ErrChoiceRejected
	}
	choice, err := service.choices.GetChoice(ctx, command.OwnerID, command.SessionID, answer.ChoiceID)
	if err != nil {
		return err
	}
	option, err := choice.Resolve(answer)
	if err != nil {
		return err
	}
	_, err = service.choices.AnswerChoice(ctx, command.OwnerID, command.SessionID, choice.ID, ChoiceRecord{
		OptionID: option.ID, UseDefault: answer.UseDefault, Source: ChoiceAnsweredOnCard, MessageID: messageID, AnsweredAt: now})
	return err
}

// SetAttachmentVerifier installs the verifier of structured attachment claims.
// Application assembly calls it once, before the service takes requests,
// because the verifier needs the reference resolver that is built after it.
func (service *Service) SetAttachmentVerifier(verifier AttachmentVerifier) {
	service.attachments = verifier
}

func (service *Service) RecoverTurns(ctx context.Context) ([]Turn, error) {
	return service.repository.ListTurns(
		ctx,
		[]string{service.turnQueued, service.turnRunning},
	)
}

func (service *Service) GetTurn(ctx context.Context, ownerID, sessionID, assistantID string) (Turn, error) {
	return service.repository.GetTurn(ctx, ownerID, sessionID, assistantID)
}

func (service *Service) ListWakeCandidates(ctx context.Context, cursor TurnPageCursor, limit int) ([]Turn, error) {
	return service.repository.ListTurnsPage(ctx, []string{service.turnWaitingTask, service.turnQueued}, cursor, limit)
}

func (service *Service) SaveTurnCheckpoint(ctx context.Context, turn Turn, state string, checkpoint TurnCheckpoint, allowStopped bool) (int64, error) {
	return service.repository.CompareAndSwapTurnCheckpoint(ctx, turn.OwnerID, turn.SessionID,
		turn.AssistantMessageID, turn.CheckpointVersion, turn.State, state, checkpoint, allowStopped, service.clock())
}

func (service *Service) TurnWaitingTaskState() string { return service.turnWaitingTask }
func (service *Service) TurnQueuedState() string      { return service.turnQueued }
func (service *Service) TurnRunningState() string     { return service.turnRunning }

func (service *Service) RequestTurnStop(ctx context.Context, ownerID, sessionID string) (Turn, bool, error) {
	return service.repository.RequestTurnStop(ctx, ownerID, sessionID,
		[]string{service.turnQueued, service.turnRunning, service.turnWaitingTask}, service.clock())
}

func (service *Service) TurnStopRequested(ctx context.Context, ownerID, sessionID, assistantMessageID string) (bool, error) {
	return service.repository.TurnStopRequested(ctx, ownerID, sessionID, assistantMessageID)
}

func (service *Service) MarkTurnRunning(
	ctx context.Context,
	assistantMessageID string,
) error {
	return service.repository.UpdateTurnState(
		ctx,
		assistantMessageID,
		service.turnRunning,
		service.clock(),
	)
}

func (service *Service) FinishTurn(
	ctx context.Context,
	assistantMessageID string,
	failed bool,
) error {
	state := service.turnDone
	if failed {
		state = service.turnError
	}
	return service.repository.UpdateTurnState(
		ctx,
		assistantMessageID,
		state,
		service.clock(),
	)
}

func (service *Service) DeleteMessage(
	ctx context.Context,
	ownerID string,
	sessionID string,
	messageID string,
) error {
	sessionFound, deleted, err := service.repository.DeleteMessage(
		ctx,
		ownerID,
		sessionID,
		messageID,
	)
	if err != nil {
		return err
	}
	if !sessionFound {
		return ErrSessionNotFound
	}
	if !deleted {
		return ErrMessageNotFound
	}
	return nil
}

func (service *Service) UpdateMessage(
	ctx context.Context,
	ownerID string,
	message Message,
) error {
	found, err := service.repository.UpdateMessage(
		ctx,
		ownerID,
		message,
		service.clock(),
	)
	if err != nil {
		return err
	}
	if !found {
		return ErrMessageNotFound
	}
	return nil
}

// PostAssistantMessage adds an assistant message to an existing session
// without a turn: the late result of an approval decided after its turn ended.
// The ID is chosen by the caller so a retry cannot post it twice; created is
// false when the message already exists. The message belongs to the owner's
// session or the call fails with ErrSessionNotFound.
func (service *Service) PostAssistantMessage(
	ctx context.Context,
	ownerID string,
	message Message,
) (Message, bool, error) {
	if ownerID == "" || message.ID == "" || message.SessionID == "" {
		return Message{}, false, ErrMessageNotFound
	}
	message.Role = service.assistantRole
	if message.CreatedAt.IsZero() {
		message.CreatedAt = service.clock()
	}
	created, err := service.repository.InsertMessageIfAbsent(ctx, ownerID, message, service.clock())
	if err != nil {
		return Message{}, false, err
	}
	return message, created, nil
}

func (service *Service) AppendEvent(
	ctx context.Context,
	event Event,
) (Event, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = service.clock()
	}
	unlock, err := service.lockStreamSession(ctx, event.SessionID)
	if err != nil {
		return Event{}, err
	}
	defer unlock()
	stored, err := service.repository.AppendEvent(ctx, event)
	if err == nil {
		service.streamMu.Lock()
		service.publishLocked(stored)
		service.streamMu.Unlock()
	}
	return stored, err
}

func (service *Service) ListEvents(
	ctx context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
) ([]Event, error) {
	events, found, err := service.repository.ListEvents(
		ctx,
		ownerID,
		sessionID,
		fromID,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	return events, nil
}

func (service *Service) Subscribe(
	ctx context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
) (*Subscription, error) {
	unlock, err := service.lockStreamSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	stream, replay, sessionFound, streamFound, err :=
		service.repository.ReadStream(
			ctx,
			ownerID,
			sessionID,
			fromID,
			service.clock(),
		)
	if err != nil {
		return nil, err
	}
	if !sessionFound {
		return nil, ErrSessionNotFound
	}
	if !streamFound {
		return nil, ErrStreamNotFound
	}
	service.streamMu.Lock()
	defer service.streamMu.Unlock()
	events := make(chan Event, service.subscriberBufferSize)
	if service.subscribers[sessionID] == nil {
		service.subscribers[sessionID] = map[chan Event]struct{}{}
	}
	service.subscribers[sessionID][events] = struct{}{}
	subscription := &Subscription{
		Replay: replay,
		Events: events,
		Status: stream.Status,
	}
	subscription.close = func() {
		service.streamMu.Lock()
		defer service.streamMu.Unlock()
		if _, exists := service.subscribers[sessionID][events]; !exists {
			return
		}
		delete(service.subscribers[sessionID], events)
		if len(service.subscribers[sessionID]) == 0 {
			delete(service.subscribers, sessionID)
		}
		close(events)
	}
	return subscription, nil
}

func (service *Service) FinishStream(
	ctx context.Context,
	sessionID string,
	assistantMessageID string,
	failed bool,
) error {
	status := service.streamDone
	if failed {
		status = service.streamError
	}
	unlock, err := service.lockStreamSession(ctx, sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	return service.repository.FinishStream(
		ctx,
		sessionID,
		assistantMessageID,
		status,
		service.clock().Add(service.streamCleanupTTL),
	)
}

func (service *Service) publishLocked(event Event) {
	for subscriber := range service.subscribers[event.SessionID] {
		select {
		case subscriber <- event:
		default:
			// Persistence precedes publication. An overflowing client reconnects
			// using its cursor and replays every event from the repository.
			delete(service.subscribers[event.SessionID], subscriber)
			close(subscriber)
		}
	}
	if len(service.subscribers[event.SessionID]) == 0 {
		delete(service.subscribers, event.SessionID)
	}
}

// NormalizeApprovalMode maps a stored mode to one the Schema offers; anything
// else reads as manual, the stricter mode.
func (service *Service) NormalizeApprovalMode(mode string) string {
	return service.approvalPolicy.NormalizeMode(mode)
}

// canvasSnapshotSelected reports whether the turn's verified selection carries
// a picture the client sent with it, not the session pick standing in for an
// empty selection (#266).
func canvasSnapshotSelected(options TurnOptions) bool {
	if options.SelectionPicked {
		return false
	}
	for _, attachment := range options.Selection {
		if attachment.Kind == AttachmentKindImage && attachment.URL != "" {
			return true
		}
	}
	return false
}
