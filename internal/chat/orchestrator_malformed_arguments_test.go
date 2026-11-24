package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #378: a model that writes a tool call's arguments as invalid JSON used to
// hang the turn for good. The raw bytes went into the turn checkpoint, the
// checkpoint could not be encoded, and the save-failure branch suspended the
// turn: no terminal event, an open stream, a session stuck on "still busy".
// These tests run on the real SQLite repository, which encodes the
// checkpoint exactly as production does (the in-memory test repository never
// encodes anything).

// A stray closing brace after the object, as in the production logs, with a
// text that must never reach a checkpoint, a message or a log line.
const leakyArguments = `{"prompt":"leaky secret prompt"}}`

func malformedToolCall(id, tool string) agents.ToolCall {
	return agents.ToolCall{ID: id, Name: tool, Arguments: json.RawMessage(leakyArguments)}
}

// checkpointFaultRepository is the SQLite repository with a fault injected
// into the turn checkpoint write.
type checkpointFaultRepository struct {
	*sqlite.Repository
	mu    sync.Mutex
	fault func(checkpoint chat.TurnCheckpoint) error
}

func (r *checkpointFaultRepository) setFault(fault func(chat.TurnCheckpoint) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fault = fault
}

func (r *checkpointFaultRepository) CompareAndSwapTurnCheckpoint(ctx context.Context,
	owner, session, assistant string, version int64, oldState, newState string,
	checkpoint chat.TurnCheckpoint, allowStopped bool, updated time.Time,
) (int64, error) {
	r.mu.Lock()
	fault := r.fault
	r.mu.Unlock()
	if fault != nil {
		if err := fault(checkpoint); err != nil {
			return 0, err
		}
	}
	return r.Repository.CompareAndSwapTurnCheckpoint(ctx, owner, session, assistant,
		version, oldState, newState, checkpoint, allowStopped, updated)
}

// hungTurnLogs is a log sink safe for the title goroutine and the worker.
type hungTurnLogs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (l *hungTurnLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.Write(p)
}

