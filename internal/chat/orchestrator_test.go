package chat_test

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
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestOrchestratorUsesSchemaBoundedQueueAndWorkers(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 1
	agent := &notifyingAgent{done: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}
	if err := orchestrator.Enqueue(command); err != nil {
		t.Fatal(err)
	}
	fullCommand := command
	fullCommand.AssistantMessageID = "configured-other-message"
	if err := orchestrator.Enqueue(fullCommand); !errors.Is(
		err,
		chat.ErrOrchestratorQueueFull,
	) {
		t.Fatalf("second enqueue error = %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- orchestrator.Run(runCtx)
	}()
	select {
	case <-agent.done:
	case <-time.After(time.Second):
		t.Fatal("schema-configured worker did not process queued turn")
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("orchestrator did not stop after cancellation")
	}
}

func TestOrchestratorRunRecoversPersistedTurnAndOptions(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 2
	agent := &scriptedAgent{responses: []agents.Completion{
		{
			Content: "configured plan",
			ToolCalls: []agents.ToolCall{{
				ID:        "configured-call",
				Name:      schema.Tools[0].Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			}},
		},
		{Content: " configured summary"},
	}}
	tools := &recordingToolExecutor{
		commands: make(chan chat.ToolCommand, 1),
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
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
		Options: chat.TurnOptions{
			AIModel:     "configured-generator",
			AspectRatio: "16:9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- orchestrator.Run(runCtx)
	}()
	select {
	case command := <-tools.commands:
		if command.Options.AIModel != "configured-generator" ||
			command.Options.AspectRatio != "16:9" {
			t.Fatalf("recovered command = %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("persisted turn was not recovered")
	}
	deadline := time.Now().Add(time.Second)
	for {
		pending, err := service.RecoverTurns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn remained recoverable: %#v", pending)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
}

func TestOrchestratorPersistsPlanBeforeExecutingGenerationTool(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	agent := &scriptedAgent{responses: []agents.Completion{
		{
			Content: "configured persisted plan",
			ToolCalls: []agents.ToolCall{{
				ID:        "configured-stable-call",
				Name:      schema.Tools[0].Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			}},
		},
		{Content: " configured summary"},
	}}
	tools := &blockingToolExecutor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
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
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID:            "owner-a",
			SessionID:          session.ID,
			AssistantMessageID: assistant.ID,
		})
	}()
	select {
	case <-tools.entered:
	case <-time.After(time.Second):
		t.Fatal("tool was not entered")
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := messages[1]
	if persisted.Content != "configured persisted plan" ||
		len(persisted.ToolCalls) != 1 ||
		persisted.ToolCalls[0].ID != assistant.ID+"_tool_0" {
		t.Fatalf("plan was not durable before tool execution: %#v", persisted)
	}
	close(tools.release)
	if err := <-processDone; err != nil {
		t.Fatal(err)
	}
}

func TestOrchestratorResumesPersistedPlanWithoutRecallingPlanner(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	tools := &recordingToolExecutor{
		commands: make(chan chat.ToolCommand, 1),
	}
	// B4 续跑请求合法地携带工具定义（同一轮继续调用工具），
	// 这里用脚本化 agent 验证摘要来自 follow-up 而非重新规划。
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: " configured resumed summary"},
	}}
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
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.Content = "configured persisted plan"
	assistant.ToolCalls = []chat.ToolCall{{
		ID:        "configured-stable-call",
		Name:      schema.Tools[0].Name,
		Arguments: []byte(`{"prompt":"paint"}`),
	}}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-tools.commands:
		if command.Call.ID != "configured-stable-call" {
			t.Fatalf("command = %#v", command)
		}
	default:
		t.Fatal("persisted tool call was not resumed")
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[1].Content !=
		"configured persisted plan configured resumed summary" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestOrchestratorNeverExposesAgentDiagnostics(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.UnknownError = "configured safe error"
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent: failingAgent{
			err: errors.New("secret upstream diagnostic"),
		},
		Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// #240: the safe text carries only the error reference with the message ID.
	want := "configured safe error" + strings.ReplaceAll(schema.Presentation.Messages.ErrorReference, "{id}", messages[1].ID)
	if messages[1].Content != want || strings.Contains(messages[1].Content, "secret") {
		t.Fatalf("assistant exposed diagnostic: %#v", messages[1])
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Data["configured-content"] == "secret upstream diagnostic" {
			t.Fatalf("SSE exposed diagnostic: %#v", events)
		}
	}
}

func TestOrchestratorBuildsSchemaDrivenVisionContextOnlyForUserImages(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Context.TextPartKind = "configured-text-part"
	schema.Context.ImagePartKind = "configured-image-part"
	schema.Context.ImageDetail = "configured-detail"
	schema.Context.UserImageLabelTemplate = "\nconfigured-user-{index}"
	schema.Context.AIImageLabelTemplate = "\nconfigured-ai-{index}"
	schema.Context.CurrentUploadHint = "configured-hint-{count}-{tool}"
	agent := &scriptedAgent{responses: []agents.Completion{{
		Content: "configured response",
	}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	firstAssistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "first request",
		ImageURLs: []string{
			"data:image/png;base64,Y29uZmlndXJlZA==",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstAssistant.Content = "first response"
	firstAssistant.ImageURLs = []string{"/configured/generated.png"}
	if err := service.UpdateMessage(ctx, "owner-a", firstAssistant); err != nil {
		t.Fatal(err)
	}
	secondAssistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "second request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: secondAssistant.ID,
		Options: chat.TurnOptions{
			CreativeDomain:        "image",
			ReferenceImageIndices: []int{1},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	request := agent.requests[0]
	if !strings.Contains(request.Messages[0].Content, "Use configured 2D tools") {
		t.Fatalf("domain context missing from system prompt: %q", request.Messages[0].Content)
	}
	if len(request.Messages) != 4 {
		t.Fatalf("messages = %#v", request.Messages)
	}
	firstUser := request.Messages[1]
	if len(firstUser.Parts) != 2 ||
		firstUser.Parts[0].Kind != "configured-text-part" ||
		firstUser.Parts[0].Text != "first request" ||
		firstUser.Parts[1].Kind != "configured-image-part" ||
		firstUser.Parts[1].URL !=
			"data:image/png;base64,Y29uZmlndXJlZA==" ||
		firstUser.Parts[1].Detail != "configured-detail" {
		t.Fatalf("first user = %#v", firstUser)
	}
	generatedAssistant := request.Messages[2]
	if len(generatedAssistant.Parts) != 0 ||
		generatedAssistant.Content !=
			"first response\nconfigured-ai-2" {
		t.Fatalf("generated assistant = %#v", generatedAssistant)
	}
	for _, message := range request.Messages {
		if message.Role != schema.Roles.User && len(message.Parts) > 0 {
			t.Fatalf("non-user image parts = %#v", message)
		}
	}
}

func TestOrchestratorGeneratesFirstTurnTitleFromSchemaAndEmitsEvent(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.TitlePrompt =
		"configured title prompt {maximum}: {content}"
	schema.Agent.TitleMaxChars = 5
	schema.Stream.EventTypes.Title = "configured-title-event"
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: "configured response"},
		{Content: "123456789"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "configured request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	orchestrator.WaitForTitles()
	sessions, err := service.ListSessions(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "12345" {
		t.Fatalf("sessions = %#v", sessions)
	}
	if len(agent.requests) != 2 ||
		agent.requests[1].Messages[0].Content !=
			"configured title prompt 5: configured request" {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Data["configured-type"] == "configured-title-event" &&
			event.Data["configured-content"] == "12345" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %#v", events)
	}
}

func TestOrchestratorParsesAndSuppressesFragmentedSchemaDSML(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.DSML = catalog.ChatDSMLSchema{
		Enabled:           true,
		Markers:           []string{"configured-marker-"},
		ToolCallsElements: []string{"calls"},
		InvokeElement:     "run",
		ParameterElement:  "arg",
		NameAttribute:     "key",
		StringAttribute:   "text",
		TrueValue:         "yes",
		FalseValue:        "no",
		CallIDTemplate:    "configured-dsml-{index}",
	}
	raw := "visible<configured-marker-calls>" +
		`<configured-marker-run key="` + schema.Tools[0].Name + `">` +
		`<configured-marker-arg key="prompt" text="yes">paint</configured-marker-arg>` +
		`</configured-marker-run></configured-marker-calls>`
	agent := &fragmentedDSMLAgent{
		raw: raw,
		chunks: []string{
			"vis",
			"ible<configured-",
			"marker-ca",
			"lls>",
		},
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := messages[1]
	if stored.Content != "visible configured summary" ||
		len(stored.ToolCalls) != 1 ||
		stored.ToolCalls[0].ID != assistant.ID+"_tool_0" {
		t.Fatalf("stored = %#v", stored)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		content, _ := event.Data["configured-content"].(string)
		if strings.Contains(content, "configured-marker-") {
			t.Fatalf("DSML leaked through SSE: %#v", events)
		}
	}
}

// 用户停止必须真正中断服务端执行：轮次以停止文案收尾且不伪装成失败，
// 调度前取消的轮次同样落停止终态且不调用 LLM。
func TestOrchestratorUserCancelStopsTurnWithoutFailure(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	agent := &blockingStreamAgent{entered: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw slowly",
	})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID:            "owner-a",
			SessionID:          session.ID,
			AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered
	active, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID)
	if err != nil || !active {
		t.Fatalf("CancelTurn = %v, %v", active, err)
	}
	if err := <-processDone; err != nil {
		t.Fatalf("Process after cancel: %v", err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[1].Content != "configured stopped" {
		t.Fatalf("stopped content = %q", messages[1].Content)
	}

	// 空闲时点停止：没有活动轮次，返回 false 且不写取消标记，
	// 之后的正常轮次不受影响（AGT-01 毒化回归）。
	if active, err := orchestrator.CancelTurn(
		ctx,
		"owner-a",
		session.ID,
	); err != nil || active {
		t.Fatalf("idle CancelTurn = %v, %v", active, err)
	}
	freshAgent := &notifyingAgent{done: make(chan struct{})}
	freshOrchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   freshAgent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "fresh turn after idle stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := freshOrchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: second.ID,
	}); err != nil {
		t.Fatalf("turn after idle stop: %v", err)
	}
	messages, err = service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[3].Content != "configured response" {
		t.Fatalf("turn after idle stop = %q", messages[3].Content)
	}
}

type blockingStreamAgent struct {
	entered chan struct{}
}

func (agent *blockingStreamAgent) Stream(
	ctx context.Context,
	_ agents.Request,
	_ func(agents.Delta) error,
) (agents.Completion, error) {
	select {
	case <-agent.entered:
	default:
		close(agent.entered)
	}
	<-ctx.Done()
	return agents.Completion{}, ctx.Err()
}

func (*blockingStreamAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, context.Canceled
}

// 同会话第二轮 DSML 工具调用的幂等键不得与第一轮相同：
// 幂等模板按会话+调用 ID 组键，若 ID 每轮都从 0 开始，
// 第二轮要么撞幂等冲突、要么静默回放第一轮任务。
func TestOrchestratorToolCallIDsStayUniqueAcrossTurns(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.DSML = catalog.ChatDSMLSchema{
		Enabled:           true,
		Markers:           []string{"configured-marker-"},
		ToolCallsElements: []string{"calls"},
		InvokeElement:     "run",
		ParameterElement:  "arg",
		NameAttribute:     "key",
		StringAttribute:   "text",
		TrueValue:         "yes",
		FalseValue:        "no",
		CallIDTemplate:    "configured-dsml-{index}",
	}
	raw := "visible<configured-marker-calls>" +
		`<configured-marker-run key="` + schema.Tools[0].Name + `">` +
		`<configured-marker-arg key="prompt" text="yes">paint</configured-marker-arg>` +
		`</configured-marker-run></configured-marker-calls>`
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: raw},
		{Content: " configured summary"},
		{Content: raw},
		{Content: " configured summary"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw a cat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: first.ID,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw a dog",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: second.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	firstCall := messages[1].ToolCalls[0].ID
	secondCall := messages[3].ToolCalls[0].ID
	if firstCall != first.ID+"_tool_0" || secondCall != second.ID+"_tool_0" {
		t.Fatalf("tool call IDs = %q, %q", firstCall, secondCall)
	}
}

func TestOrchestratorStreamsAndPersistsTextUsingProductSchema(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	agent := &scriptedAgent{responses: []agents.Completion{{
		Content: "configured response",
	}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}

	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[1].Content != "configured response" {
		t.Fatalf("assistant = %#v", messages[1])
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// #285: the round announces the thinking status before its text.
	if len(events) != 4 ||
		events[1].Data["configured-type"] != schema.Stream.EventTypes.Thinking ||
		events[1].Data["configured-content"] != schema.Presentation.Status.Thinking ||
		events[2].Data["configured-type"] != "configured-text" ||
		events[2].Data["configured-content"] != "configured response" ||
		events[3].Data["configured-type"] != "configured-done" {
		t.Fatalf("events = %#v", events)
	}
}

func TestOrchestratorBridgesAgentToolToTaskEventsAndPersistsBinding(
	t *testing.T,
) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Stream.EventFields.TaskID = "configured-task"
	schema.Stream.EventFields.Status = "configured-status-field"
	schema.Stream.EventFields.Percent = "configured-percent"
	schema.Stream.EventTypes.Status = "configured-status-event"
	schema.Stream.EventTypes.TaskRegistered = "configured-registered"
	schema.Stream.EventTypes.TaskUpdate = "configured-update"
	schema.Stream.EventTypes.Progress = "configured-progress"
	schema.Stream.EventTypes.Images = "configured-images"
	schema.Presentation.Status.Generating = "configured generating"
	agent := &scriptedAgent{responses: []agents.Completion{
		{
			Content: "configured plan",
			ToolCalls: []agents.ToolCall{{
				ID:        "configured-call",
				Name:      schema.Tools[0].Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			}},
		},
		{Content: " configured summary"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := messages[1]
	if stored.Content != "configured plan configured summary" ||
		len(stored.ImageURLs) != 1 ||
		stored.ImageURLs[0] != "/configured/result.png" ||
		len(stored.ToolCalls) != 1 ||
		stored.ToolCalls[0].TaskID != "configured-task-final" {
		t.Fatalf("stored assistant = %#v", stored)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var foundStatus, foundRegistered, foundImages bool
	for _, event := range events {
		switch event.Data["configured-type"] {
		case "configured-status-event":
			foundStatus = event.Data["configured-content"] ==
				"configured generating"
		case "configured-registered":
			foundRegistered = event.Data["configured-task"] ==
				"configured-task-stable"
		case "configured-images":
			foundImages = true
		}
	}
	if !foundStatus || !foundRegistered || !foundImages {
		t.Fatalf("events = %#v", events)
	}
}

func configuredChatSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	schema.Stream.EventFields.Type = "configured-type"
	schema.Stream.EventFields.Content = "configured-content"
	schema.Stream.EventTypes.Text = "configured-text"
	schema.Stream.EventTypes.Done = "configured-done"
	schema.Roles.System = "configured-system"
	schema.Roles.User = "configured-user"
	schema.Roles.Assistant = "configured-assistant"
	schema.Roles.Tool = "configured-tool"
	schema.State.PendingContent = "configured-pending"
	schema.Defaults.Title = "configured default title"
	schema.Agent.SystemPrompt = "configured-system-prompt"
	schema.Agent.DomainContext = map[string]string{"image": "Use configured 2D tools"}
	return schema
}

type scriptedAgent struct {
	responses []agents.Completion
	requests  []agents.Request
	index     int
}

type notifyingAgent struct {
	done chan struct{}
}

func (agent *notifyingAgent) Stream(
	_ context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	if emit != nil {
		if err := emit(agents.Delta{Content: "configured response"}); err != nil {
			return agents.Completion{}, err
		}
	}
	select {
	case <-agent.done:
	default:
		close(agent.done)
	}
	return agents.Completion{Content: "configured response"}, nil
}

func (*notifyingAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

type failingAgent struct {
	err error
}

type fragmentedDSMLAgent struct {
	raw    string
	chunks []string
	calls  int
}

type resumeAgent struct {
	summary string
}

func (agent resumeAgent) Stream(
	_ context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	if len(request.Tools) > 0 {
		return agents.Completion{}, errors.New(
			"planner must not be recalled for a persisted plan",
		)
	}
	if err := emit(agents.Delta{Content: agent.summary}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: agent.summary}, nil
}

func (resumeAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

func (agent *fragmentedDSMLAgent) Stream(
	_ context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	agent.calls++
	if agent.calls == 1 {
		for _, chunk := range agent.chunks {
			if err := emit(agents.Delta{Content: chunk}); err != nil {
				return agents.Completion{}, err
			}
		}
		return agents.Completion{Content: agent.raw}, nil
	}
	if err := emit(agents.Delta{Content: " configured summary"}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: " configured summary"}, nil
}

func (*fragmentedDSMLAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

func (agent failingAgent) Stream(
	context.Context,
	agents.Request,
	func(agents.Delta) error,
) (agents.Completion, error) {
	return agents.Completion{}, agent.err
}

func (agent failingAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, agent.err
}

func (agent *scriptedAgent) Stream(
	_ context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	agent.requests = append(agent.requests, request)
	response := agent.responses[agent.index]
	agent.index++
	if response.Content != "" && emit != nil {
		if err := emit(agents.Delta{Content: response.Content}); err != nil {
			return agents.Completion{}, err
		}
	}
	return response, nil
}

func (agent *scriptedAgent) Complete(
	_ context.Context,
	request agents.Request,
) (agents.Completion, error) {
	agent.requests = append(agent.requests, request)
	response := agent.responses[agent.index]
	agent.index++
	return response, nil
}

type noToolExecutor struct{}

func (noToolExecutor) Execute(
	context.Context,
	chat.ToolCommand,
	func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	return chat.ToolResult{}, nil
}

type scriptedToolExecutor struct{}

func (scriptedToolExecutor) Execute(
	_ context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if err := observe(chat.ToolObservation{
		Registered: true,
		TaskID:     "configured-task-stable",
		Capability: string(command.Tool.Capability),
		Prompt:     "paint",
		AIModel:    "configured-generator",
	}); err != nil {
		return chat.ToolResult{}, err
	}
	if err := observe(chat.ToolObservation{
		TaskID:   "configured-task-stable",
		Status:   "configured-running",
		Progress: 42,
	}); err != nil {
		return chat.ToolResult{}, err
	}
	if err := observe(chat.ToolObservation{
		TaskID:      "configured-task-stable",
		FinalTaskID: "configured-task-final",
		Status:      "configured-succeeded",
		Progress:    100,
		ImageURLs:   []string{"/configured/result.png"},
	}); err != nil {
		return chat.ToolResult{}, err
	}
	return chat.ToolResult{
		TaskID:       "configured-task-final",
		ImageURLs:    []string{"/configured/result.png"},
		AgentContent: `{"configured":"result"}`,
	}, nil
}

type recordingToolExecutor struct {
	commands chan chat.ToolCommand
}

type blockingToolExecutor struct {
	entered chan struct{}
	release chan struct{}
}

func (executor *blockingToolExecutor) Execute(
	_ context.Context,
	_ chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	close(executor.entered)
	<-executor.release
	return chat.ToolResult{AgentContent: `{}`}, nil
}

func (executor *recordingToolExecutor) Execute(
	_ context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	executor.commands <- command
	return chat.ToolResult{AgentContent: `{}`}, nil
}

func TestOrchestratorInlinesOwnedAssetURLsIntoVisionParts(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Context.TextPartKind = "configured-text-part"
	schema.Context.ImagePartKind = "configured-image-part"
	schema.Context.ImageDetail = "configured-detail"
	schema.Context.UserImageLabelTemplate = "\nconfigured-user-{index}"
	schema.Context.AIImageLabelTemplate = "\nconfigured-ai-{index}"
	schema.Context.CurrentUploadHint = "configured-hint-{count}-{tool}"
	agent := &scriptedAgent{responses: []agents.Completion{{
		Content: "configured response",
	}}}
	inlined := map[string]string{}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   noToolExecutor{},
		VisionInline: func(
			_ context.Context,
			owner generation.OwnerID,
			url string,
		) (string, bool) {
			inlined[url] = string(owner)
			if url == "/api/v1/assets/owned" {
				return "data:image/png;base64,QQ==", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "edit these",
		ImageURLs: []string{
			"/api/v1/assets/owned",
			"/api/v1/tasks/other/assets/foreign",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
		Options:            chat.TurnOptions{CreativeDomain: "image"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	user := agent.requests[0].Messages[1]
	if len(user.Parts) != 3 ||
		user.Parts[1].Kind != "configured-image-part" ||
		user.Parts[1].URL != "data:image/png;base64,QQ==" {
		t.Fatalf("vision parts = %#v", user.Parts)
	}
	if !strings.Contains(user.Content, "configured-user-2") {
		t.Fatalf("unresolvable image must stay a label: %q", user.Content)
	}
	if inlined["/api/v1/assets/owned"] != "owner-a" {
		t.Fatalf("inline owner = %#v", inlined)
	}
}

// B4 有界工具循环：工具结果后模型可在同一轮内继续调用工具；
// 每轮调用 ID 唯一、调用与产物累积、续跑请求携带工具定义。
func TestOrchestratorToolLoopContinuesWithinRoundLimit(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolCallsPerReply = 2
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{
			ID:        "gateway-1",
			Name:      schema.Tools[0].Name,
			Arguments: []byte(`{"prompt":"one"}`),
		}}},
		{ToolCalls: []agents.ToolCall{{
			ID:        "gateway-2",
			Name:      schema.Tools[0].Name,
			Arguments: []byte(`{"prompt":"two"}`),
		}}},
		{Content: " final summary"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw twice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := messages[1]
	if stored.Content != " final summary" {
		t.Fatalf("content = %q", stored.Content)
	}
	if len(stored.ToolCalls) != 2 ||
		stored.ToolCalls[0].ID != assistant.ID+"_tool_0" ||
		stored.ToolCalls[1].ID != assistant.ID+"_r1_tool_0" {
		t.Fatalf("tool calls = %#v", stored.ToolCalls)
	}
	for _, call := range stored.ToolCalls {
		if call.TaskID != "configured-task-final" {
			t.Fatalf("binding missing: %#v", call)
		}
	}
	if len(agent.requests) != 3 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	// 续跑请求必须携带工具定义，模型才能在同意获批后继续调用。
	if len(agent.requests[1].Tools) == 0 {
		t.Fatal("follow-up request dropped tools")
	}
}

// 达到轮数上限后不再执行后续工具调用：未执行调用从消息移除，
// 内容折叠为 Schema 声明的上限提示。
func TestOrchestratorToolLoopStopsAtRoundLimit(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolCallsPerReply = 1
	schema.Presentation.Messages.ToolRoundLimit = "configured round limit"
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{
			ID:        "gateway-1",
			Name:      schema.Tools[0].Name,
			Arguments: []byte(`{"prompt":"one"}`),
		}}},
		{ToolCalls: []agents.ToolCall{{
			ID:        "gateway-2",
			Name:      schema.Tools[0].Name,
			Arguments: []byte(`{"prompt":"two"}`),
		}}},
		// AGT-04：命中轮数上限后的无工具总结调用。
		{Content: "configured summary of failures"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw once",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := messages[1]
	if len(stored.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", stored.ToolCalls)
	}
	if !strings.Contains(stored.Content, "configured round limit") {
		t.Fatalf("content = %q", stored.Content)
	}
	if !strings.Contains(stored.Content, "configured summary of failures") {
		t.Fatalf("summary missing from content = %q", stored.Content)
	}
	if len(agent.requests) != 3 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	// 总结请求不得携带工具定义：模型不能再发起会超限的调用。
	if len(agent.requests[2].Tools) != 0 {
		t.Fatalf("summary request tools = %#v", agent.requests[2].Tools)
	}
}

// B14 领域过滤：2D 会话不收 3D 工具；越域工具调用按未知工具回执，
// 保持 tool_calls 配对完整。
func TestOrchestratorFiltersToolsByCreativeDomain(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	configured3DTool := schema.Tools[2]
	configured3DTool.Name = "configured-3d-tool"
	configured3DTool.Description = "3d generation"
	schema.Tools = append(schema.Tools, configured3DTool)
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: "domain filtered"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
		ToolAllowed: func(capability, domain string) bool {
			return capability != "text-to-3d"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
		Options:   chat.TurnOptions{CreativeDomain: "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
		Options:            chat.TurnOptions{CreativeDomain: "image"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range agent.requests[0].Tools {
		if tool.Name == "configured-3d-tool" {
			t.Fatalf("3D tool leaked into an image session: %#v", agent.requests[0].Tools)
		}
	}
}

// scriptedStreamFuncs 按次执行注入的流式函数，并记录收到的请求，
// 用于断言续跑请求的上下文累积（AGT-02）与停止语义（AGT-01/06）。
type scriptedStreamFuncs struct {
	mu       sync.Mutex
	streams  []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error)
	requests []agents.Request
}

func (agent *scriptedStreamFuncs) Stream(
	ctx context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	agent.mu.Lock()
	index := len(agent.requests)
	agent.requests = append(agent.requests, request)
	stream := agent.streams[index]
	agent.mu.Unlock()
	return stream(ctx, emit)
}

func (agent *scriptedStreamFuncs) Complete(
	_ context.Context,
	_ agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, errors.New("Complete 不在本测试范围内")
}

func newScriptedStreamOrchestrator(
	t *testing.T,
	schema catalog.ChatSchema,
	streams []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error),
	executor chat.ToolExecutor,
	taskCanceler func(ctx context.Context, ownerID, taskID string) error,
) (*chat.Orchestrator, *chat.Service, *scriptedStreamFuncs) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	agent := &scriptedStreamFuncs{streams: streams}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service:      service,
		Schema:       schema,
		Model:        "configured-model",
		Agent:        agent,
		Tools:        executor,
		TaskCanceler: taskCanceler,
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator, service, agent
}

func orchestratorRunTurn(
	t *testing.T,
	orchestrator *chat.Orchestrator,
	service *chat.Service,
	ownerID string,
	sessionID string,
	content string,
) (string, error) {
	t.Helper()
	assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID:   ownerID,
		SessionID: sessionID,
		Content:   content,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = orchestrator.Process(context.Background(), chat.ProcessTurn{
		OwnerID:            ownerID,
		SessionID:          sessionID,
		AssistantMessageID: assistant.ID,
	})
	return assistant.ID, err
}

// 停止一轮后，同一会话的下一轮必须照常执行（AGT-01：取消标记
// 不得残留毒化下一轮）。
func TestOrchestratorCancelDoesNotPoisonNextTurn(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	ctx := context.Background()
	streams := []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error){
		func(ctx context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			<-ctx.Done()
			return agents.Completion{}, ctx.Err()
		},
		func(_ context.Context, emit func(agents.Delta) error) (agents.Completion, error) {
			if emit != nil {
				if err := emit(agents.Delta{Content: "second ok"}); err != nil {
					return agents.Completion{}, err
				}
			}
			return agents.Completion{Content: "second ok"}, nil
		},
	}
	orchestrator, service, _ := newScriptedStreamOrchestrator(
		t,
		schema,
		streams,
		scriptedToolExecutor{},
		nil,
	)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw slowly",
	})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID:            "owner-a",
			SessionID:          session.ID,
			AssistantMessageID: first.ID,
		})
	}()
	time.Sleep(50 * time.Millisecond)
	if active, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil || !active {
		t.Fatalf("CancelTurn = %v, %v", active, err)
	}
	if err := <-processDone; err != nil {
		t.Fatalf("first turn: %v", err)
	}
	second, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: second.ID,
	}); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages[3].Content, "second ok") {
		t.Fatalf("next turn poisoned: %q", messages[3].Content)
	}
}

// 续跑请求必须累积此前各轮的 assistant 调用与 tool 回执（AGT-02），
// 否则模型每轮失忆、重复调用同一工具直到耗尽轮数上限。
func TestOrchestratorFollowUpRequestsAccumulateToolRounds(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolCallsPerReply = 3
	toolCall := agents.ToolCall{
		Name:      schema.Tools[0].Name,
		Arguments: []byte(`{"prompt":"paint"}`),
	}
	streams := []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error){
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{ToolCalls: []agents.ToolCall{toolCall}}, nil
		},
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{ToolCalls: []agents.ToolCall{toolCall}}, nil
		},
		func(_ context.Context, emit func(agents.Delta) error) (agents.Completion, error) {
			if emit != nil {
				if err := emit(agents.Delta{Content: " final"}); err != nil {
					return agents.Completion{}, err
				}
			}
			return agents.Completion{Content: " final"}, nil
		},
	}
	ctx := context.Background()
	orchestrator, service, agent := newScriptedStreamOrchestrator(
		t,
		schema,
		streams,
		scriptedToolExecutor{},
		nil,
	)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	requests := agent.requests
	if len(requests) != 3 {
		t.Fatalf("requests = %d", len(requests))
	}
	if len(requests[1].Messages) != len(requests[0].Messages)+2 {
		t.Fatalf("follow-up 1 messages = %d, base = %d",
			len(requests[1].Messages), len(requests[0].Messages))
	}
	if len(requests[2].Messages) != len(requests[1].Messages)+2 {
		t.Fatalf("follow-up 2 lost context: %d vs %d",
			len(requests[2].Messages), len(requests[1].Messages))
	}
	// 第三次请求必须同时包含第一轮与第二轮的 tool 回执。
	toolReceipts := 0
	for _, message := range requests[2].Messages {
		if message.Role == schema.Roles.Tool {
			toolReceipts++
		}
	}
	if toolReceipts != 2 {
		t.Fatalf("follow-up 2 tool receipts = %d", toolReceipts)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(messages[1].Content, "final") {
		t.Fatalf("final content = %q", messages[1].Content)
	}
}

// 停止活动轮次必须取消已绑定的在飞任务（AGT-06 计费止损），
// 且观察失败返回空 TaskID 时不得清空已持久化的绑定。
func TestOrchestratorStopCancelsBoundInFlightTasks(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	bound := make(chan struct{})
	streams := []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error){
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{ToolCalls: []agents.ToolCall{{
				Name:      schema.Tools[0].Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			}}}, nil
		},
		func(ctx context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			<-ctx.Done()
			return agents.Completion{}, ctx.Err()
		},
	}
	executor := &bindingOnlyToolExecutor{
		bound:   bound,
		release: make(chan struct{}),
	}
	canceled := make(chan string, 4)
	canceler := func(_ context.Context, _, taskID string) error {
		canceled <- taskID
		return nil
	}
	orchestrator, service, _ := newScriptedStreamOrchestrator(
		t,
		schema,
		streams,
		executor,
		canceler,
	)
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(context.Background(), chat.ProcessTurn{
			OwnerID:            "owner-a",
			SessionID:          session.ID,
			AssistantMessageID: assistant.ID,
		})
	}()
	select {
	case <-bound:
	case <-time.After(2 * time.Second):
		t.Fatal("tool never bound a task")
	}
	// 进程此刻必然阻塞在工具执行内部：停止语义与绑定取消都是确定的。
	if active, err := orchestrator.CancelTurn(context.Background(), "owner-a", session.ID); err != nil || !active {
		t.Fatalf("CancelTurn = %v, %v", active, err)
	}
	close(executor.release)
	if err := <-processDone; err != nil {
		t.Fatalf("stopped turn: %v", err)
	}
	select {
	case taskID := <-canceled:
		if taskID != "configured-task-final" {
			t.Fatalf("canceled task = %q", taskID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bound task was never canceled")
	}
	messages, err := service.ListMessages(context.Background(), "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages[1].ToolCalls) != 1 ||
		messages[1].ToolCalls[0].TaskID != "configured-task-final" {
		t.Fatalf("binding cleared: %#v", messages[1].ToolCalls)
	}
}

// bindingOnlyToolExecutor 只通过观察回调绑定任务，随后阻塞到测试
// 放行并返回空 TaskID：复现"绑定已持久化、结果为空"的观察失败路径，
// 已持久化的绑定不得被空结果清空。
type bindingOnlyToolExecutor struct {
	bound   chan struct{}
	release chan struct{}
}

func (executor *bindingOnlyToolExecutor) Execute(
	_ context.Context,
	_ chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if err := observe(chat.ToolObservation{
		Registered: true,
		TaskID:     "configured-task-final",
	}); err != nil {
		return chat.ToolResult{}, err
	}
	select {
	case <-executor.bound:
	default:
		close(executor.bound)
	}
	<-executor.release
	return chat.ToolResult{}, nil
}

// Vendors such as DeepSeek reject a tool-loop follow-up that omits the
// assistant's reasoning_content (400). Each follow-up therefore keeps the
// reasoning of every earlier round, while the user-visible reply never does.
func TestOrchestratorFollowUpCarriesReasoningContent(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolCallsPerReply = 3
	toolCall := agents.ToolCall{
		Name:      schema.Tools[0].Name,
		Arguments: []byte(`{"prompt":"paint"}`),
	}
	streams := []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error){
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{ToolCalls: []agents.ToolCall{toolCall}, ReasoningContent: "private plan one"}, nil
		},
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{ToolCalls: []agents.ToolCall{toolCall}, ReasoningContent: "private plan two"}, nil
		},
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{Content: "done", ReasoningContent: "private plan three"}, nil
		},
	}
	ctx := context.Background()
	orchestrator, service, agent := newScriptedStreamOrchestrator(t, schema, streams, scriptedToolExecutor{}, nil)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	reasoningOf := func(request agents.Request) []string {
		var out []string
		for _, message := range request.Messages {
			if message.Role == schema.Roles.Assistant && len(message.ToolCalls) > 0 {
				out = append(out, message.ReasoningContent)
			}
		}
		return out
	}
	requests := agent.requests
	if len(requests) != 3 {
		t.Fatalf("requests = %d", len(requests))
	}
	if got := reasoningOf(requests[1]); strings.Join(got, "|") != "private plan one" {
		t.Fatalf("follow-up 1 reasoning = %q", got)
	}
	if got := reasoningOf(requests[2]); strings.Join(got, "|") != "private plan one|private plan two" {
		t.Fatalf("follow-up 2 reasoning = %q", got)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messages[1].Content, "private plan") {
		t.Fatalf("reasoning leaked into the reply: %q", messages[1].Content)
	}
}

