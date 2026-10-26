package chat_test

// AGT-12 挂起/恢复回归：request_consent 不再在 worker 内阻塞轮询，
// 而是登记挂起立即释放 worker；用户决定/超时/停止唤醒后按落库计划
// 重放续跑。以下测试覆盖 worker 释放、四条续跑路径与幂等。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

// suspendConsentToolExecutor 把 request_consent 实现为挂起标记，
// 其余工具回执空内容。
type suspendConsentToolExecutor struct {
	mu                sync.Mutex
	consents          int
	resumeConsentGate <-chan struct{}
}

type uncertainAcceptedTool struct{ calls int }

func TestUnknownAcceptedThreeViewKeepsCheckpointForRestart(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
		ID: "three-view", Name: "create_three_view", Arguments: json.RawMessage(`{}`),
	}}}, resume: []agents.Completion{{Content: "recovered original step"}}}
	tools := &uncertainAcceptedTool{}
	makeOrchestrator := func() *chat.Orchestrator {
		o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema,
			Model: "model", Agent: agent, Tools: tools,
			Configuration: func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
				legacy := append([]catalog.ChatAgentToolSchema(nil), schema.AgentTools...)
				for index := range legacy {
					if legacy[index].Name == "create_three_view" {
						legacy[index].Execution.Effects = []string{"SESSION_WRITE"}
						legacy[index].Execution.Cancellation = "none"
					}
				}
				return chat.AgentConfigurationSnapshot{Revision: "legacy", AgentTools: legacy}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "three views")
	if err != nil {
		t.Fatal(err)
	}
	assistantID := startTurnFor(t, service, "owner-a", session.ID, "three views")
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistantID}
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, assistantID)
	if err != nil || len(turn.Checkpoint.Completed) != 0 || turn.State == "configured-turn-error" {
		t.Fatalf("unknown paid Skill acceptance was finalized: %+v %v", turn, err)
	}
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, assistantID)
	if err != nil || tools.calls != 2 || turn.State != "configured-turn-done" {
		t.Fatalf("paid Skill recovery failed: %+v calls=%d err=%v", turn, tools.calls, err)
	}
	messages, err := service.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err != nil || messages[1].ToolCalls[0].TaskID != "accepted-task" {
		t.Fatalf("paid Skill binding was lost: %+v %v", messages, err)
	}
	for _, tool := range turn.Checkpoint.Configuration.AgentTools {
		if tool.Name == "create_three_view" && (tool.Execution.Effects[0] != "SESSION_WRITE" || tool.Execution.Cancellation != "none") {
			t.Fatal("recovery mutated the frozen configuration")
		}
	}
}

type unavailableBoundTool struct {
	wantHarness    *harness.Binding
	accepted       int
	lookupFailures int
	observed       int
}

func (tool *unavailableBoundTool) BoundTaskForCall(_ context.Context, command chat.ToolCommand) (string, error) {
	if tool.wantHarness != nil && (command.Options.Harness == nil || command.Options.Harness.Selection.Binding != *tool.wantHarness) {
		return "", chat.ErrTurnCheckpointConflict
	}
	if tool.lookupFailures > 0 {
		tool.lookupFailures--
		return "", errors.New("temporary grant read failure")
	}
	if command.OwnerID != "owner-a" || command.Call.ID == "" || command.IdempotencyKey() == "" {
		return "", errors.New("wrong recovery identity")
	}
	return "accepted-task", nil
}

func (tool *unavailableBoundTool) Execute(_ context.Context, command chat.ToolCommand,
	_ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	if command.BoundTaskID == "" {
		tool.accepted++
		return chat.ToolResult{}, errors.New("unknown submit response")
	}
	if command.BoundTaskID != "accepted-task" {
		return chat.ToolResult{NoSubmission: true}, errors.New("wrong bound Task")
	}
	tool.observed++
	return chat.ToolResult{TaskID: command.BoundTaskID, AgentContent: "original task observed"}, nil
}