func (l *hungTurnLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

type hungTurn struct {
	t       *testing.T
	repo    *checkpointFaultRepository
	service *chat.Service
	schema  catalog.ChatSchema
	logs    *hungTurnLogs
	texts   *catalog.TextLocalizer
	session chat.Session
	// configuration, when set, reads the Agent configuration snapshot a new
	// turn freezes before it does anything else.
	configuration func(context.Context, string) (chat.AgentConfigurationSnapshot, error)
}

// configuredRejectionTexts gives the two tool results of #378 texts no code
// can hardcode.
func configuredRejectionTexts(schema *catalog.ChatSchema) {
	schema.Presentation.Messages.ToolArgumentsInvalid = "configured arguments invalid"
	schema.Presentation.Messages.ToolUnavailable = "configured unavailable {tool}"
}

func newHungTurn(t *testing.T, adjust func(*catalog.ChatSchema)) *hungTurn {
	t.Helper()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := &checkpointFaultRepository{Repository: repository}
	t.Cleanup(func() { _ = repo.Close() })
	service := newChatService(t, repo)
	schema := configuredChatSchema(t)
	if adjust != nil {
		adjust(&schema)
	}
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	return &hungTurn{t: t, repo: repo, service: service, schema: schema, logs: &hungTurnLogs{}, session: session}
}

// start adds a user message and the turn that answers it.
func (h *hungTurn) start(options ...chat.TurnOptions) chat.ProcessTurn {
	h.t.Helper()
	command := chat.StartTurn{OwnerID: "owner-a", SessionID: h.session.ID, Content: "draw"}
	if len(options) > 0 {
		command.Options = options[0]
	}
	assistant, err := h.service.StartTurn(context.Background(), command)
	if err != nil {
		h.t.Fatal(err)
	}
	return chat.ProcessTurn{OwnerID: "owner-a", SessionID: h.session.ID, AssistantMessageID: assistant.ID}
}

// process runs the turn once on a new Orchestrator, as a worker (or a
// restarted process) would.
func (h *hungTurn) process(command chat.ProcessTurn, agent agents.Adapter, tools chat.ToolExecutor) error {
	h.t.Helper()
	return h.processWith(context.Background(), command, agent, tools)
}

// processWith is process under a context the test controls (a shutdown).
func (h *hungTurn) processWith(ctx context.Context, command chat.ProcessTurn, agent agents.Adapter, tools chat.ToolExecutor) error {
	h.t.Helper()
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: h.service, Schema: h.schema, Model: "configured-model", Agent: agent, Tools: tools,
		Logger:        slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Texts:         h.texts,
		Configuration: h.configuration,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	defer orchestrator.WaitForTitles()
	return orchestrator.Process(ctx, command)
}

func (h *hungTurn) run(agent agents.Adapter, tools chat.ToolExecutor) (chat.ProcessTurn, error) {
	h.t.Helper()
	command := h.start()
	return command, h.process(command, agent, tools)
}

func (h *hungTurn) turn(command chat.ProcessTurn) chat.Turn {
	h.t.Helper()
	return storedEndReasonTurn(h.t, h.service, command)
}

// stream reads what a client that connects now sees: the stream status and
// whether the replay holds the event that makes the SSE handler close.
func (h *hungTurn) stream(command chat.ProcessTurn) (status string, closed bool) {
	h.t.Helper()
	subscription, err := h.service.Subscribe(context.Background(), command.OwnerID, command.SessionID, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	defer subscription.Close()
	for _, event := range subscription.Replay {
		if event.Data[h.schema.Stream.EventFields.Type] == h.schema.Stream.EventTypes.Done {
			closed = true
		}
	}
	return subscription.Status, closed
}

func (h *hungTurn) assistant(command chat.ProcessTurn) chat.Message {
	h.t.Helper()
	messages, err := h.service.ListMessages(context.Background(), command.OwnerID, command.SessionID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == command.AssistantMessageID {
			return message
		}
	}
	h.t.Fatalf("assistant message %s not found", command.AssistantMessageID)
	return chat.Message{}
}

// wantSessionOpen is the user-visible symptom of the bug: after the turn the
// session accepts the next message instead of answering "still busy".
func (h *hungTurn) wantSessionOpen() {
	h.t.Helper()
	if _, err := h.service.StartTurn(context.Background(),
		chat.StartTurn{OwnerID: "owner-a", SessionID: h.session.ID, Content: "and again"}); err != nil {
		h.t.Fatalf("the session did not take the next message: %v", err)
	}
}

// toolResults are the tool messages that close a request, in order.
func toolResults(schema catalog.ChatSchema, request agents.Request) []agents.Message {
	var results []agents.Message
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role != schema.Roles.Tool {
			break
		}
		results = append([]agents.Message{request.Messages[index]}, results...)
	}
	return results
}

func TestMalformedToolArgumentsReachTheModelAsAToolError(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	tool := h.schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{malformedToolCall("raw-1", tool), budgetCall("raw-2", tool, "one")}},
		{ToolCalls: []agents.ToolCall{budgetCall("raw-3", tool, "two")}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	command, err := h.run(agent, tools)
	if err != nil {
		t.Fatalf("Process = %v", err)
	}
	// Only the well-formed calls ran, and the model was asked again.
	if executed := drainPrompts(tools); executed != "one,two" {
		t.Fatalf("executed = %q", executed)
	}
	if len(agent.requests) != 3 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	followUp := agent.requests[1]
	results := toolResults(h.schema, followUp)
	if len(results) != 2 ||
		results[0].ToolCallID != command.AssistantMessageID+"_tool_0" || results[0].Content != "configured arguments invalid" ||
		results[1].ToolCallID != command.AssistantMessageID+"_tool_1" || results[1].Content != `{}` {
		t.Fatalf("tool results of the follow-up = %#v", results)
	}
	// The model's own call is echoed with an empty object, never the raw text.
	echoed := followUp.Messages[len(followUp.Messages)-3]
	if len(echoed.ToolCalls) != 2 || string(echoed.ToolCalls[0].Arguments) != `{}` {
		t.Fatalf("echoed assistant message = %#v", echoed)
	}
	// The turn ended normally: done, not failed, stream closed, session usable.
	wantTurnEnd(t, h.turn(command), "configured-turn-done", configuredEndReasons.Completed)
	if status, closed := h.stream(command); status != "configured-done" || !closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	stored := h.assistant(command)
	if !strings.Contains(stored.Content, "configured reply") || len(stored.ToolCalls) != 3 ||
		string(stored.ToolCalls[0].Arguments) != `{}` ||
		string(stored.ToolCalls[1].Arguments) != `{"prompt":"one"}` {
		t.Fatalf("stored reply = %#v", stored)
	}
	// The raw text is in no checkpoint, no stored message and no log line,
	// but the log tells which call it was.
	encoded, err := json.Marshal([]any{h.turn(command).Checkpoint, stored})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "leaky secret") || strings.Contains(h.logs.String(), "leaky secret") {
		t.Fatalf("malformed arguments leaked: %s / %s", encoded, h.logs.String())
	}
	for _, want := range []string{"level=WARN", "session_id=" + h.session.ID,
		"assistant_message_id=" + command.AssistantMessageID, "call_id=" + command.AssistantMessageID + "_tool_0", "tool=" + tool} {
		if !strings.Contains(h.logs.String(), want) {
			t.Fatalf("log lacks %q: %s", want, h.logs.String())
		}
	}
	h.wantSessionOpen()
}

