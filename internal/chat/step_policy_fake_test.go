package chat_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	fakeregistry "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #330 fake stack: the Agent's real system prompt is sent to the fake
// upstream, which answers [[fake:system-contains TEXT]] from it. The three
// step modes each carry their own rule, and a non-steps session carries none.
// Zero real upstream: the fake is an in-process loopback server.
func TestStepPolicyPromptReachesTheModelOnTheFakeUpstream(t *testing.T) {
	handler, prefix, err := fakeregistry.Handler("openai/v1")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           server.URL + prefix,
		APIKey:            "fake-key",
		EndpointPath:      "chat/completions",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    10 * time.Second,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shipped := taskCatalog.Application()
	schema := configuredChatSchema(t)
	schema.Agent.SystemPrompt = shipped.Chat.Agent.SystemPrompt
	schema.ApprovalPolicy = shipped.Chat.ApprovalPolicy
	// The fake recognizes its own system message by the real role name.
	schema.Roles = shipped.Chat.Roles
	schema.Tools = shipped.Chat.Tools
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ApprovalPolicy = approvalpolicy.New("manual", []string{"auto", "manual"},
			[]string{"text-to-3d"}, []string{"text-to-3d"})
	})
	ctx := context.Background()

	for name, testCase := range map[string]struct {
		mode   string
		marker string
		// also is a second text the same session must carry.
		also   string
		absent string
	}{
		"the segment name": {mode: approvalpolicy.StepModeAgent, marker: "3D 步数", also: "由你自己管理步数"},
		"unlimited mode":   {mode: approvalpolicy.StepModeUnlimited, marker: "不限制 3D 步数"},
		"the cap mode":     {mode: approvalpolicy.StepModeCap, marker: "每个资产最多 20 步"},
		"another mode text is absent": {mode: approvalpolicy.StepModeCap, marker: "由你自己管理步数",
			absent: "present: 由你自己管理步数"},
	} {
		session, err := service.CreateSession(ctx, "owner-a", "Owner", name)
		if err != nil {
			t.Fatal(err)
		}
		service.SetMCPStepSessions(stepMCPAssets{mark: map[string]bool{session.ID: true}})
		config := chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "m", Agent: adapter, Tools: &scopeToolExecutor{},
			StepPolicy: shipped.StepPolicy,
			StepUsage: func(context.Context, string, string) (executiongrant.StepSummary, error) {
				return executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: testCase.mode, Cap: 20, Used: 2}, nil
			},
			StepBalances: func(context.Context) []chat.StepBalance {
				return []chat.StepBalance{{Label: "MeshyAI", Known: true, Amount: "1000", UnitLabel: "Meshy credits"}}
			},
			StepCosts: func(context.Context, string, string) []chat.StepCost { return nil },
		}
		orchestrator, err := chat.NewOrchestrator(config)
		if err != nil {
			t.Fatal(err)
		}
		reply := fakeStepTurn(t, service, orchestrator, session.ID, testCase.marker)
		if testCase.absent != "" {
			if strings.Contains(reply, testCase.absent) {
				t.Errorf("%s: the reply %q carries another mode's rule", name, reply)
			}
			continue
		}
		if !strings.Contains(reply, "present: "+testCase.marker) {
			t.Errorf("%s: the model did not see %q in the system prompt: %q", name, testCase.marker, reply)
		}
		if testCase.also != "" {
			reply = fakeStepTurn(t, service, orchestrator, session.ID, testCase.also)
			if !strings.Contains(reply, "present: "+testCase.also) {
				t.Errorf("%s: the model did not see %q in the system prompt: %q", name, testCase.also, reply)
			}
		}
	}

	plain, err := service.CreateSession(ctx, "owner-a", "Owner", "plain")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: adapter, Tools: &scopeToolExecutor{},
		StepPolicy: shipped.StepPolicy,
		StepUsage: func(context.Context, string, string) (executiongrant.StepSummary, error) {
			return executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeAgent}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply := fakeStepTurn(t, service, orchestrator, plain.ID, "由你自己管理步数")
	if !strings.Contains(reply, "absent: 由你自己管理步数") {
		t.Fatalf("a non-steps session carried the step block: %q", reply)
	}
}

// fakeStepTurn sends one turn through the fake and returns the assistant's
// answer to the system-contains marker.
func fakeStepTurn(t *testing.T, service *chat.Service, orchestrator *chat.Orchestrator, sessionID, marker string) string {
	t.Helper()
	ctx := context.Background()
	options := chat.TurnOptions{CreativeDomain: "model-3d"}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: sessionID,
		Content: "check [[fake:system-contains " + marker + "]]", Options: options,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: sessionID,
		AssistantMessageID: assistant.ID, Options: options,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.HasPrefix(strings.TrimSpace(messages[index].Content), "present:") ||
			strings.HasPrefix(strings.TrimSpace(messages[index].Content), "absent:") {
			return messages[index].Content
		}
	}
	t.Fatalf("the fake answered no system-contains marker in %d messages", len(messages))
	return ""
}