// The checkpoint resumes a suspended tool loop (for example after approval);
// it must keep the reasoning the follow-up has to send back.
func TestTurnCheckpointKeepsReasoningContent(t *testing.T) {
	checkpoint := chat.TurnCheckpoint{
		Request:    agents.Request{Messages: []agents.Message{{Role: "assistant", ReasoningContent: "earlier plan"}}},
		Completion: agents.Completion{ReasoningContent: "current plan"},
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chat.TurnCheckpoint
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Request.Messages[0].ReasoningContent != "earlier plan" || decoded.Completion.ReasoningContent != "current plan" {
		t.Fatalf("checkpoint lost reasoning: %+v", decoded)
	}
}

// #253: a deployment without Agent settings answers every turn with the
// Schema's not-configured text (never the unknown error), and the
// orchestrator starts without a model name for that stand-in only.
func TestOrchestratorSaysWhenTheAgentIsNotConfigured(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.UnknownError = "configured safe error"
	schema.Presentation.Messages.AgentNotConfigured = "configured not-configured text"
	if _, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Agent: failingAgent{err: errors.New("x")}, Tools: noToolExecutor{},
	}); err == nil {
		t.Fatal("configured Agent without model accepted")
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema,
		Agent: agents.NotConfigured{Missing: []string{"LLM_API_KEY"}},
		Tools: noToolExecutor{},
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
	orchestrator.WaitForTitles()
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(messages[1].Content, "configured not-configured text") || strings.Contains(messages[1].Content, "LLM_API_KEY") {
		t.Fatalf("assistant = %#v", messages[1])
	}
}

// #253: when the Agent model refused a request that carried pictures, the
// person is told the model may not read images, still without diagnostics.
func TestOrchestratorSaysWhenTheModelRefusedPictures(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.UnknownError = "configured safe error"
	schema.Presentation.Messages.AgentImagesUnsupported = "configured no-vision text"
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model",
		Agent: failingAgent{err: errors.Join(agents.ErrImagesRejected, errors.New("secret upstream diagnostic"))},
		Tools: noToolExecutor{},
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
	if !strings.HasPrefix(messages[1].Content, "configured no-vision text") || strings.Contains(messages[1].Content, "secret") {
		t.Fatalf("assistant = %#v", messages[1])
	}
}