// No arguments at all is not broken arguments: an empty or blank value (a tool
// with no parameters, a gateway that sends "") reaches the tool as before,
// and the checkpoint still encodes it (an empty value that is not nil does
// not, as JSON).
func TestEmptyToolArgumentsAreNotMalformed(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	tool := h.schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{
			{ID: "raw-1", Name: tool, Arguments: json.RawMessage("")},
			{ID: "raw-2", Name: tool, Arguments: json.RawMessage("  \n")},
			{ID: "raw-3", Name: tool},
		}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	command, err := h.run(agent, tools)
	if err != nil {
		t.Fatalf("Process = %v", err)
	}
	close(tools.commands)
	ran := 0
	for executed := range tools.commands {
		if len(executed.Call.Arguments) != 0 {
			t.Fatalf("call %s reached the tool with arguments %q", executed.Call.ID, executed.Call.Arguments)
		}
		ran++
	}
	if ran != 3 {
		t.Fatalf("%d of 3 calls ran", ran)
	}
	wantTurnEnd(t, h.turn(command), "configured-turn-done", configuredEndReasons.Completed)
	if strings.Contains(h.logs.String(), "level=WARN") {
		t.Fatalf("empty arguments were reported as malformed: %s", h.logs.String())
	}
}

// A model that keeps writing broken arguments gets each call answered with the
// error, like any other call, until it stops calling tools; there is no retry
// loop of its own and, since #553, no tool call limit that cuts it short.
func TestMalformedToolArgumentsAreAnsweredUntilTheModelStops(t *testing.T) {
	h := newHungTurn(t, func(schema *catalog.ChatSchema) {
		configuredRejectionTexts(schema)
	})
	tool := h.schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{malformedToolCall("raw-1", tool)}},
		{ToolCalls: []agents.ToolCall{malformedToolCall("raw-2", tool)}},
		{ToolCalls: []agents.ToolCall{malformedToolCall("raw-3", tool)}},
		{Content: "configured summary"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	command, err := h.run(agent, tools)
	if err != nil {
		t.Fatalf("Process = %v", err)
	}
	if executed := drainPrompts(tools); executed != "" {
		t.Fatalf("a call with broken arguments ran: %q", executed)
	}
	// Three answered calls, each followed by a request that keeps the tools.
	if len(agent.requests) != 4 || len(agent.requests[3].Tools) == 0 {
		t.Fatalf("agent requests = %d, last request tools = %d", len(agent.requests), len(agent.requests[len(agent.requests)-1].Tools))
	}
	wantTurnEnd(t, h.turn(command), "configured-turn-done", configuredEndReasons.Completed)
	if status, closed := h.stream(command); status != "configured-done" || !closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	stored := h.assistant(command)
	if !strings.Contains(stored.Content, "configured summary") || len(stored.ToolCalls) != 3 {
		t.Fatalf("stored reply = %#v", stored)
	}
	h.wantSessionOpen()
}

