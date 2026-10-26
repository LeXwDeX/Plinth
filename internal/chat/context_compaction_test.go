package chat_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// memoryContextStore is the in-memory ContextStore of these tests; it keeps
// the same owner scoping and compare-and-set as the SQLite store.
type memoryContextStore struct {
	mu          sync.Mutex
	repository  *memoryChatRepository
	efforts     map[string]string
	usage       map[string]*chat.ContextUsage
	compactions map[string][]chat.Compaction
	models      map[string]string
}

func newMemoryContextStore(repository *memoryChatRepository) *memoryContextStore {
	return &memoryContextStore{repository: repository, efforts: map[string]string{},
		usage: map[string]*chat.ContextUsage{}, compactions: map[string][]chat.Compaction{}}
}

func (store *memoryContextStore) owns(owner, session string) bool {
	owned, _ := store.repository.OwnsSession(context.Background(), owner, session)
	return owned
}

func (store *memoryContextStore) SetSessionReasoningEffort(_ context.Context, owner, session, effort string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.owns(owner, session) {
		return false, nil
	}
	store.efforts[session] = effort
	return true, nil
}

func (store *memoryContextStore) SessionContext(_ context.Context, owner, session string) (chat.SessionContext, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.owns(owner, session) {
		return chat.SessionContext{}, false, nil
	}
	state := chat.SessionContext{ReasoningEffort: store.efforts[session], ChatModel: store.models[session]}
	if usage := store.usage[session]; usage != nil {
		copied := *usage
		state.Usage = &copied
	}
	if list := store.compactions[session]; len(list) > 0 {
		latest := list[len(list)-1]
		state.Latest = &latest
	}
	return state, true, nil
}

func (store *memoryContextStore) RecordContextUsage(_ context.Context, owner, session string, usage *chat.ContextUsage) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.owns(owner, session) {
		store.usage[session] = usage
	}
	return nil
}

func (store *memoryContextStore) SaveCompaction(_ context.Context, compaction chat.Compaction, previousID string) (chat.Compaction, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.owns(compaction.OwnerID, compaction.SessionID) {
		return chat.Compaction{}, chat.ErrSessionNotFound
	}
	list := store.compactions[compaction.SessionID]
	newest := ""
	if len(list) > 0 {
		newest = list[len(list)-1].ID
	}
	if newest != previousID {
		return chat.Compaction{}, chat.ErrCompactionConflict
	}
	compaction.ID = fmt.Sprintf("compaction-%d", len(list)+1)
	store.compactions[compaction.SessionID] = append(list, compaction)
	store.usage[compaction.SessionID] = nil
	return compaction, nil
}

func (store *memoryContextStore) ListCompactions(_ context.Context, owner, session string, limit int) ([]chat.Compaction, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.owns(owner, session) {
		return nil, nil
	}
	list := store.compactions[session]
	if len(list) > limit {
		list = list[len(list)-limit:]
	}
	return slices.Clone(list), nil
}

// contextAgent answers every turn with one line and the usage the test sets;
// it records the requests, and answers summary calls with summary (or fails).
type contextAgent struct {
	mu         sync.Mutex
	streams    []agents.Request
	summaries  []agents.Request
	usage      *agents.Usage
	summary    string
	summaryErr error
}

func (agent *contextAgent) Stream(_ context.Context, request agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	agent.streams = append(agent.streams, request)
	usage := agent.usage
	agent.mu.Unlock()
	if err := emit(agents.Delta{Content: "reply"}); err != nil {
		return agents.Completion{}, err
	}
	completion := agents.Completion{Content: "reply"}
	if usage != nil {
		copied := *usage
		completion.Usage = &copied
	}
	return completion, nil
}

func (agent *contextAgent) Complete(_ context.Context, request agents.Request) (agents.Completion, error) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if request.Purpose != agents.PurposeSummary {
		return agents.Completion{Content: "title"}, nil
	}
	agent.summaries = append(agent.summaries, request)
	if agent.summaryErr != nil {
		return agents.Completion{}, agent.summaryErr
	}
	return agents.Completion{Content: agent.summary}, nil
}

