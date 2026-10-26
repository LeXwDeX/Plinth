package generationtool_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type failingObservationGateway struct {
	*fakeGateway
	readErr error
}

func (g *failingObservationGateway) Get(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.Task, error) {
	if g.readErr != nil {
		return generation.Task{}, g.readErr
	}
	return g.fakeGateway.Get(ctx, owner, task)
}

func TestBoundTaskReadAndObservationErrorsRespectDurableDeadline(t *testing.T) {
	for _, mode := range []string{"deleted", "transient read", "registered observation", "state observation"} {
		t.Run(mode, func(t *testing.T) {
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := product.Application()
			tool := application.Chat.Tools[0]
			now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
			deadline := now.Add(time.Minute)
			original := generation.Task{ID: "original-task", OwnerID: "owner-a", SessionID: "session-a",
				State: generation.TaskRunning, Offering: generation.Offering{Timeout: time.Hour}}
			gateway := &failingObservationGateway{fakeGateway: &fakeGateway{tasks: []generation.Task{original, original, original, original}}}
			if mode == "deleted" {
				gateway.readErr = &generation.Error{Code: generation.ErrNotFound}
			} else if mode == "transient read" {
				gateway.readErr = errors.New("private read failure")
			}
			executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
				Chat: application.Chat, TaskStates: application.TaskStates, Clock: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", BoundTaskID: "original-task",
				Tool: tool, Call: agents.ToolCall{ID: "call-a", Name: tool.Name}, TaskWaitDeadline: deadline}
			observe := func(o chat.ToolObservation) error {
				if mode == "registered observation" && o.Registered || mode == "state observation" && !o.Registered {
					return errors.New("private observation failure")
				}
				return nil
			}
			result, err := executor.Execute(context.Background(), command, observe)
			if err == nil || !result.AwaitTask || result.TaskID != "original-task" || !result.WaitUntil.Equal(deadline) {
				t.Fatalf("read failure lost original deadline: %+v err=%v", result, err)
			}
			now = deadline
			result, err = executor.Execute(context.Background(), command, observe)
			if err == nil || result.AwaitTask || result.TaskID != "original-task" || result.AgentContent == "" {
				t.Fatalf("expired observation still waits: %+v err=%v", result, err)
			}
			if strings.Contains(result.AgentContent, "private") || gateway.submitted.OfferingID != "" {
				t.Fatalf("failed read exposed details or resubmitted: %+v command=%+v", result, gateway.submitted)
			}
		})
	}
}

func TestFirstAcceptedObservationErrorHasDeadline(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	offering := generation.Offering{ID: "configured/offering", ModelID: "configured-model", Enabled: true,
		Capability: tool.FallbackCapability, Timeout: 3 * time.Minute,
		Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true}}}
	gateway := &fakeGateway{offerings: []generation.Offering{offering}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
		Chat: application.Chat, TaskStates: application.TaskStates, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
		Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"paint"}`)},
		Options: chat.TurnOptions{AIModel: "configured-model"}}, func(chat.ToolObservation) error {
		return errors.New("event unavailable")
	})
	if err == nil || !result.AwaitTask || result.TaskID != "configured-task" || !result.WaitUntil.Equal(now.Add(offering.Timeout)) {
		t.Fatalf("accepted callback failure lost identity/deadline: %+v %v", result, err)
	}
}
