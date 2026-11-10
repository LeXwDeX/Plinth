package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #249: an approval or resume has no in-memory layer trace, but the prepared
// input still carries the exact prompt that was approved. The rebuilt report
// must name that prompt, not the call's original text.
func TestPromptTraceFromPreparedRestoresTheFrozenPrompt(t *testing.T) {
	executor := &Executor{chat: catalog.ChatSchema{Context: catalog.ChatContextSchema{ToolPromptArgument: "prompt"}}}
	offering := generation.Offering{Parameters: []generation.Parameter{{
		Name: "prompt", Semantic: generation.SemanticPrompt, MaxLength: 80,
	}}}
	command := chat.ToolCommand{
		Call:    agents.ToolCall{Arguments: []byte(`{"prompt":"original"}`)},
		Options: chat.TurnOptions{},
	}
	trace := executor.promptTraceFromPrepared(command, offering, []byte(`{"prompt":"templated original"}`))
	if trace == nil || trace.Final != "templated original" || trace.Original != "original" {
		t.Fatalf("trace = %#v", trace)
	}
	if len(trace.Layers) != 1 || trace.Layers[0].ID != promptLayerPreparedInput {
		t.Fatalf("layers = %#v", trace.Layers)
	}
	if trace.MaxLength != 80 {
		t.Fatalf("max length = %d", trace.MaxLength)
	}
	// An unchanged prompt reports nothing.
	unchanged := executor.promptTraceFromPrepared(command, offering, []byte(`{"prompt":"original"}`))
	if unchanged == nil || unchanged.changed() {
		t.Fatalf("unchanged trace = %#v", unchanged)
	}
}