func (agent *contextAgent) lastStream() agents.Request {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.streams[len(agent.streams)-1]
}

type contextFixture struct {
	service      *chat.Service
	orchestrator *chat.Orchestrator
	agent        *contextAgent
	repository   *memoryChatRepository
	store        *memoryContextStore
	session      string
	schema       catalog.ChatSchema
}

func newContextFixture(t *testing.T, policy chat.ModelPolicy) *contextFixture {
	t.Helper()
	return newContextFixtureWith(t, policy, nil)
}

func newContextFixtureWith(t *testing.T, policy chat.ModelPolicy, mutate func(*catalog.ChatSchema)) *contextFixture {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Stream.EventTypes.Status = "configured-status"
	if mutate != nil {
		mutate(&schema)
	}
	repository := newMemoryChatRepository()
	if policy.Thinking == nil {
		policy.Thinking = schema.Agent.Thinking
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) { config.Models = policy })
	store := newMemoryContextStore(repository)
	service.SetContextStore(store, schema.ContextCompaction.ListLimit)
	agent := &contextAgent{summary: "configured summary @img1"}
	model := policy.Model
	if model == "" {
		model = "configured-model"
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: model, Agent: agent, Tools: scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	return &contextFixture{service: service, orchestrator: orchestrator, agent: agent, repository: repository,
		store: store, session: session.ID, schema: schema}
}

func (fixture *contextFixture) turn(t *testing.T, content string) chat.Message {
	t.Helper()
	ctx := context.Background()
	assistant, err := fixture.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: fixture.session, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: fixture.session,
		AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	fixture.orchestrator.WaitForTitles()
	return assistant
}

func requestText(request agents.Request) string {
	var text strings.Builder
	for _, message := range request.Messages {
		text.WriteString(message.Content)
		for _, part := range message.Parts {
			text.WriteString(part.Text)
		}
		text.WriteString("\n")
	}
	return text.String()
}

func TestSessionThinkingLevelFollowsTheModelProfileAndFreezesWithTheTurn(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek-flash"})
	view, err := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session)
	if err != nil || view.Effort != "low" || view.Default != "low" || !slices.Equal(view.Levels, []string{"low", "high", "max"}) {
		t.Fatalf("initial view = %+v err=%v", view, err)
	}
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "xhigh"); !errors.Is(err, chat.ErrInvalidReasoningEffort) {
		t.Fatalf("a level DeepSeek does not offer = %v", err)
	}
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-b", fixture.session, "max"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign write = %v", err)
	}
	if _, err := fixture.service.SessionReasoning(ctx, "owner-b", fixture.session); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign read = %v", err)
	}
	if view, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "max"); err != nil || view.Effort != "max" {
		t.Fatalf("set max = %+v err=%v", view, err)
	}
	fixture.turn(t, "draw")
	request := fixture.agent.lastStream()
	if request.Model != "deepseek-flash" || request.ReasoningEffort != "max" {
		t.Fatalf("turn request model=%q effort=%q", request.Model, request.ReasoningEffort)
	}
	turns, _ := fixture.repository.ListTurns(ctx, nil)
	for _, turn := range turns {
		if turn.Options.Model != "deepseek-flash" || turn.Options.ReasoningEffort != "max" {
			t.Fatalf("frozen turn options = %+v", turn.Options)
		}
	}
	// A level stored for another model reads, and is sent, as the default.
	fixture.store.efforts[fixture.session] = "xhigh"
	if view, _ := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session); view.Effort != "low" {
		t.Fatalf("level of another model = %+v", view)
	}
}

func TestModelWithoutProfileOffersNoThinkingLevel(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "gpt-4o"})
	view, err := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session)
	if err != nil || view.Effort != "" || len(view.Levels) != 0 {
		t.Fatalf("view = %+v err=%v", view, err)
	}
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "high"); !errors.Is(err, chat.ErrReasoningUnavailable) {
		t.Fatalf("set on a model without levels = %v", err)
	}
	fixture.turn(t, "draw")
	if request := fixture.agent.lastStream(); request.ReasoningEffort != "" {
		t.Fatalf("effort sent for a model without a profile: %q", request.ReasoningEffort)
	}
}

