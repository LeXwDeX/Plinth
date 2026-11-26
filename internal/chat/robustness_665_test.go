package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// panickingToolExecutor stands for a tool or executor bug (#665).
type panickingToolExecutor struct{}

func (panickingToolExecutor) Execute(context.Context, chat.ToolCommand,
	func(chat.ToolObservation) error) (chat.ToolResult, error) {
	panic("tool bug")
}

// processOnce starts one turn and processes it, returning the Process error.
func processOnce(t *testing.T, service *chat.Service, schema catalog.ChatSchema,
	agent agents.Adapter, tools chat.ToolExecutor) (chat.ProcessTurn, error) {
	t.Helper()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	return command, orchestrator.Process(ctx, command)
}

func storedReply(t *testing.T, service *chat.Service, command chat.ProcessTurn) chat.Message {
	t.Helper()
	messages, err := service.ListMessages(context.Background(), command.OwnerID, command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == command.AssistantMessageID {
			return message
		}
	}
	t.Fatal("reply not found")
	return chat.Message{}
}

// #665: a panic in a tool is recovered by the turn: the process lives on, the
// reply carries the Schema's error text with its error ID, the stream gets its
// done event, and the turn ends as failed, so no restart recovers it into the
// same panic.
func TestOrchestratorRecoversAToolPanicAndFailsTheTurn(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	agent := &scriptedAgent{responses: []agents.Completion{{ToolCalls: []agents.ToolCall{
		budgetCall("call-1", schema.Tools[0].Name, "paint")}}}}
	command, err := processOnce(t, service, schema, agent, panickingToolExecutor{})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Process = %v, want the recovered panic", err)
	}
	reply := storedReply(t, service, command)
	if !strings.HasPrefix(reply.Content, schema.Presentation.Messages.UnknownError) ||
		!strings.Contains(reply.Content, command.AssistantMessageID) {
		t.Fatalf("reply = %q", reply.Content)
	}
	if _, closed := streamStatus(t, service, schema, command); !closed {
		t.Fatal("the stream got no done event")
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-error", configuredEndReasons.Error)
}

// #665: a model that keeps calling tools is stopped at
// chat.agent.max_tool_rounds_per_turn: the round past it does not run and the
// reply ends with the Schema note naming the bound.
func TestOrchestratorStopsToolRoundsAtTheSchemaBound(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolRoundsPerTurn = 2
	tool := schema.Tools[0].Name
	var responses []agents.Completion
	for round := range 4 {
		prompt := fmt.Sprintf("r%d", round)
		responses = append(responses, agents.Completion{Content: prompt, ToolCalls: []agents.ToolCall{budgetCall(prompt, tool, prompt)}})
	}
	agent := &scriptedAgent{responses: responses}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	command, err := processOnce(t, service, schema, agent, tools)
	if err != nil {
		t.Fatal(err)
	}
	if executed := drainPrompts(tools); executed != "r0,r1" {
		t.Fatalf("executed %q, want the first two rounds only", executed)
	}
	if len(agent.requests) != 3 {
		t.Fatalf("agent requests = %d, want 3", len(agent.requests))
	}
	reply := storedReply(t, service, command)
	note := strings.ReplaceAll(schema.Presentation.Messages.ToolRoundsCapped, "{limit}", "2")
	if note == "" || !strings.HasSuffix(reply.Content, note) || len(reply.ToolCalls) != 2 {
		t.Fatalf("reply = %q with %d calls", reply.Content, len(reply.ToolCalls))
	}
}

