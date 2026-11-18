package chat_test

// #330 step 4: the platform's 3D self-check context offers executable
// remedies with counted steps. The fake upstream's system-contains marker
// answers from the system prompt the Agent really received, so the reply
// proves the block without reading a private prompt or calling a real model.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

type remedySelfCheckStore struct {
	record chat.SelfCheckRecord
}

func (store *remedySelfCheckStore) ListSelfChecks(context.Context, string, string) ([]chat.SelfCheckRecord, error) {
	return []chat.SelfCheckRecord{store.record}, nil
}

func (store *remedySelfCheckStore) SelfCheck(_ context.Context, _, _, taskID string) (chat.SelfCheckRecord, bool, error) {
	if taskID != store.record.TaskID {
		return chat.SelfCheckRecord{}, false, nil
	}
	return store.record, true, nil
}

func (store *remedySelfCheckStore) SelfChecksByTask(_ context.Context, _, taskID string, _ int) ([]chat.SelfCheckRecord, error) {
	if taskID != store.record.TaskID {
		return nil, nil
	}
	return []chat.SelfCheckRecord{store.record}, nil
}

func (store *remedySelfCheckStore) SaveSelfCheck(_ context.Context, _, _ string,
	record chat.SelfCheckRecord, _ int64) (chat.SelfCheckRecord, bool, error) {
	return record, false, nil
}

// fakeRemedyTurn sends one message carrying the fake-only system-contains
// marker and returns the assembled assistant reply.
func fakeRemedyTurn(t *testing.T, orchestrator *chat.Orchestrator, service *chat.Service,
	sessionID, content string) string {
	t.Helper()
	ctx := context.Background()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: sessionID, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == assistant.ID {
			return message.Content
		}
	}
	t.Fatalf("assistant message %q was not stored", assistant.ID)
	return ""
}

func TestFakeSystemPromptShowsCountedRemediesAndNoZeroStepFix(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	// configuredChatSchema keeps the service's role names; the fake upstream's
	// marker only reads messages whose role is literally "system".
	schema := configuredChatSchema(t)
	schema.Roles.System = "system"
	fake, err := registry.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: server.URL + "/openai/v1", EndpointPath: "chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", APIKey: "fake-key",
		RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatService(t, newMemoryChatRepository())
	store := &remedySelfCheckStore{record: chat.SelfCheckRecord{
		TaskID: "task-a", UpdatedAt: time.Now().UTC(),
		Checks: []chat.SelfCheckCheck{{
			Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
			UsageFailed: []string{"triangles", "uv"},
		}},
	}}
	service.SetSelfChecks(store, schema.SelfCheck)
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "fake-model", Agent: adapter, Tools: noToolExecutor{},
		StepPolicy: application.StepPolicy, Capabilities: application.Capabilities,
		SelfCheckTasks: func(context.Context, string, string) ([]chat.SelfCheckTask, error) {
			return []chat.SelfCheckTask{{TaskID: "task-a"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	present := fakeRemedyTurn(t, orchestrator, service, session.ID,
		"检查 [[fake:system-contains 可补救：重拓扑，1 步]]")
	if present != "present: 可补救：重拓扑，1 步" {
		t.Fatalf("the 3D self-check block lacks the counted remedy: %q", present)
	}
	forbidden := fakeRemedyTurn(t, orchestrator, service, session.ID,
		"检查 [[fake:system-contains 0 步]]")
	if !strings.HasPrefix(forbidden, "absent: ") {
		t.Fatalf("a local fix was shown as 0 steps: %q", forbidden)
	}
	if uv := fakeRemedyTurn(t, orchestrator, service, session.ID,
		"检查 [[fake:system-contains UV 展开]]"); !strings.HasPrefix(uv, "absent: ") {
		t.Fatalf("a local UV fix was offered as a remedy: %q", uv)
	}
}