func TestTurnRecordsTheUsageOfItsLastRequest(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek-flash"})
	fixture.agent.usage = &agents.Usage{PromptTokens: 12000, CompletionTokens: 345}
	fixture.turn(t, "draw")
	view, err := fixture.service.SessionContextView(ctx, "owner-a", fixture.session)
	if err != nil || view.Tokens == nil || *view.Tokens != 12345 || view.Window != 1000000 {
		t.Fatalf("view = %+v err=%v", view, err)
	}
	// The upstream reported nothing for the newest request: unknown, never a guess.
	fixture.agent.usage = nil
	fixture.turn(t, "again")
	if view, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session); view.Tokens != nil {
		t.Fatalf("unreported usage = %d", *view.Tokens)
	}
	// A model without a declared window shows only the tokens used.
	other := newContextFixture(t, chat.ModelPolicy{Model: "qwen-plus"})
	if view, _ := other.service.SessionContextView(ctx, "owner-a", other.session); view.Window != 0 {
		t.Fatalf("qwen window = %d", view.Window)
	}
	// The deployment may override the window of its own model.
	small := newContextFixture(t, chat.ModelPolicy{Model: "deepseek", WindowOverride: 4000})
	if view, _ := small.service.SessionContextView(ctx, "owner-a", small.session); view.Window != 4000 {
		t.Fatalf("override window = %d", view.Window)
	}
}

func TestManualCompactionSummarizesAllButTheNewestRoundsAndKeepsTheHistory(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek-flash"})
	keep := fixture.schema.ContextCompaction.KeepRecentRounds
	for round := 1; round <= keep; round++ {
		fixture.turn(t, fmt.Sprintf("round %d", round))
	}
	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session); !errors.Is(err, chat.ErrNothingToCompact) {
		t.Fatalf("compaction of %d rounds = %v", keep, err)
	}
	fixture.turn(t, fmt.Sprintf("round %d", keep+1))
	fixture.turn(t, fmt.Sprintf("round %d", keep+2))
	fixture.agent.usage = &agents.Usage{PromptTokens: 900}
	fixture.turn(t, fmt.Sprintf("round %d", keep+3))
	before, _ := fixture.service.ListMessages(ctx, "owner-a", fixture.session)

	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-b", fixture.session); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign compaction = %v", err)
	}
	view, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Compactions) != 1 || view.Compactions[0].Rounds != 3 || view.Compactions[0].Trigger != chat.CompactionManual ||
		view.Compactions[0].ThroughMessageID != before[5].ID || view.Tokens != nil {
		t.Fatalf("view after compaction = %+v", view)
	}
	if len(fixture.agent.summaries) != 1 {
		t.Fatalf("summary calls = %d", len(fixture.agent.summaries))
	}
	summaryCall := fixture.agent.summaries[0]
	source := requestText(summaryCall)
	if summaryCall.Model != "deepseek-flash" || summaryCall.ReasoningEffort != "low" || len(summaryCall.Tools) != 0 ||
		!strings.Contains(source, "round 1") || !strings.Contains(source, "round 3") || strings.Contains(source, "round 4") ||
		!strings.Contains(summaryCall.Messages[0].Content, fixture.schema.ContextCompaction.SummaryPrompt) {
		t.Fatalf("summary call = %+v\n%s", summaryCall, source)
	}
	after, _ := fixture.service.ListMessages(ctx, "owner-a", fixture.session)
	if len(after) != len(before) {
		t.Fatalf("history changed: %d -> %d messages", len(before), len(after))
	}

	fixture.turn(t, "round next")
	request := fixture.agent.lastStream()
	text := requestText(request)
	system := request.Messages[0].Content
	if !strings.Contains(system, fixture.schema.ContextCompaction.OpenMarker+"\n") ||
		!strings.Contains(system, "configured summary @img1") {
		t.Fatalf("system prompt lacks the summary:\n%s", system)
	}
	for round := 1; round <= 3; round++ {
		if strings.Contains(text, fmt.Sprintf("round %d\n", round)) {
			t.Fatalf("compacted round %d was sent again:\n%s", round, text)
		}
	}
	for round := 4; round <= keep+3; round++ {
		if !strings.Contains(text, fmt.Sprintf("round %d", round)) {
			t.Fatalf("kept round %d missing:\n%s", round, text)
		}
	}
	// A second compaction merges the first summary and covers what it adds.
	fixture.turn(t, "round more")
	view, err = fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	second := requestText(fixture.agent.summaries[1])
	if len(view.Compactions) != 2 || view.Compactions[1].Rounds != 5 ||
		!strings.Contains(second, "configured summary @img1") || strings.Contains(second, "round 3") ||
		!strings.Contains(second, "round 4") || !strings.Contains(second, "round 5") || strings.Contains(second, "round 6") {
		t.Fatalf("second compaction = %+v\n%s", view.Compactions, second)
	}
}

