package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #402: one reply can take several model rounds (the Agent narrates, calls a
// tool, narrates the result). The public message and the stream used to join
// the rounds' text with nothing between, so "我先出一张概念图。" and "概念图好了。"
// ran together. A paragraph break now separates the visible text of adjacent
// rounds, and the stream and the stored message agree on it: the text events,
// joined, are the stored text.

type narrationRound402 struct {
	chunks []string // the deltas of the round's text, as the stream delivers them
	calls  int      // tool calls the round ends with
}

type narrationAgent402 struct {
	mu       sync.Mutex
	rounds   []narrationRound402
	index    int
	toolName string
}

func (agent *narrationAgent402) Stream(_ context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	if agent.index >= len(agent.rounds) {
		agent.mu.Unlock()
		return agents.Completion{}, errors.New("narration agent: no scripted round left")
	}
	round, index := agent.rounds[agent.index], agent.index
	agent.index++
	agent.mu.Unlock()
	for _, chunk := range round.chunks {
		if err := emit(agents.Delta{Content: chunk}); err != nil {
			return agents.Completion{}, err
		}
	}
	completion := agents.Completion{Content: strings.Join(round.chunks, "")}
	for call := 0; call < round.calls; call++ {
		completion.ToolCalls = append(completion.ToolCalls, agents.ToolCall{
			ID: fmt.Sprintf("gateway-%d-%d", index, call), Name: agent.toolName, Arguments: []byte(`{"prompt":"p"}`),
		})
	}
	return completion, nil
}

func (agent *narrationAgent402) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "标题"}, nil
}

// runNarration402 runs one reply of the scripted rounds and returns the text
// the public message stores and the text its stream carried.
func runNarration402(t *testing.T, rounds []narrationRound402) (stored, streamed string) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   &narrationAgent402{rounds: rounds, toolName: schema.Tools[0].Name},
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
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	orchestrator.WaitForTitles()
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, event := range events {
		if event.Data[schema.Stream.EventFields.Type] == schema.Stream.EventTypes.Text {
			piece, _ := event.Data[schema.Stream.EventFields.Content].(string)
			text.WriteString(piece)
		}
	}
	return messages[1].Content, text.String()
}

func TestOrchestratorSeparatesTheNarrationOfAdjacentRounds_402(t *testing.T) {
	for _, test := range []struct {
		name   string
		rounds []narrationRound402
		want   string
	}{
		{
			"both rounds have text",
			[]narrationRound402{{chunks: []string{"我先出一张概念图。"}, calls: 1}, {chunks: []string{"概念图好了。"}}},
			"我先出一张概念图。\n\n概念图好了。",
		},
		{
			"the text arrives in pieces",
			[]narrationRound402{{chunks: []string{"我先", "出一张概念图。"}, calls: 1}, {chunks: []string{"概念图", "好了。"}}},
			"我先出一张概念图。\n\n概念图好了。",
		},
		{
			"three rounds of text",
			[]narrationRound402{
				{chunks: []string{"第一步。"}, calls: 1}, {chunks: []string{"第二步。"}, calls: 1}, {chunks: []string{"第三步。"}},
			},
			"第一步。\n\n第二步。\n\n第三步。",
		},
		{
			// A round that only calls a tool has no text to separate from.
			"the first round has no text",
			[]narrationRound402{{calls: 1}, {chunks: []string{"概念图好了。"}}},
			"概念图好了。",
		},
		{
			"a round in the middle has no text",
			[]narrationRound402{{chunks: []string{"第一步。"}, calls: 1}, {calls: 1}, {chunks: []string{"第三步。"}}},
			"第一步。\n\n第三步。",
		},
		{
			"a round of only blank text adds no break of its own",
			[]narrationRound402{{chunks: []string{"第一步。"}, calls: 1}, {chunks: []string{"\n\n"}, calls: 1}, {chunks: []string{"第三步。"}}},
			"第一步。\n\n第三步。",
		},
		{
			// The text so far already ends with a line break: nothing is added.
			"the text so far ends with a line break",
			[]narrationRound402{{chunks: []string{"第一步。\n"}, calls: 1}, {chunks: []string{"第二步。"}}},
			"第一步。\n第二步。",
		},
		{
			"the text so far ends with a blank line",
			[]narrationRound402{{chunks: []string{"第一步。\n\n"}, calls: 1}, {chunks: []string{"第二步。"}}},
			"第一步。\n\n第二步。",
		},
		{
			// The line breaks the later round opens with are the separation.
			"the later round opens with a blank line",
			[]narrationRound402{{chunks: []string{"第一步。"}, calls: 1}, {chunks: []string{"\n", "\n第二步。"}}},
			"第一步。\n\n第二步。",
		},
		{
			// Whitespace the later round opens with stays where the model put it,
			// ahead of the break, so the stream and the stored text agree.
			"the later round opens with a space",
			[]narrationRound402{{chunks: []string{"第一步。"}, calls: 1}, {chunks: []string{" ", "第二步。"}}},
			"第一步。 \n\n第二步。",
		},
		{
			"a reply of one round is unchanged",
			[]narrationRound402{{chunks: []string{"只有一轮。"}}},
			"只有一轮。",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored, streamed := runNarration402(t, test.rounds)
			if stored != test.want {
				t.Fatalf("stored text = %q, want %q", stored, test.want)
			}
			if streamed != stored {
				t.Fatalf("the stream carried %q but the message stores %q", streamed, stored)
			}
		})
	}
}