func TestUnavailableOfferingRecoversBoundTaskWithoutChatBinding(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	schema.Agent.ConsentWaitTimeoutSeconds = 60
	toolName := schema.Tools[0].Name
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
		ID: "call-a", Name: toolName, Arguments: json.RawMessage(`{"prompt":"paint"}`),
	}}}, resume: []agents.Completion{{Content: "recovered"}}}
	tools := &unavailableBoundTool{lookupFailures: 1}
	newOrchestrator := func(available bool) *chat.Orchestrator {
		o, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tools,
			ToolAllowed: func(string, string) bool { return available },
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	frozen := harness.DefaultSelection("owner-a", session.ID)
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) {
		return resolve.Snapshot{Selection: frozen, Status: resolve.StatusFree}, nil
	})
	tools.wantHarness = &frozen.Binding
	assistantID := startTurnFor(t, service, "owner-a", session.ID, "draw")
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistantID}
	if err := newOrchestrator(true).Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || len(turn.Checkpoint.Completed) != 0 {
		t.Fatalf("unknown result completed: %+v %v", turn, err)
	}
	if err := newOrchestrator(false).Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || len(turn.Checkpoint.Completed) != 0 {
		t.Fatalf("temporary grant read completed: %+v %v", turn, err)
	}
	if err := newOrchestrator(false).Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if tools.accepted != 1 || tools.observed != 1 {
		turn, _ := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
		t.Fatalf("accepted=%d observed=%d lookups_remaining=%d turn=%+v", tools.accepted, tools.observed, tools.lookupFailures, turn)
	}
}

func (tool *uncertainAcceptedTool) Execute(_ context.Context, _ chat.ToolCommand,
	observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	tool.calls++
	if tool.calls == 1 {
		return chat.ToolResult{}, errors.New("submit result unknown")
	}
	if err := observe(chat.ToolObservation{Registered: true, TaskID: "accepted-task"}); err != nil {
		return chat.ToolResult{}, err
	}
	return chat.ToolResult{TaskID: "accepted-task"}, nil
}

func TestUnknownAcceptedGenerationKeepsCheckpointForRestart(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	schema.Agent.ConsentWaitTimeoutSeconds = 60
	toolName := schema.Tools[0].Name
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
		ID: "call-a", Name: toolName, Arguments: json.RawMessage(`{"prompt":"paint"}`),
	}}}, resume: []agents.Completion{{Content: "recovered task"}}}
	tools := &uncertainAcceptedTool{}
	makeOrchestrator := func() *chat.Orchestrator {
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tools,
		})
		if err != nil {
			t.Fatal(err)
		}
		return orchestrator
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistantID := startTurnFor(t, service, "owner-a", session.ID, "draw")
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistantID}
	first := makeOrchestrator()
	if err := first.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if first.PendingConsentWaits() != 1 {
		t.Fatal("uncertain accepted call was completed instead of suspended")
	}
	turns, err := service.RecoverTurns(ctx)
	if err != nil || len(turns) == 0 {
		t.Fatalf("uncertain call not recoverable: %d %v", len(turns), err)
	}
	if len(turns[0].Checkpoint.Completed) != 0 {
		t.Fatalf("uncertain call marked completed: %+v", turns[0].Checkpoint.Completed)
	}
	restarted := makeOrchestrator()
	if err := restarted.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if tools.calls != 2 {
		t.Fatalf("recovery tool calls=%d", tools.calls)
	}
	if msg := lastAssistant(t, service, "owner-a", session.ID); !strings.Contains(msg.Content, "recovered task") {
		t.Fatalf("recovery did not finish original task: %+v", msg)
	}
}

func (e *suspendConsentToolExecutor) Execute(
	ctx context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if command.Call.Name != "request_consent" {
		return chat.ToolResult{}, nil
	}
	e.mu.Lock()
	e.consents++
	gate := e.resumeConsentGate
	e.mu.Unlock()
	// Only an explicit wake carries the suspension deadline. Startup recovery
	// can replay the plan too, but must suspend immediately like real agenttools.
	// Blocking the second call instead could occupy the only worker before B.
	if !command.ConsentWaitDeadline.IsZero() && gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return chat.ToolResult{}, ctx.Err()
		}
	}
	// 与真实 agenttools 语义一致：恢复重放时超过最长挂起时长仍无
	// 决定 → 返回等待文案收束本轮，不再挂起。
	if !command.ConsentWaitDeadline.IsZero() &&
		!time.Now().Before(command.ConsentWaitDeadline) {
		return chat.ToolResult{AgentContent: "consent wait expired"}, nil
	}
	return chat.ToolResult{Suspend: true}, nil
}