func TestCompactionIsRefusedWhileATurnRunsAndLeavesTheSessionOnFailure(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek-flash"})
	for round := 1; round <= fixture.schema.ContextCompaction.KeepRecentRounds+2; round++ {
		fixture.turn(t, fmt.Sprintf("round %d", round))
	}
	fixture.agent.usage = &agents.Usage{PromptTokens: 777}
	fixture.turn(t, "last")
	fixture.agent.summaryErr = errors.New("upstream 503")
	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session); !errors.Is(err, chat.ErrCompactionFailed) {
		t.Fatalf("failed summary = %v", err)
	}
	view, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session)
	if len(view.Compactions) != 0 || view.Tokens == nil || *view.Tokens != 777 {
		t.Fatalf("session changed after a failed compaction: %+v", view)
	}
	fixture.agent.summaryErr = nil
	if _, err := fixture.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: fixture.session, Content: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session); !errors.Is(err, chat.ErrCompactionTurnActive) {
		t.Fatalf("compaction during a turn = %v", err)
	}
}

func TestAutomaticCompactionRunsBeforeATurnOverTheThreshold(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek", WindowOverride: 1000})
	keep := fixture.schema.ContextCompaction.KeepRecentRounds
	for round := 1; round <= keep+1; round++ {
		fixture.turn(t, fmt.Sprintf("round %d", round))
	}
	// Below 80% of the window: no compaction.
	fixture.agent.usage = &agents.Usage{PromptTokens: 700, CompletionTokens: 50}
	fixture.turn(t, fmt.Sprintf("round %d", keep+2))
	if len(fixture.agent.summaries) != 0 {
		t.Fatalf("compacted below the threshold")
	}
	fixture.agent.usage = &agents.Usage{PromptTokens: 790, CompletionTokens: 10}
	fixture.turn(t, fmt.Sprintf("round %d", keep+3))
	if len(fixture.agent.summaries) != 0 {
		t.Fatalf("compacted before the next turn")
	}
	fixture.agent.usage = &agents.Usage{PromptTokens: 300, CompletionTokens: 10}
	fixture.turn(t, "round after threshold")
	if len(fixture.agent.summaries) != 1 {
		t.Fatalf("summary calls = %d", len(fixture.agent.summaries))
	}
	view, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session)
	if len(view.Compactions) != 1 || view.Compactions[0].Trigger != chat.CompactionAuto || view.Compactions[0].Rounds != keep+3-keep ||
		view.Tokens == nil || *view.Tokens != 310 {
		t.Fatalf("view = %+v", view)
	}
	// The turn's own round was not summarized, and its request already used the summary.
	if source := requestText(fixture.agent.summaries[0]); strings.Contains(source, "round after threshold") {
		t.Fatalf("the current round was summarized:\n%s", source)
	}
	request := fixture.agent.lastStream()
	if text := requestText(request); strings.Contains(text, "round 1\n") || !strings.Contains(text, "round after threshold") ||
		!strings.Contains(request.Messages[0].Content, "configured summary") {
		t.Fatalf("request after auto compaction:\n%s", text)
	}
	events, _ := fixture.service.ListEvents(ctx, "owner-a", fixture.session, 0)
	statuses := []string{}
	for _, event := range events {
		if event.Data["configured-type"] == "configured-status" {
			statuses = append(statuses, fmt.Sprint(event.Data["configured-content"]))
		}
	}
	status := fixture.schema.Presentation.Status
	if !slices.Contains(statuses, status.Compacting) || !slices.Contains(statuses, status.Compacted) {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestFailedAutomaticCompactionKeepsTheTurnGoingWithTheFullContext(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek", WindowOverride: 1000})
	for round := 1; round <= fixture.schema.ContextCompaction.KeepRecentRounds+2; round++ {
		fixture.turn(t, fmt.Sprintf("round %d", round))
	}
	fixture.agent.usage = &agents.Usage{PromptTokens: 950}
	fixture.turn(t, "fill")
	fixture.agent.summaryErr = errors.New("upstream timeout")
	assistant := fixture.turn(t, "keep going")
	request := fixture.agent.lastStream()
	if text := requestText(request); !strings.Contains(text, "round 1") || !strings.Contains(text, "keep going") {
		t.Fatalf("turn after a failed compaction did not send the full context:\n%s", text)
	}
	messages, _ := fixture.service.ListMessages(ctx, "owner-a", fixture.session)
	if messages[len(messages)-1].ID != assistant.ID || messages[len(messages)-1].Content != "reply" {
		t.Fatalf("the turn did not finish normally: %+v", messages[len(messages)-1])
	}
	events, _ := fixture.service.ListEvents(ctx, "owner-a", fixture.session, 0)
	failed := false
	for _, event := range events {
		failed = failed || event.Data["configured-content"] == fixture.schema.Presentation.Status.CompactionFailed
	}
	if view, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session); !failed || len(view.Compactions) != 0 {
		t.Fatalf("failed status = %v, compactions = %+v", failed, view.Compactions)
	}
}