// The broken call is remembered with the round's checkpoint: a worker that
// picks the turn up after a restart still answers it with the error instead
// of running the tool on the empty object that replaced its arguments.
func TestMalformedToolArgumentsAreStillAnsweredAfterARestart(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	tool := h.schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{malformedToolCall("raw-1", tool)}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	// The process dies once the round is stored: the answer's write is still
	// attempted (it does not follow the cancelled context) and the shutdown
	// keeps the turn where the restart finds it.
	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	writes := 0
	h.repo.setFault(func(chat.TurnCheckpoint) error {
		writes++
		if writes == 2 {
			shutdown()
			return errors.New("process died")
		}
		return nil
	})
	command := h.start()
	if err := h.processWith(ctx, command, agent, tools); err != nil {
		t.Fatalf("the shutdown reported a failure: %v", err)
	}
	interrupted := h.turn(command)
	callID := command.AssistantMessageID + "_tool_0"
	if interrupted.State != "configured-turn-running" || len(interrupted.Checkpoint.MalformedCalls) != 1 ||
		interrupted.Checkpoint.MalformedCalls[0] != callID {
		t.Fatalf("interrupted turn = state %q, malformed calls %v", interrupted.State, interrupted.Checkpoint.MalformedCalls)
	}
	h.repo.setFault(nil)
	if err := h.process(command, agent, tools); err != nil {
		t.Fatalf("resumed Process = %v", err)
	}
	if executed := drainPrompts(tools); executed != "" {
		t.Fatalf("the resumed round ran the tool: %q", executed)
	}
	results := toolResults(h.schema, agent.requests[len(agent.requests)-1])
	if len(results) != 1 || results[0].ToolCallID != callID || results[0].Content != "configured arguments invalid" {
		t.Fatalf("tool results after the restart = %#v", results)
	}
	wantTurnEnd(t, h.turn(command), "configured-turn-done", configuredEndReasons.Completed)
	if status, closed := h.stream(command); status != "configured-done" || !closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
}

// Both tool results are Schema copy in the turn's UI locale, and neither one
// fails the turn: the model reads it, corrects itself, and the reply ends as
// an ordinary one.
func TestToolRejectionTextsComeFromTheSchemaInTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	messages := application.Chat.Presentation.Messages
	english := application.Localization.Bundles["en"].Fields
	for _, test := range []struct {
		name, locale string
		call         func(tool string) agents.ToolCall
		want         string
	}{
		{"invalid arguments, default locale", "", func(tool string) agents.ToolCall { return malformedToolCall("raw-1", tool) },
			messages.ToolArgumentsInvalid},
		{"invalid arguments, English", "en", func(tool string) agents.ToolCall { return malformedToolCall("raw-1", tool) },
			english["chat.presentation.messages.tool_arguments_invalid"]},
		{"unavailable tool, default locale", "", func(string) agents.ToolCall { return budgetCall("raw-1", "no_such_tool", "x") },
			strings.ReplaceAll(messages.ToolUnavailable, "{tool}", "no_such_tool")},
		{"unavailable tool, English", "en", func(string) agents.ToolCall { return budgetCall("raw-1", "no_such_tool", "x") },
			strings.ReplaceAll(english["chat.presentation.messages.tool_unavailable"], "{tool}", "no_such_tool")},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHungTurn(t, nil)
			h.texts = texts
			if test.want == "" || strings.Contains(test.want, "{") {
				t.Fatalf("the default Catalog has no usable text: %q", test.want)
			}
			agent := &scriptedAgent{responses: []agents.Completion{
				{ToolCalls: []agents.ToolCall{test.call(h.schema.Tools[0].Name)}},
				{Content: "configured reply"},
			}}
			tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
			command := h.start(chat.TurnOptions{Locale: test.locale})
			if err := h.process(command, agent, tools); err != nil {
				t.Fatalf("Process = %v", err)
			}
			if executed := drainPrompts(tools); executed != "" {
				t.Fatalf("the refused call ran: %q", executed)
			}
			results := toolResults(h.schema, agent.requests[len(agent.requests)-1])
			if len(results) != 1 || results[0].Content != test.want {
				t.Fatalf("tool results = %#v, want %q", results, test.want)
			}
			// The call was refused by the server, not failed by the tool: the
			// turn is an ordinary completed one.
			wantTurnEnd(t, h.turn(command), "configured-turn-done", configuredEndReasons.Completed)
			if status, closed := h.stream(command); status != "configured-done" || !closed {
				t.Fatalf("stream status = %q, terminal event = %v", status, closed)
			}
			if strings.Contains(h.assistant(command).Content, h.schema.Presentation.Messages.UnknownError) {
				t.Fatalf("the reply reports an error: %q", h.assistant(command).Content)
			}
			h.wantSessionOpen()
		})
	}
}

