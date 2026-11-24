package chat

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// detachedToolsFake answers the one question the end reason asks of the
// executor, and keeps what it was asked.
type detachedToolsFake struct {
	noopToolExecutorForWake
	detached bool
	err      error
	asked    []ToolCommand
}

func (fake *detachedToolsFake) DetachedOperationForCall(_ context.Context, command ToolCommand) (bool, error) {
	fake.asked = append(fake.asked, command)
	return fake.detached, fake.err
}

func endReasonOrchestratorForInternal(t *testing.T, tools ToolExecutor, logger *slog.Logger) (*Orchestrator, catalog.ChatSchema) {
	t.Helper()
	service := newChatServiceForInternal(t)
	service.turnEndReasons = catalog.ChatTurnEndReasonsSchema{
		Completed: "c", AwaitingApproval: "a", Stopped: "s", Background: "b", Error: "e",
	}
	schema := suspendTestSchema()
	orchestrator, err := NewOrchestrator(OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: &noopStreamAgent{}, Tools: tools, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator, schema
}

// The end reason follows one fixed priority: failed, background, stopped,
// awaiting approval, completed (#328).
func TestTurnEndReasonPriority(t *testing.T) {
	for _, test := range []struct {
		name     string
		outcome  turnOutcome
		detached bool
		want     TurnEndReason
	}{
		{"nothing special", turnOutcome{}, false, TurnEndCompleted},
		{"an approval card", turnOutcome{}, true, TurnEndAwaitingApproval},
		{"a stop beats a card", turnOutcome{stopped: true}, true, TurnEndStopped},
		{"background beats a stop", turnOutcome{background: true, stopped: true}, true, TurnEndBackground},
		{"failed beats everything", turnOutcome{failed: true, background: true, stopped: true}, true, TurnEndError},
		{"failed alone", turnOutcome{failed: true}, false, TurnEndError},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools := &detachedToolsFake{detached: test.detached}
			orchestrator, schema := endReasonOrchestratorForInternal(t, tools, nil)
			assistant := Message{ToolCalls: []ToolCall{{ID: "call-1", Name: schema.Tools[0].Name}}}
			got := orchestrator.turnEndReason(context.Background(), ProcessTurn{
				OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			}, assistant, test.outcome)
			if got != test.want {
				t.Fatalf("reason = %d, want %d", got, test.want)
			}
		})
	}
}

// Only generation calls can have an approval card, and the question carries
// what the stop reconciliation's does: the call and its frozen key, the bound
// Task, the turn and the options (the harness binding is checked against them).
func TestTurnEndReasonAsksOnlyGenerationCallsAboutTheirCards(t *testing.T) {
	tools := &detachedToolsFake{}
	orchestrator, schema := endReasonOrchestratorForInternal(t, tools, nil)
	if len(schema.AgentTools) == 0 {
		t.Fatal("the shipped Schema declares no agent tool")
	}
	assistant := Message{ToolCalls: []ToolCall{
		{ID: "call-agent", Name: schema.AgentTools[0].Name},
		{ID: "call-unknown", Name: "no-such-tool"},
		{ID: "call-paid", Name: schema.Tools[0].Name, Arguments: []byte(`{"prompt":"p"}`), TaskID: "task-1", IdempotencyKey: "key-1"},
	}}
	command := ProcessTurn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Options: TurnOptions{Model: "frozen-model"}}
	if got := orchestrator.turnEndReason(context.Background(), command, assistant, turnOutcome{}); got != TurnEndCompleted {
		t.Fatalf("reason = %d", got)
	}
	if len(tools.asked) != 1 {
		t.Fatalf("asked about %d calls: %#v", len(tools.asked), tools.asked)
	}
	asked := tools.asked[0]
	if asked.OwnerID != "owner-a" || asked.SessionID != "session-a" || asked.AssistantMessageID != "turn-a" ||
		asked.Call.ID != "call-paid" || asked.Call.Name != schema.Tools[0].Name || string(asked.Call.Arguments) != `{"prompt":"p"}` ||
		asked.BoundTaskID != "task-1" || asked.FrozenIdempotencyKey != "key-1" || asked.Options.Model != "frozen-model" {
		t.Fatalf("question = %#v", asked)
	}
}

// One unreadable call does not hide a card on another call.
func TestTurnEndReasonOneUnreadableCallDoesNotHideAnotherCard(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	questions := 0
	orchestrator, schema := endReasonOrchestratorForInternal(t, &scriptedDetachedTools{
		script: func(command ToolCommand) (bool, error) {
			questions++
			if command.Call.ID == "call-1" {
				return false, errors.New("operation store unavailable")
			}
			return true, nil
		},
	}, logger)
	assistant := Message{ToolCalls: []ToolCall{
		{ID: "call-1", Name: schema.Tools[0].Name},
		{ID: "call-2", Name: schema.Tools[0].Name},
	}}
	got := orchestrator.turnEndReason(context.Background(), ProcessTurn{OwnerID: "o", SessionID: "s", AssistantMessageID: "m"},
		assistant, turnOutcome{})
	if got != TurnEndAwaitingApproval || questions != 2 {
		t.Fatalf("reason = %d after %d questions", got, questions)
	}
	if !strings.Contains(logs.String(), "call-1") || !strings.Contains(logs.String(), "operation store unavailable") {
		t.Fatalf("the unreadable call was not logged: %q", logs.String())
	}
}

type scriptedDetachedTools struct {
	noopToolExecutorForWake
	script func(ToolCommand) (bool, error)
}

func (tools *scriptedDetachedTools) DetachedOperationForCall(_ context.Context, command ToolCommand) (bool, error) {
	return tools.script(command)
}