// openChoiceStore holds one open choice card of the session.
type openChoiceStore struct {
	chat.ChoiceStore
	choice chat.Choice
}

func (store openChoiceStore) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	return []chat.Choice{store.choice}, nil
}

// handleAgent calls the first tool with the handle the newest user message
// names, then answers in text; summary calls answer the configured summary.
type handleAgent struct {
	contextAgent
	tool string
}

func (agent *handleAgent) Stream(ctx context.Context, request agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	last := request.Messages[len(request.Messages)-1]
	if len(request.Tools) > 0 && last.Role != "configured-tool" && strings.Contains(last.Content, "@img1") {
		agent.mu.Lock()
		agent.streams = append(agent.streams, request)
		agent.mu.Unlock()
		return agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-handle", Name: agent.tool,
			Arguments: []byte(`{"prompt":"make it blue","handle":"@img1"}`)}}}, nil
	}
	return agent.contextAgent.Stream(ctx, request, emit)
}

// handleTools resolves the handle the call names against the history the
// orchestrator hands the tool, like the generation tools do.
type handleTools struct {
	policy   chat.HandlePolicy
	mu       sync.Mutex
	history  int
	resolved []chat.ResolvedAttachment
	err      error
}

func (tools *handleTools) Execute(_ context.Context, command chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	tools.mu.Lock()
	defer tools.mu.Unlock()
	tools.history = len(command.History)
	tools.resolved, tools.err = tools.policy.ResolveHandles(command.History, command.Options.Selection, []string{"@img1"}, chat.AttachmentKindImage)
	return chat.ToolResult{AgentContent: `{"success":true}`}, nil
}

