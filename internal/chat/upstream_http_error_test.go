package chat_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// chatProviderName is the default chat connection's display name.
func chatProviderName(t *testing.T) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	for _, connection := range application.Runtime.ProviderConnections {
		if connection.ID == application.Chat.Agent.ConnectionID {
			return connection.DisplayName
		}
	}
	t.Fatal("the default Schema declares no chat connection")
	return ""
}

func runNamedFailingTurn(t *testing.T, agent agents.Adapter) string {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: configuredChatSchema(t), Model: "configured-model", Agent: agent, Tools: noToolExecutor{},
		AgentProviderName: chatProviderName(t),
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

// The chat gateway answered an error status and the #509 retries did not get
// past it: the person reads "对话模型 网关返回 {status}，原因：{reason}" with the
// error ID, never the upstream's wording or the generic "no result".
func TestOrchestratorNamesTheChatGatewayHTTPError(t *testing.T) {
	schema := configuredChatSchema(t)
	reference := strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]

	t.Run("fake gateway 5xx after retries", func(t *testing.T) {
		fake, err := fakeupstream.New(fakeupstream.Config{FailTimes: 100}, openaiv1.Version())
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		adapter, err := openaicompatible.New(openaicompatible.Config{
			BaseURL: server.URL + openaiv1.Prefix, APIKey: "fake-" + string(fakeupstream.ScenarioServerError),
			EndpointPath: "/chat/completions", AuthorizationName: "Authorization", AuthorizationType: "Bearer",
			RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		})
		if err != nil {
			t.Fatal(err)
		}
		attempts := 0
		retrying := agents.NewRetrying(adapter, agents.RetryPolicy{
			MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
			Wait: func(context.Context, time.Duration) error { attempts++; return nil },
		})
		content := runNamedFailingTurn(t, retrying)
		if attempts != 2 {
			t.Fatalf("retries = %d, want the call retried until the policy gave up", attempts)
		}
		want := "对话模型 网关返回 500，原因：上游服务器故障。"
		if !strings.HasPrefix(content, want) || !strings.Contains(content, reference) || strings.Contains(content, "internal server error") {
			t.Fatalf("assistant = %q, want %q with the error reference", content, want)
		}
	})

	t.Run("502 bad gateway", func(t *testing.T) {
		transient := agents.Transient(errors.Join(&agents.StatusError{Status: 502}, errors.New("<html>502 Bad Gateway</html>")), 0)
		content := runNamedFailingTurn(t, failingAgent{err: transient})
		want := "对话模型 网关返回 502，原因：上游服务器故障。"
		if !strings.HasPrefix(content, want) || strings.Contains(content, "html") {
			t.Fatalf("assistant = %q, want %q", content, want)
		}
	})

	t.Run("429 after a tool call", func(t *testing.T) {
		agent := &quotaAfterToolAgent{tool: schema.Tools[0].Name,
			err: agents.Transient(errors.Join(&agents.StatusError{Status: 429}, errors.New("rate limit exceeded")), 0)}
		content := runNamedFailingTurn(t, agent)
		want := "对话模型 网关返回 429，原因：上游服务器限流，请稍后再试。"
		if !strings.HasPrefix(content, want) || !strings.Contains(content, reference) {
			t.Fatalf("assistant = %q, want %q", content, want)
		}
	})

	t.Run("502 after a partial reply", func(t *testing.T) {
		agent := &breaksAfterToolAgent{tool: schema.Tools[0].Name, opening: "Working on it.",
			err: agents.Transient(errors.Join(&agents.StatusError{Status: 502}, errors.New("bad gateway")), 0)}
		content := runNamedFailingTurn(t, agent)
		if !strings.HasPrefix(content, "Working on it.") || !strings.Contains(content, "对话模型 网关返回 502，原因：上游服务器故障。") ||
			strings.Contains(content, schema.Presentation.Messages.AgentUpstreamInterrupted) {
			t.Fatalf("assistant = %q", content)
		}
	})

	t.Run("specific refusals keep their text", func(t *testing.T) {
		quota := errors.Join(&agents.QuotaExhaustion{Class: "credential_quota"}, &agents.StatusError{Status: 429})
		if content := runNamedFailingTurn(t, failingAgent{err: quota}); !strings.HasPrefix(content, schema.Presentation.Messages.AgentQuotaExhausted) {
			t.Fatalf("quota: assistant = %q", content)
		}
		// A break without an HTTP status stays the interrupted text.
		broke := agents.Transient(errors.New("agent stream ended before its last chunk"), 0)
		if content := runNamedFailingTurn(t, failingAgent{err: broke}); !strings.HasPrefix(content, schema.Presentation.Messages.AgentUpstreamInterrupted) {
			t.Fatalf("break: assistant = %q", content)
		}
	})
}