// scriptedSuspendAgent 按 calls 次序回放：先返回同意请求计划，
// 续跑时按需返回后续轮内容。
type scriptedSuspendAgent struct {
	mu       sync.Mutex
	resume   []agents.Completion
	initial  agents.Completion
	idx      int
	streams  int
	maxStale int
}

func (agent *scriptedSuspendAgent) streamsCount() int {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.streams
}

func (agent *scriptedSuspendAgent) Stream(
	_ context.Context,
	_ agents.Request,
	_ func(agents.Delta) error,
) (agents.Completion, error) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.streams++
	if agent.idx == 0 {
		agent.idx++
		return agent.initial, nil
	}
	if len(agent.resume) == 0 {
		return agents.Completion{Content: "idle"}, nil
	}
	next := agent.resume[0]
	agent.resume = agent.resume[1:]
	return next, nil
}

func (agent *scriptedSuspendAgent) Complete(
	_ context.Context,
	_ agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, errors.New("complete unused")
}

func suspendSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 8
	schema.Agent.MaxToolCallsPerReply = 3
	return schema
}

func startTurnFor(
	t *testing.T,
	service *chat.Service,
	owner, sessionID, content string,
) string {
	t.Helper()
	assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID:   owner,
		SessionID: sessionID,
		Content:   content,
	})
	if err != nil {
		t.Fatal(err)
	}
	return assistant.ID
}