// TestCompactionKeepsHandlesNotesAndChoicesWorking pins the #322 guarantee:
// compaction changes only the messages a request carries. A handle of a
// compacted message keeps its number, the model is told what it names when
// the person mentions it, and the tool still resolves it against the whole
// history; the session notes and the open choice card are still injected
// from their own stores.
func TestCompactionKeepsHandlesNotesAndChoicesWorking(t *testing.T) {
	ctx := context.Background()
	schema := configuredChatSchema(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Models = chat.ModelPolicy{Model: "deepseek", Thinking: schema.Agent.Thinking}
		config.Handles = chat.NewHandlePolicy(schema.Context)
		config.MaximumSessionNotes = 2000
	})
	service.SetContextStore(newMemoryContextStore(repository), schema.ContextCompaction.ListLimit)
	service.SetChoices(openChoiceStore{choice: chat.Choice{ID: "choice-open", ToolName: "retexture_model", Capability: "retexture",
		State: chat.ChoicePending, DefaultOptionID: "first", Options: []chat.ChoiceOption{
			{ID: "first", ProviderName: "P", ModelName: "One", Arguments: []byte(`{"offering_id":"first"}`)},
			{ID: "second", ProviderName: "P", ModelName: "Two", Arguments: []byte(`{"offering_id":"second"}`)},
		}}}, 20)
	agent := &handleAgent{contextAgent: contextAgent{summary: "older rounds summarized"}, tool: schema.Tools[0].Name}
	tools := &handleTools{policy: chat.NewHandlePolicy(schema.Context)}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "deepseek", Agent: agent, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	// The first round carries an uploaded picture with handle @img1.
	repository.mu.Lock()
	repository.messages[session.ID] = append(repository.messages[session.ID],
		chat.Message{ID: "seed-user", SessionID: session.ID, Role: "configured-user", Content: "a red cat",
			ImageURLs:   []string{"data:image/png;base64,AAAA"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "asset-cat", ImageSlot: 1}}},
		chat.Message{ID: "seed-assistant", SessionID: session.ID, Role: "configured-assistant", Content: "noted"},
	)
	repository.mu.Unlock()
	if _, err := service.SetSessionNotes(ctx, "owner-a", session.ID, "style: watercolor"); err != nil {
		t.Fatal(err)
	}
	run := func(content string) {
		t.Helper()
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: content})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
			t.Fatal(err)
		}
		orchestrator.WaitForTitles()
	}
	for round := 2; round <= schema.ContextCompaction.KeepRecentRounds+2; round++ {
		run(fmt.Sprintf("round %d", round))
	}
	view, err := orchestrator.CompactContext(ctx, "owner-a", session.ID)
	if err != nil || len(view.Compactions) != 1 {
		t.Fatalf("compaction = %+v err=%v", view, err)
	}
	// The summary call saw the handle label of the compacted picture.
	if source := requestText(agent.summaries[0]); !strings.Contains(source, "@img1") {
		t.Fatalf("transcript lost the handle:\n%s", source)
	}
	history, _ := service.ListMessages(ctx, "owner-a", session.ID)
	run("@img1 make it blue")
	request := agent.lastStream()
	system := request.Messages[0].Content
	if strings.Contains(requestText(request), "a red cat") {
		t.Fatalf("the compacted round was sent again")
	}
	compactedLabel := strings.ReplaceAll(strings.ReplaceAll(schema.Context.AttachmentLabelTemplates["image.upload"], "{index}", "1"), "{handle}", "@img1")
	if !strings.Contains(system, compactedLabel) || !strings.Contains(system, "older rounds summarized") ||
		!strings.Contains(system, "style: watercolor") || !strings.Contains(system, `"retexture_model"`) {
		t.Fatalf("system prompt after compaction:\n%s", system)
	}
	tools.mu.Lock()
	defer tools.mu.Unlock()
	if tools.err != nil || len(tools.resolved) != 1 || tools.resolved[0].Attachment.AssetID != "asset-cat" ||
		tools.history != len(history)+1 {
		t.Fatalf("tool handle resolution = %+v err=%v history=%d (session %d)", tools.resolved, tools.err, tools.history, len(history))
	}
	// A later picture still gets the next number: handles are counted over
	// the whole history, which compaction never shortens.
	history, _ = service.ListMessages(ctx, "owner-a", session.ID)
	if next := chat.NewHandlePolicy(schema.Context).Assign(history, []chat.Attachment{{Kind: chat.AttachmentKindImage}}); next[0].Handle != "@img2" {
		t.Fatalf("next handle after compaction = %q", next[0].Handle)
	}
}