// A checkpoint that cannot be saved ends the turn as failed, with the
// terminal event that closes the stream, and says why in the log. Whether the
// bytes cannot be encoded or the store refuses them, nothing in the live
// process would ever retry the write.
func TestCheckpointSaveFailureEndsTheTurnAsFailed(t *testing.T) {
	_, encodeErr := json.Marshal(struct{ Raw json.RawMessage }{Raw: json.RawMessage(`{`)})
	if encodeErr == nil {
		t.Fatal("the encoding fault did not fail")
	}
	for name, fault := range map[string]error{
		"the checkpoint cannot be encoded": encodeErr,
		"the store refuses the write":      errors.New("database is locked"),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHungTurn(t, configuredRejectionTexts)
			tool := h.schema.Tools[0].Name
			h.repo.setFault(func(checkpoint chat.TurnCheckpoint) error {
				if len(checkpoint.Completion.ToolCalls) > 0 && len(checkpoint.Completed) == 0 {
					return fault
				}
				return nil
			})
			agent := &scriptedAgent{responses: []agents.Completion{
				{ToolCalls: []agents.ToolCall{budgetCall("raw-1", tool, "one")}},
			}}
			tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
			command, err := h.run(agent, tools)
			if err != nil {
				t.Fatalf("Process = %v; the failure is handled, not left for the worker", err)
			}
			if executed := drainPrompts(tools); executed != "" {
				t.Fatalf("a call of the unsaved round ran: %q", executed)
			}
			wantTurnEnd(t, h.turn(command), "configured-turn-error", configuredEndReasons.Error)
			if status, closed := h.stream(command); status != "configured-error" || !closed {
				t.Fatalf("stream status = %q, terminal event = %v", status, closed)
			}
			stored := h.assistant(command)
			if !strings.Contains(stored.Content, h.schema.Presentation.Messages.UnknownError) ||
				!strings.Contains(stored.Content, command.AssistantMessageID) || len(stored.ToolCalls) != 0 {
				t.Fatalf("stored reply = %#v", stored)
			}
			for _, want := range []string{"level=ERROR", "session_id=" + h.session.ID,
				"assistant_message_id=" + command.AssistantMessageID, fault.Error()} {
				if !strings.Contains(h.logs.String(), want) {
					t.Fatalf("log lacks %q: %s", want, h.logs.String())
				}
			}
			h.wantSessionOpen()
		})
	}
}

// A write that lost to another writer is the other writer's turn now: the
// turn is left as it is for that writer, or for the restart recovery.
func TestCheckpointConflictLeavesTheTurnToItsOtherWriter(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	tool := h.schema.Tools[0].Name
	h.repo.setFault(func(checkpoint chat.TurnCheckpoint) error {
		if len(checkpoint.Completion.ToolCalls) > 0 {
			return chat.ErrTurnCheckpointConflict
		}
		return nil
	})
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("raw-1", tool, "one")}},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	command, err := h.run(agent, tools)
	if !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("Process = %v", err)
	}
	if state := h.turn(command).State; state != "configured-turn-running" {
		t.Fatalf("turn state = %q", state)
	}
	if status, closed := h.stream(command); status != "configured-active" || closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
}

