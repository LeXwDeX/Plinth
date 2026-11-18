package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// replayGateGenerate reports an already prepared operation and executes only
// when the gate lets the replay through.
type replayGateGenerate struct{ calls int }

func (g *replayGateGenerate) UsesExecutionGrants() bool { return true }

func (g *replayGateGenerate) ExistingOperation(context.Context, chat.ToolCommand) (bool, error) {
	return true, nil
}

func (g *replayGateGenerate) Execute(context.Context, chat.ToolCommand,
	func(chat.ToolObservation) error) (chat.ToolResult, error) {
	g.calls++
	return chat.ToolResult{NoSubmission: true}, nil
}

func gateCommand(sessionID, callID string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: testOwner, SessionID: sessionID,
		Call: agents.ToolCall{ID: callID, Name: "refine_image", Arguments: json.RawMessage(`{}`)},
	}
}

func TestSubmissionGateRefusesFreshGeneration(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerate{}
	gates := 0
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		SubmissionGate: func(context.Context, chat.ToolCommand) string {
			gates++
			return "presence pause from Schema"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := executor.Execute(context.Background(), gateCommand("ses_item", "call-1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gates != 1 || gen.callCount != 0 {
		t.Fatalf("gate calls=%d generation calls=%d; want a refusal before submit", gates, gen.callCount)
	}
	if result.AgentContent != "presence pause from Schema" || !result.NoSubmission {
		t.Fatalf("refusal = %+v; want Schema text and no submission", result)
	}
}

func TestSubmissionGateRefusesPreparedReplay(t *testing.T) {
	t.Parallel()
	gen := &replayGateGenerate{}
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		SubmissionGate: func(context.Context, chat.ToolCommand) string {
			return "stopped from Schema"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := executor.Execute(context.Background(), gateCommand("ses_item", "call-2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gen.calls != 0 || result.AgentContent != "stopped from Schema" || !result.NoSubmission {
		t.Fatalf("replay = %+v calls=%d; want refused before the prepared operation", result, gen.calls)
	}
}

func TestSubmissionGateLetsOrdinarySessionThrough(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		SubmissionGate: func(_ context.Context, command chat.ToolCommand) string {
			if command.SessionID == "ses_item" {
				return "paused"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := executor.Execute(context.Background(), gateCommand("ses_plain", "call-3"), nil); err != nil {
		t.Fatal(err)
	}
	if gen.callCount != 1 {
		t.Fatalf("ordinary session generation calls = %d; want 1", gen.callCount)
	}
}

func TestSubmissionGateSkipsBoundTaskObservation(t *testing.T) {
	t.Parallel()
	gen := &fakeGenerate{}
	gates := 0
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		SubmissionGate: func(context.Context, chat.ToolCommand) string {
			gates++
			return "paused"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := gateCommand("ses_item", "call-4")
	command.BoundTaskID = "task_existing"
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	if gates != 0 || gen.callCount != 1 {
		t.Fatalf("bound task gates=%d generation calls=%d; want observation only", gates, gen.callCount)
	}
}