// #665: a round the upstream cut at its output limit runs none of its calls
// (their arguments may be cut short) and the reply says it was cut; a round
// its content filter ended says that instead.
func TestOrchestratorEndsACutRoundWithTheSchemaNote(t *testing.T) {
	schema := configuredChatSchema(t)
	messages := schema.Presentation.Messages
	for _, test := range []struct {
		reason, note string
	}{
		{agents.FinishLength, messages.AgentReplyTruncated},
		{agents.FinishContentFilter, messages.AgentReplyFiltered},
	} {
		service := newChatService(t, newMemoryChatRepository())
		agent := &scriptedAgent{responses: []agents.Completion{{Content: "Partial plan", FinishReason: test.reason,
			ToolCalls: []agents.ToolCall{budgetCall("call-1", schema.Tools[0].Name, "paint")}}}}
		tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 1)}
		command, err := processOnce(t, service, schema, agent, tools)
		if err != nil {
			t.Fatal(err)
		}
		if executed := drainPrompts(tools); executed != "" {
			t.Fatalf("%s: executed %q", test.reason, executed)
		}
		reply := storedReply(t, service, command)
		if test.note == "" || !strings.HasPrefix(reply.Content, "Partial plan") || !strings.HasSuffix(reply.Content, test.note) ||
			len(reply.ToolCalls) != 0 {
			t.Fatalf("%s: reply = %q with %d calls", test.reason, reply.Content, len(reply.ToolCalls))
		}
	}
}

// halfThenFailAgent streams half a sentence, then fails.
type halfThenFailAgent struct{ err error }

func (agent halfThenFailAgent) Stream(_ context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	if err := emit(agents.Delta{Content: "Hello wor"}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{}, agent.err
}

func (agent halfThenFailAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, agent.err
}

// #665: the text a first round streamed before it failed stays in the reply,
// before the failure text, as the person saw it.
func TestOrchestratorKeepsTheStreamedTextOfAFailedFirstRound(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	command, err := processOnce(t, service, schema, halfThenFailAgent{err: errors.New("boom")}, noToolExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	reply := storedReply(t, service, command)
	if !strings.HasPrefix(reply.Content, "Hello wor") || !strings.Contains(reply.Content, schema.Presentation.Messages.UnknownError) {
		t.Fatalf("reply = %q", reply.Content)
	}
}

// #665: a failure whose failure text cannot be stored still ends the stream
// with its done event.
func TestOrchestratorFailureEndsTheStreamWhenItsTextCannotBeStored(t *testing.T) {
	repository := &endingFlakyRepository{memoryChatRepository: newMemoryChatRepository()}
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	repository.failEnding(1, schema.Presentation.Messages.UnknownError)
	command, err := processOnce(t, service, schema, failingAgent{err: errors.New("boom")}, noToolExecutor{})
	if err == nil {
		t.Fatal("Process ended without the store failure")
	}
	if _, closed := streamStatus(t, service, schema, command); !closed {
		t.Fatal("the stream got no done event")
	}
}

// #665: an automatic compaction that failed is not tried again on the next
// turn of the session: the turn goes on with the full context without waiting
// the summary call's timeout again.
func TestFailedAutomaticCompactionBacksOff(t *testing.T) {
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek", WindowOverride: 1000})
	if fixture.schema.ContextCompaction.AutoFailureBackoffSeconds <= 0 {
		t.Fatal("the default Schema declares no auto_failure_backoff_seconds")
	}
	for round := 1; round <= fixture.schema.ContextCompaction.KeepRecentRounds+2; round++ {
		fixture.turn(t, fmt.Sprintf("round %d", round))
	}
	fixture.agent.usage = &agents.Usage{PromptTokens: 950}
	fixture.turn(t, "fill")
	fixture.agent.summaryErr = errors.New("upstream timeout")
	fixture.turn(t, "keep going")
	fixture.agent.mu.Lock()
	tried := len(fixture.agent.summaries)
	fixture.agent.mu.Unlock()
	if tried == 0 {
		t.Fatal("the automatic compaction was not tried")
	}
	fixture.turn(t, "and again")
	fixture.agent.mu.Lock()
	defer fixture.agent.mu.Unlock()
	if len(fixture.agent.summaries) != tried {
		t.Fatalf("summary calls = %d after the backoff started at %d", len(fixture.agent.summaries), tried)
	}
}