// The Agent configuration snapshot is read and saved before the turn does
// anything else. When that fails the turn ends like any other that cannot be
// saved: failed, with the terminal event that closes the stream, an error in
// the log, and a session that takes the next message. The model is never
// asked: there is nothing it could have been told.
func TestConfigurationSnapshotFailureEndsTheTurnAsFailed(t *testing.T) {
	validSnapshot := func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
		return chat.AgentConfigurationSnapshot{Revision: "1", SystemPrompt: "frozen prompt"}, nil
	}
	for name, test := range map[string]struct {
		setup func(h *hungTurn)
		fault string
	}{
		"the snapshot cannot be read": {
			fault: "configuration store unavailable",
			setup: func(h *hungTurn) {
				h.configuration = func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
					return chat.AgentConfigurationSnapshot{}, errors.New("configuration store unavailable")
				}
			},
		},
		"the snapshot cannot be saved": {
			fault: "database is locked",
			setup: func(h *hungTurn) {
				h.configuration = validSnapshot
				h.repo.setFault(func(checkpoint chat.TurnCheckpoint) error {
					if checkpoint.Configuration != nil {
						return errors.New("database is locked")
					}
					return nil
				})
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHungTurn(t, configuredRejectionTexts)
			test.setup(h)
			agent := &scriptedAgent{}
			tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
			command, err := h.run(agent, tools)
			if err != nil {
				t.Fatalf("Process = %v; the failure is handled, not left for the worker", err)
			}
			if len(agent.requests) != 0 || drainPrompts(tools) != "" {
				t.Fatalf("the turn went on after its snapshot failed: %d requests", len(agent.requests))
			}
			wantTurnEnd(t, h.turn(command), "configured-turn-error", configuredEndReasons.Error)
			if status, closed := h.stream(command); status != "configured-error" || !closed {
				t.Fatalf("stream status = %q, terminal event = %v", status, closed)
			}
			stored := h.assistant(command)
			if !strings.Contains(stored.Content, h.schema.Presentation.Messages.UnknownError) ||
				!strings.Contains(stored.Content, command.AssistantMessageID) {
				t.Fatalf("stored reply = %#v", stored)
			}
			for _, want := range []string{"level=ERROR", "session_id=" + h.session.ID,
				"assistant_message_id=" + command.AssistantMessageID, test.fault} {
				if !strings.Contains(h.logs.String(), want) {
					t.Fatalf("log lacks %q: %s", want, h.logs.String())
				}
			}
			h.wantSessionOpen()
		})
	}
}

// A snapshot write that lost to another writer is that writer's turn now, as
// with the round's checkpoint.
func TestConfigurationSnapshotConflictLeavesTheTurnToItsOtherWriter(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	h.configuration = func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
		return chat.AgentConfigurationSnapshot{Revision: "1", SystemPrompt: "frozen prompt"}, nil
	}
	h.repo.setFault(func(checkpoint chat.TurnCheckpoint) error {
		if checkpoint.Configuration != nil {
			return chat.ErrTurnCheckpointConflict
		}
		return nil
	})
	command, err := h.run(&scriptedAgent{}, &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)})
	if !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("Process = %v", err)
	}
	if state := h.turn(command).State; state != "configured-turn-running" {
		t.Fatalf("turn state = %q", state)
	}
	if status, closed := h.stream(command); status != "configured-active" || closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
}

// A process shutdown is not a failure of the turn: it stays where the restart
// recovery finds it, with its stream open, and nothing is logged as an error.
func TestConfigurationSnapshotReadInterruptedByShutdownKeepsTheTurnRecoverable(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	h.configuration = func(ctx context.Context, _ string) (chat.AgentConfigurationSnapshot, error) {
		shutdown()
		return chat.AgentConfigurationSnapshot{}, ctx.Err()
	}
	command := h.start()
	if err := h.processWith(ctx, command, &scriptedAgent{}, &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}); err != nil {
		t.Fatalf("Process = %v", err)
	}
	if state := h.turn(command).State; state != "configured-turn-running" {
		t.Fatalf("turn state = %q", state)
	}
	if status, closed := h.stream(command); status != "configured-active" || closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	if strings.Contains(h.logs.String(), "level=ERROR") {
		t.Fatalf("a shutdown was logged as an error: %s", h.logs.String())
	}
	pending, err := h.service.RecoverTurns(context.Background())
	if err != nil || len(pending) != 1 || pending[0].AssistantMessageID != command.AssistantMessageID {
		t.Fatalf("recoverable turns = %+v, %v", pending, err)
	}
}
