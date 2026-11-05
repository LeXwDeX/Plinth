package generationtool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #136: upstream queue time does not count toward the job timeout, so the
// chat waits for the Task as long as the kernel may keep it: the request
// timeout plus the Offering's queue wait limit.
func TestTaskWaitDeadlineCoversTheUpstreamQueueLimit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	now := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	offering := generation.Offering{ID: "configured/offering", ModelID: "configured-model", Enabled: true,
		Capability: tool.FallbackCapability, Timeout: 3 * time.Minute, UpstreamQueueTimeout: 20 * time.Minute,
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
	if err == nil || !result.AwaitTask || !result.WaitUntil.Equal(now.Add(23*time.Minute)) {
		t.Fatalf("wait deadline = %v (await %v), want request timeout plus queue limit", result.WaitUntil, result.AwaitTask)
	}
}
