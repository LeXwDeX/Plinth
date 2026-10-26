package mock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

// #333: "[mock:markdown]" makes the mock Agent answer in Markdown, the way
// real models often do, so the chat's Markdown rendering and its XSS cases
// run end to end on the mock stack. The reply streams in several pieces, one
// of them cut inside a bold run, and names the handles the person wrote.
func TestMockMarkdownMarkerAnswersInStreamedMarkdown(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "done",
		UserRole: "user", ToolRole: "tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	completion, err := adapter.Stream(context.Background(), agents.Request{
		Tools:    []agents.Tool{{Name: "refine_image"}},
		Messages: []agents.Message{{Role: "user", Content: "用 Markdown 说说 @img1 和 @model2 [mock:markdown]"}},
	}, func(delta agents.Delta) error {
		deltas = append(deltas, delta.Content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 {
		t.Fatalf("tool calls = %#v, want a text reply", completion.ToolCalls)
	}
	if len(deltas) < 4 {
		t.Fatalf("deltas = %d, want the reply streamed in pieces", len(deltas))
	}
	if strings.Join(deltas, "") != completion.Content {
		t.Fatalf("streamed %q, completed %q", strings.Join(deltas, ""), completion.Content)
	}
	cutInsideBold := false
	for _, delta := range deltas[:len(deltas)-1] {
		if strings.Count(delta, "**")%2 == 1 {
			cutInsideBold = true
		}
	}
	if !cutInsideBold {
		t.Fatalf("no piece ends inside a bold run: %q", deltas)
	}
	for _, want := range []string{
		"**", "*", "`", "```", "1. ", "- ", "](https://", "javascript:", "data:text/html",
		"<script>", "onerror=", "&lt;", "![", "**@img1**", "@model2",
	} {
		if !strings.Contains(completion.Content, want) {
			t.Errorf("reply lacks %q:\n%s", want, completion.Content)
		}
	}
	if strings.Contains(completion.Content, "[mock:") {
		t.Fatalf("reply repeats the marker:\n%s", completion.Content)
	}
}

// Without the marker, and after a tool result, the mock keeps its usual path.
func TestMockMarkdownMarkerOnlyAnswersTheMarkedTurn(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "done",
		UserRole: "user", ToolRole: "tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := adapter.Stream(context.Background(), agents.Request{
		Tools:    []agents.Tool{{Name: "refine_image"}},
		Messages: []agents.Message{{Role: "user", Content: "画一只猫"}},
	}, nil)
	if err != nil || len(plain.ToolCalls) != 1 {
		t.Fatalf("plain turn = %#v %v", plain, err)
	}
}