func lastAssistant(
	t *testing.T,
	service *chat.Service,
	owner, sessionID string,
) chat.Message {
	t.Helper()
	messages, err := service.ListMessages(context.Background(), owner, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return messages[len(messages)-1]
}

// suspendedCount 只读探测挂起数（导出指标），不消费登记。
func suspendedCount(orchestrator *chat.Orchestrator) int {
	return orchestrator.PendingConsentWaits()
}

func TestOrchestratorConsentSuspendsAndReleasesWorker(t *testing.T) {
	testOrchestratorConsentSuspendsAndReleasesWorker(t, false)
}

func TestOrchestratorRecoveredConsentSuspendsAndReleasesWorker(t *testing.T) {
	testOrchestratorConsentSuspendsAndReleasesWorker(t, true)
}

func testOrchestratorConsentSuspendsAndReleasesWorker(t *testing.T, recoverSuspended bool) {
	t.Helper()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	resumeConsentGate := make(chan struct{})
	tools := &suspendConsentToolExecutor{resumeConsentGate: resumeConsentGate}
	agent := &scriptedSuspendAgent{
		initial: agents.Completion{
			Content: "requesting consent",
			ToolCalls: []agents.ToolCall{{
				ID:        "call-consent-a",
				Name:      "request_consent",
				Arguments: json.RawMessage(`{"reason":"paint"}`),
			}},
		},
		resume: []agents.Completion{{Content: "approved done"}},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessionA, err := service.CreateSession(ctx, "owner-a", "A", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistantA := startTurnFor(t, service, "owner-a", sessionA.ID, "paint a cat")
	commandA := chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          sessionA.ID,
		AssistantMessageID: assistantA,
	}
	if recoverSuspended {
		// Force startup recovery to replay an already suspended plan, without
		// relying on Run's recovery snapshot racing the first worker execution.
		if err := orchestrator.Process(ctx, commandA); err != nil {
			t.Fatal(err)
		}
	}
	runErr := make(chan error, 1)
	go func() { runErr <- orchestrator.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("orchestrator.Run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("orchestrator.Run did not stop")
		}
		// Stop a remaining suspension timer even if an assertion failed.
		orchestrator.ResumeConsentWait("owner-a", sessionA.ID)
	})
	if !recoverSuspended {
		if err := orchestrator.Schedule(ctx, commandA); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && suspendedCount(orchestrator) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if suspendedCount(orchestrator) != 1 {
		t.Fatal("turn A never suspended")
	}
	if recoverSuspended {
		deadline = time.Now().Add(2 * time.Second)
		for {
			tools.mu.Lock()
			consents := tools.consents
			tools.mu.Unlock()
			if consents >= 2 {
				break
			}
			if !time.Now().Before(deadline) {
				t.Fatal("startup recovery did not replay suspended turn A")
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	// 关键断言：A 挂起期间（仅 1 个 worker），会话 B 的完整轮次
	// 立即得到处理——证明挂起不再占用 worker。
	sessionB, err := service.CreateSession(ctx, "owner-b", "B", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistantB := startTurnFor(t, service, "owner-b", sessionB.ID, "hello")
	agent.mu.Lock()
	agent.resume = append([]agents.Completion{{Content: "b reply"}}, agent.resume...)
	agent.mu.Unlock()
	if err := orchestrator.Schedule(ctx, chat.ProcessTurn{
		OwnerID:            "owner-b",
		SessionID:          sessionB.ID,
		AssistantMessageID: assistantB,
	}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := service.GetTurn(ctx, "owner-b", sessionB.ID, assistantB)
		if err != nil {
			t.Fatal(err)
		}
		if msg := lastAssistant(t, service, "owner-b", sessionB.ID); msg.Content == "b reply" &&
			turn.State == "configured-turn-done" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if msg := lastAssistant(t, service, "owner-b", sessionB.ID); msg.Content != "b reply" {
		t.Fatalf("session B did not complete while A suspended: %q", msg.Content)
	}
	if turn, err := service.GetTurn(ctx, "owner-b", sessionB.ID, assistantB); err != nil ||
		turn.State != "configured-turn-done" {
		t.Fatalf("session B did not reach its done state: %q, %v", turn.State, err)
	}

	// 唤醒 A：重放 request_consent 返回挂起标记——但决定已写入时
	// executor 层直接放行；本 fake 始终挂起，因此为让 A 完成需要
	// 走真实 agenttools 组合。这里改为验证幂等唤醒语义后以停止收束。
	if !orchestrator.ResumeConsentWait("owner-a", sessionA.ID) {
		t.Fatal("ResumeConsentWait reported no suspension")
	}
	if orchestrator.ResumeConsentWait("owner-a", sessionA.ID) {
		t.Fatal("second ResumeConsentWait must be a no-op (idempotent)")
	}
	close(resumeConsentGate)
	// 等待 A 续跑重新挂起（fake 会再次 Suspend），然后停止收束。
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && suspendedCount(orchestrator) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if stopped, err := orchestrator.CancelTurn(ctx, "owner-a", sessionA.ID); err != nil || !stopped {
		t.Fatalf("CancelTurn on suspended turn = %v, %v", stopped, err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg := lastAssistant(t, service, "owner-a", sessionA.ID)
		if strings.Contains(msg.Content, schema.Presentation.Messages.TurnStopped) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if msg := lastAssistant(t, service, "owner-a", sessionA.ID); !strings.Contains(
		msg.Content,
		schema.Presentation.Messages.TurnStopped,
	) {
		t.Fatalf("suspended turn not finalized as stopped: %q", msg.Content)
	}
	if suspendedCount(orchestrator) != 0 {
		t.Fatal("consent wait registry not drained")
	}
}

func TestOrchestratorConsentTimeoutFinalizesWithWaiting(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	schema.Agent.ConsentWaitTimeoutSeconds = 1
	tools := &suspendConsentToolExecutor{}
	agent := &scriptedSuspendAgent{
		initial: agents.Completion{
			ToolCalls: []agents.ToolCall{{
				ID:        "call-consent-t",
				Name:      "request_consent",
				Arguments: json.RawMessage(`{}`),
			}},
		},
		resume: []agents.Completion{{Content: " summary after wait"}},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = orchestrator.Run(ctx) }()
	session, err := service.CreateSession(ctx, "owner-t", "T", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant := startTurnFor(t, service, "owner-t", session.ID, "draw")
	if err := orchestrator.Schedule(ctx, chat.ProcessTurn{
		OwnerID:            "owner-t",
		SessionID:          session.ID,
		AssistantMessageID: assistant,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && suspendedCount(orchestrator) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if suspendedCount(orchestrator) != 1 {
		t.Fatal("turn never suspended before timeout")
	}
	// 等待超时唤醒后轮次自然收束（不 FinishStream 判定由 turn 终态）。
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := service.RecoverTurns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		active := false
		for _, turn := range turns {
			if turn.AssistantMessageID == assistant {
				active = true
			}
		}
		if !active {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if suspendedCount(orchestrator) != 0 {
		t.Fatal("timeout did not drain suspension")
	}
	msg := lastAssistant(t, service, "owner-t", session.ID)
	if !strings.Contains(msg.Content, "summary after wait") {
		t.Fatalf("timeout resume did not complete turn: %q", msg.Content)
	}
}

func TestOrchestratorSuspensionBlocksSameSessionNewTurn(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	tools := &suspendConsentToolExecutor{}
	agent := &scriptedSuspendAgent{
		initial: agents.Completion{
			ToolCalls: []agents.ToolCall{{
				ID:        "call-consent-s",
				Name:      "request_consent",
				Arguments: json.RawMessage(`{}`),
			}},
		},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-s", "S", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant := startTurnFor(t, service, "owner-s", session.ID, "draw")
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-s",
		SessionID:          session.ID,
		AssistantMessageID: assistant,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && suspendedCount(orchestrator) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	// 挂起轮次必须保持可恢复：turn 停留在 queued/running（不落终态），
	// 重启后 RecoverTurns 会重新入队续跑。同会话并发新轮次由 sqlite
	// go_chat_streams 条件 UPSERT 拒绝（chat_repository.go；挂起不落
	// 终态即维持 active 流行，条件成立），memory 仓库未实现该拒绝，
	// 不在此断言。
	turns, err := service.RecoverTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range turns {
		if turn.AssistantMessageID == assistant {
			return // 挂起轮次仍在可恢复集合中 ✓
		}
	}
	t.Fatal("suspended turn vanished from recoverable set")
}

// cancelDuringConsentToolExecutor 模拟“用户在工具执行瞬间点停止”：
// 返回 Suspend 标记前先触发一次 CancelTurn（写取消标记）。审查 R2：
// Process 挂起分支必须消费取消标记并按停止收束，而不是挂起后照常
// 续跑计费。
type cancelDuringConsentToolExecutor struct {
	orchestrator *chat.Orchestrator
	owner        string
	session      string
	fired        bool
	mu           sync.Mutex
}

func (e *cancelDuringConsentToolExecutor) Execute(
	ctx context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if command.Call.Name != "request_consent" {
		return chat.ToolResult{}, nil
	}
	e.mu.Lock()
	fired := e.fired
	e.fired = true
	e.mu.Unlock()
	if !fired && e.orchestrator != nil {
		_, _ = e.orchestrator.CancelTurn(ctx, e.owner, e.session)
	}
	return chat.ToolResult{Suspend: true}, nil
}

func TestOrchestratorStopRacingSuspensionFinalizesStopped(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	tools := &cancelDuringConsentToolExecutor{}
	agent := &scriptedSuspendAgent{
		initial: agents.Completion{
			ToolCalls: []agents.ToolCall{{
				ID:        "call-consent-race",
				Name:      "request_consent",
				Arguments: json.RawMessage(`{}`),
			}},
		},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-race", "R", "chat")
	if err != nil {
		t.Fatal(err)
	}
	tools.owner, tools.session = "owner-race", session.ID
	tools.orchestrator = orchestrator
	assistant := startTurnFor(t, service, "owner-race", session.ID, "draw")
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-race",
		SessionID:          session.ID,
		AssistantMessageID: assistant,
	}); err != nil {
		t.Fatal(err)
	}
	// 竞态命中：轮次必须以停止终态收束，而不是挂起等批准。
	if orchestrator.PendingConsentWaits() != 0 {
		t.Fatal("raced suspension must not stay registered")
	}
	msg := lastAssistant(t, service, "owner-race", session.ID)
	if !strings.Contains(msg.Content, schema.Presentation.Messages.TurnStopped) {
		t.Fatalf("raced stop did not finalize stopped: %q", msg.Content)
	}
	// 消费掉取消标记：同会话下一轮不再被残留标记毒化（AGT-01 回归）。
	_, err = service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-race",
		SessionID: session.ID,
		Content:   "again",
	})
	if err != nil {
		t.Fatalf("StartTurn after raced stop: %v", err)
	}
}

// 真实 agenttools 批准唤醒端到端：request_consent 登记挂起 → 写入
// approved 决定 → ResumeConsentWait 重放读到决定 → 轮次继续（审查
// R4 测试缺口：此前两层分开测，组合行为只在 dev-test 手工覆盖）。
func TestOrchestratorApprovalWakeEndToEndWithRealAgentTools(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	state := agentparams.NewStore()
	tools, err := agenttools.New(agenttools.Config{
		Params:                 agentparams.NewStore(),
		Generate:               &wakeFakeGenerate{},
		ConsentApprovedContent: "consent approved",
		ConsentDeniedContent:   "consent denied",
		ConsentWaitingContent:  "consent waiting",
		ConsentBlockedContent:  "consent blocked",
		ConsentRequiredContent: "consent required",
		ConsentLimitContent:    "consent limit",
		ConsentState:           state.Get,
		ConsentClear: func(scope string) {
			state.Delete(scope, agenttools.ConsentPendingKey,
				agenttools.ConsentDecisionKey, agenttools.ConsentApprovedKey)
		},
		ConsentWaitTimeout: time.Minute,
		Consent: func(command chat.ToolCommand, request json.RawMessage, _ json.RawMessage) error {
			scope := agentparams.ScopeKey(command.OwnerID, command.SessionID)
			state.Set(scope, map[string]json.RawMessage{
				agenttools.ConsentPendingKey:  request,
				agenttools.ConsentDecisionKey: json.RawMessage(`"pending"`),
				agenttools.ConsentApprovedKey: json.RawMessage(`false`),
			})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := &scriptedSuspendAgent{
		initial: agents.Completion{
			Content: "requesting approval",
			ToolCalls: []agents.ToolCall{{
				ID:        "e2e-consent-call",
				Name:      "request_consent",
				Arguments: json.RawMessage(`{"reason":"paint"}`),
			}},
		},
		resume: []agents.Completion{{Content: " painted after approval"}},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = orchestrator.Run(ctx) }()
	session, err := service.CreateSession(ctx, "owner-e2e", "E", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant := startTurnFor(t, service, "owner-e2e", session.ID, "draw")
	if err := orchestrator.Schedule(ctx, chat.ProcessTurn{
		OwnerID:            "owner-e2e",
		SessionID:          session.ID,
		AssistantMessageID: assistant,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && orchestrator.PendingConsentWaits() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if orchestrator.PendingConsentWaits() != 1 {
		t.Fatal("turn did not suspend with real agenttools executor")
	}
	// 用户批准（与 HTTP handler 等价的写入）→ 唤醒。
	scope := agentparams.ScopeKey("owner-e2e", session.ID)
	t.Logf("DEBUG scope=%q stored=%v sessionID=%q", scope, state.Get(scope), session.ID)
	state.Set(scope, map[string]json.RawMessage{
		agenttools.ConsentDecisionKey: json.RawMessage(`"approved"`),
	})
	if !orchestrator.ResumeConsentWait("owner-e2e", session.ID) {
		t.Fatal("ResumeConsentWait reported no suspension")
	}
	wakeDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(wakeDeadline) {
		msg := lastAssistant(t, service, "owner-e2e", session.ID)
		if strings.Contains(msg.Content, "painted after approval") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	msg := lastAssistant(t, service, "owner-e2e", session.ID)
	if !strings.Contains(msg.Content, "painted after approval") {
		t.Fatalf("approval wake did not complete the turn: %q", msg.Content)
	}
	if orchestrator.PendingConsentWaits() != 0 {
		t.Fatal("suspension not drained after completion")
	}
}

// wakeFakeGenerate 是 agenttools.Config.Generate 的最小生成工具：
// 批准后的重放路径不会真正执行到它（本轮以文本收尾），仅满足依赖。
type wakeFakeGenerate struct{}

func (*wakeFakeGenerate) Execute(
	_ context.Context,
	_ chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	return chat.ToolResult{}, nil
}
