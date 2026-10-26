package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #275: once the tool rounds run out, the tool-less summary round may still
// write DSML (here the spaced "calls" variant, split across chunks). The
// markup must neither stream to the person nor be stored; the reply says the
// tool budget is spent and lists, from Schema texts, the tools the model still
// wanted and what for.
func TestOrchestratorSummaryRoundStripsDSMLAndListsPendingCalls(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.DSML.Enabled = true
	schema.Agent.MaxToolCallsPerReply = 1
	schema.Context.ToolPromptPreviewChars = 12
	messages := &schema.Presentation.Messages
	messages.ToolRoundLimit = "configured round limit"
	messages.ToolRoundLimitPending = "configured pending:"
	messages.ToolRoundLimitPendingItem = "* {tool} -> {intent}"
	messages.ToolRoundLimitPendingTool = "* {tool}"
	tool := schema.Tools[0].Name
	summary := []string{
		"configured summary text\n<｜",
		"｜DSML｜｜ ca",
		"lls>\n<｜｜DSML｜｜ invoke name=\"" + tool + "\">\n",
		"<｜｜DSML｜｜ parameter name=\"prompt\" string=\"true\">sharpen   the\nlogo edges please</｜｜DSML｜｜ parameter>\n",
		"</｜｜DSML｜｜ invoke>\n<｜｜DSML｜｜ invoke name=\"list_models\">\n</｜｜DSML｜｜ invoke>\n</｜｜DSML｜｜ calls>",
	}
	agent := &roundLimitSummaryAgent{
		calls: []agents.Completion{
			{ToolCalls: []agents.ToolCall{{ID: "gateway-1", Name: tool, Arguments: []byte(`{"prompt":"one"}`)}}},
			{ToolCalls: []agents.ToolCall{{ID: "gateway-2", Name: tool, Arguments: []byte(`{"prompt":"two"}`)}}},
		},
		summary: summary,
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
		Content:   "refine it",
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
	stored, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	content := stored[1].Content
	for _, want := range []string{
		"configured summary text",
		"configured round limit\nconfigured pending:\n" +
			"* " + tool + " -> two\n" +
			"* " + tool + " -> sharpen the\n" +
			"* list_models",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
	if strings.Contains(content, "DSML") || len(stored[1].ToolCalls) != 1 {
		t.Fatalf("stored = %#v", stored[1])
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, event := range events {
		if text, _ := event.Data["configured-content"].(string); event.Data["configured-type"] == "configured-text" {
			streamed.WriteString(text)
		}
	}
	if !strings.Contains(streamed.String(), "configured summary text") ||
		strings.ContainsAny(streamed.String(), "<｜") {
		t.Fatalf("streamed = %q", streamed.String())
	}
}

type roundLimitSummaryAgent struct {
	calls   []agents.Completion
	summary []string
	index   int
}

func (agent *roundLimitSummaryAgent) Stream(
	_ context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	defer func() { agent.index++ }()
	if agent.index < len(agent.calls) {
		return agent.calls[agent.index], nil
	}
	if len(request.Tools) != 0 {
		panic("summary round must not offer tools")
	}
	for _, chunk := range agent.summary {
		if err := emit(agents.Delta{Content: chunk}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: strings.Join(agent.summary, "")}, nil
}

func (*roundLimitSummaryAgent) Complete(
	context.Context,
	agents.Request,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}
