package chat

// AGT-12 审查修复的内部单测：唤醒入队语义与挂起/取消竞态仲裁。
// 这些行为依赖私有状态（queued 集合、canceledTurn 标记），
// 外部测试包无法构造，故放内部包。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestEnqueueForWakeDistinguishesNotReleasedFromQueued(t *testing.T) {
	orchestrator, err := NewOrchestrator(OrchestratorConfig{
		Service: newChatServiceForInternal(t),
		Schema:  suspendTestSchema(),
		Model:   "configured-model",
		Agent:   &noopStreamAgent{},
		Tools:   &noopToolExecutorForWake{},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ProcessTurn{
		OwnerID:            "owner",
		SessionID:          "session",
		AssistantMessageID: "message-wake-1",
	}
	// 未 markQueued：直接入队成功（队列有空间）。
	if err := orchestrator.enqueueForWake(command); err != nil {
		t.Fatalf("first enqueueForWake = %v", err)
	}
	// 已在队列：与普通 Enqueue 不同，唤醒入队必须显式报“未释放”，
	// 而不是返回 nil 假成功（审查 R1：去重假成功导致凭据丢失死锁）。
	if err := orchestrator.enqueueForWake(command); !errors.Is(err, ErrWakeNotReleased) {
		t.Fatalf("second enqueueForWake = %v, want ErrWakeNotReleased", err)
	}
	// 清理队列，避免影响其他测试。
	orchestrator.unmarkQueued(command.AssistantMessageID)
}

func TestSuspendForConsentRefusesWhenCancelMarkerPresent(t *testing.T) {
	orchestrator, err := NewOrchestrator(OrchestratorConfig{
		Service: newChatServiceForInternal(t),
		Schema:  suspendTestSchema(),
		Model:   "configured-model",
		Agent:   &noopStreamAgent{},
		Tools:   &noopToolExecutorForWake{},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ProcessTurn{
		OwnerID:            "owner",
		SessionID:          "session",
		AssistantMessageID: "message-cancelled",
	}
	// 模拟 CancelTurn 活动分支已写取消标记（停止与挂起竞态窗口）。
	key := orchestrator.turnKey(command.OwnerID, command.SessionID)
	orchestrator.activeMu.Lock()
	orchestrator.canceledTurn[key] = struct{}{}
	orchestrator.activeMu.Unlock()
	if orchestrator.suspendForConsent(command) {
		t.Fatal("suspendForConsent must refuse when cancel marker present")
	}
	if orchestrator.PendingConsentWaits() != 0 {
		t.Fatal("refused suspension must not register")
	}
}

func TestSuspendRearmsElapsedDeadlineForPersistentAgentGrant(t *testing.T) {
	schema := suspendTestSchema()
	schema.Agent.ConsentWaitTimeoutSeconds = 3
	orchestrator, err := NewOrchestrator(OrchestratorConfig{
		Service: newChatServiceForInternal(t), Schema: schema,
		Model: "configured-model", Agent: &noopStreamAgent{}, Tools: &noopToolExecutorForWake{},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ProcessTurn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn-a",
		ConsentWaitDeadline: time.Now().Add(-time.Second)}
	if !orchestrator.suspendForConsent(command) {
		t.Fatal("elapsed persistent wait was refused")
	}
	orchestrator.suspendMu.Lock()
	deadline := orchestrator.consentWaits[orchestrator.turnKey(command.OwnerID, command.SessionID)].deadline
	orchestrator.suspendMu.Unlock()
	if time.Until(deadline) < 2*time.Second {
		t.Fatalf("elapsed wait was not rearmed: %v", deadline)
	}
	orchestrator.popConsentWait(command.OwnerID, command.SessionID)
}

func TestMaxRoundFromCallsParsesRoundIDs(t *testing.T) {
	calls := []ToolCall{
		{ID: "msg_tool_0"},
		{ID: "msg_r1_tool_0"},
		{ID: "msg_r3_tool_2"},
	}
	if got := maxRoundFromCalls(calls); got != 3 {
		t.Fatalf("maxRoundFromCalls = %d, want 3", got)
	}
	if got := maxRoundFromCalls([]ToolCall{{ID: "msg_tool_1"}}); got != 0 {
		t.Fatalf("maxRoundFromCalls(round0 only) = %d, want 0", got)
	}
	if got := maxRoundFromCalls(nil); got != 0 {
		t.Fatalf("maxRoundFromCalls(nil) = %d, want 0", got)
	}
}

func suspendTestSchema() catalog.ChatSchema {
	schema, err := catalog.LoadDefault()
	if err != nil {
		panic(err)
	}
	chatSchema := schema.Application().Chat
	chatSchema.Agent.WorkerCount = 1
	chatSchema.Agent.QueueSize = 8
	return chatSchema
}

// stubRepository 嵌入接口占位：这些内部单测只构造 Orchestrator 并操作
// 挂起注册表/标记，不触达仓储方法；被意外调用时 panic 即失败信号。
type stubRepository struct {
	Repository
}

func newChatServiceForInternal(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(ServiceConfig{
		Repository:      stubRepository{},
		Clock:           func() time.Time { return time.Unix(0, 0).UTC() },
		DefaultTitle:    "internal default title",
		UserRole:        "internal-user",
		AssistantRole:   "internal-assistant",
		PendingContent:  "internal-pending",
		StreamActive:    "internal-active",
		StreamDone:      "internal-done",
		StreamError:     "internal-error",
		TurnQueued:      "internal-turn-queued",
		TurnRunning:     "internal-turn-running",
		TurnWaitingTask: "internal-turn-waiting-task",
		TurnDone:        "internal-turn-done",
		TurnError:       "internal-turn-error",
		PlaceholderHealing: PlaceholderHealingPolicy{
			StaleAfter:                time.Minute,
			PromptArgument:            "internal-prompt",
			PromptPreviewCharacters:   5,
			InterruptedMessage:        "internal interrupted",
			InterruptedPromptTemplate: "internal interrupted {prompt}",
		},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 8,
		EventFields: EventFieldPolicy{
			Type:      "internal-kind",
			MessageID: "internal-message",
		},
		EventTypes: EventTypePolicy{
			MessageCreated: "internal-created",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type noopStreamAgent struct{}

func (*noopStreamAgent) Stream(
	_ context.Context,
	_ agents.Request,
	_ func(agents.Delta) error,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

func (*noopStreamAgent) Complete(
	_ context.Context,
	_ agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

type noopToolExecutorForWake struct{}

func (*noopToolExecutorForWake) Execute(
	_ context.Context,
	_ ToolCommand,
	_ func(ToolObservation) error,
) (ToolResult, error) {
	return ToolResult{}, nil
}
