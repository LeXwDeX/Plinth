package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #120: when the chat model's upstream refused for a used-up balance or key
// quota, the person reads the Schema's text for it, with the error ID, not
// "unknown error" and not the upstream wording. A plain refusal is unchanged.
func TestOrchestratorSaysWhenTheChatModelQuotaIsUsedUp(t *testing.T) {
	quota := errors.Join(&agents.QuotaExhaustion{Class: "credential_quota"},
		&agents.StatusError{Status: 429}, errors.New("secret upstream diagnostic"))
	for _, test := range []struct {
		name string
		err  error
		want func(schemaQuota, schemaUnknown string) string
	}{
		{"quota exhausted", quota, func(text, _ string) string { return text }},
		{"plain rate limit", errors.Join(&agents.StatusError{Status: 429}, errors.New("secret upstream diagnostic")),
			func(_, unknown string) string { return unknown }},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := configuredChatSchema(t)
			quotaText := schema.Presentation.Messages.AgentQuotaExhausted
			if quotaText == "" {
				t.Fatal("the default Schema declares no agent_quota_exhausted text")
			}
			content := runFailingTurn(t, failingAgent{err: test.err})
			want := test.want(quotaText, schema.Presentation.Messages.UnknownError)
			if !strings.HasPrefix(content, want) || strings.Contains(content, "secret") || strings.Contains(content, "quota_exceeded") {
				t.Fatalf("assistant = %q, want it to start with %q", content, want)
			}
			if !strings.Contains(content, strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]) {
				t.Fatalf("assistant = %q lacks the error reference", content)
			}
		})
	}
}

// A follow-up request refused for a used-up quota, after the Agent already
// called a tool, leaves no reply text: the person reads the quota text, not
// "no result, please retry".
func TestOrchestratorSaysWhenTheFollowUpHitTheUsedUpQuota(t *testing.T) {
	schema := configuredChatSchema(t)
	agent := &quotaAfterToolAgent{tool: schema.Tools[0].Name,
		err: errors.Join(&agents.QuotaExhaustion{Class: "insufficient_balance"}, &agents.StatusError{Status: 429})}
	content := runFailingTurnWith(t, schema, agent)
	if !strings.HasPrefix(content, schema.Presentation.Messages.AgentQuotaExhausted) {
		t.Fatalf("assistant = %q", content)
	}
	if agent.streams() < 2 {
		t.Fatalf("streams = %d, want the follow-up to have run", agent.streams())
	}
}

func runFailingTurn(t *testing.T, agent agents.Adapter) string {
	t.Helper()
	return runFailingTurnWith(t, configuredChatSchema(t), agent)
}

func runFailingTurnWith(t *testing.T, schema catalog.ChatSchema, agent agents.Adapter) string {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return messages[1].Content
}

// quotaAfterToolAgent calls one tool without any text, then every later
// stream fails with err.
type quotaAfterToolAgent struct {
	mu    sync.Mutex
	tool  string
	err   error
	count int
}

func (agent *quotaAfterToolAgent) Stream(context.Context, agents.Request, func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.count++
	if agent.count == 1 {
		return agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-1", Name: agent.tool, Arguments: []byte(`{"prompt":"paint"}`)}}}, nil
	}
	return agents.Completion{}, agent.err
}

func (agent *quotaAfterToolAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, agent.err
}

func (agent *quotaAfterToolAgent) streams() int {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.count
}
