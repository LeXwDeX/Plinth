package openairesponses_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// oversizedCompleted is a response.completed longer than the stream line bound
// (1 MiB in the tests' Config): the terminal event of the #638 rounds, which
// answered 7 to 9 MB. Its output is never read, so it carries none.
func oversizedCompleted() string {
	return frames("response.completed", `{"type":"response.completed","response":{"status":"completed","instructions":"`+
		strings.Repeat("i", (1<<20)+4096)+`","output":[],"usage":{"input_tokens":1,"output_tokens":2}}}`)
}

// #638: a terminal event over the line bound loses only what it alone carried
// (the usage). The message and the call the item events closed are kept, in
// output order, with the reasoning that led to the call.
func TestAnOversizedTerminalEventKeepsTheItemsTheStreamClosed(t *testing.T) {
	body := frames(
		"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","summary":[],"encrypted_content":"enc"}}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"I split it in three."}]}}`,
		"response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"c1","name":"lookup_note"}}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","call_id":"c1","name":"lookup_note","arguments":"{\"topic\":\"cube\"}"}}`,
	) + oversizedCompleted()
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	completion, deltas, err := collect(adapter, agents.Request{Model: "gpt-astra", Messages: userHello, Tools: lookup})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "I split it in three." || texts(deltas) != completion.Content {
		t.Fatalf("content = %q deltas = %#v", completion.Content, deltas)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "c1" ||
		string(completion.ToolCalls[0].Arguments) != `{"topic":"cube"}` {
		t.Fatalf("calls = %#v", completion.ToolCalls)
	}
	if completion.ReasoningContent == "" || !strings.Contains(completion.ReasoningContent, "enc") {
		t.Fatalf("the reasoning closed by its done item was lost: %q", completion.ReasoningContent)
	}
	if completion.Usage != nil {
		t.Fatalf("usage from a skipped line: %#v", completion.Usage)
	}
}

// #638 reproduction: a round that only reasoned, ended by an oversized
// response.completed, is an empty completion without a usage. The Adapter
// returns it as it is (no text, no call); telling it from a real end is the
// caller's (agents.Retrying and the chat orchestrator).
func TestAReasoningOnlyRoundWithAnOversizedTerminalEventIsEmpty(t *testing.T) {
	body := frames(
		"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","summary":[],"encrypted_content":"enc"}}`,
	) + oversizedCompleted()
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	completion, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !completion.Empty() || completion.Usage != nil {
		t.Fatalf("completion = %#v", completion)
	}
}

// #638: a model that drew its pictures itself (the Responses wire's
// image_generation_call items, a base64 result of megabytes on the done item
// and again on the terminal event) said nothing the reply can hold. Like the
// Chat Completions Adapter's delta.images, that ends with
// ErrInlineImageOutput instead of an empty completion; the signs are the item
// added, an image_generation_call event, or an oversized line that names it.
func TestAPictureTheModelDrewItselfIsInlineImageOutput(t *testing.T) {
	picture := strings.Repeat("A", (1<<20)+4096)
	for name, body := range map[string]string{
		"item added": frames(
			"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"image_generation_call","status":"in_progress"}}`,
			"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","status":"completed","result":"`+picture+`"}}`,
		) + oversizedCompleted(),
		"progress event": frames(
			"response.image_generation_call.in_progress", `{"type":"response.image_generation_call.in_progress","output_index":0}`,
		) + oversizedCompleted(),
		"oversized done item only": frames(
			"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","status":"completed","result":"`+picture+`"}}`,
		) + oversizedCompleted(),
	} {
		adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
		if _, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello}, nil); !errors.Is(err, agents.ErrInlineImageOutput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A picture beside a call or text is dropped; the rest is the answer.
	withCall := frames(
		"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"image_generation_call","status":"in_progress"}}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"lookup_note","arguments":"{}"}}`,
	) + oversizedCompleted()
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", withCall)
	completion, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello, Tools: lookup}, nil)
	if err != nil || len(completion.ToolCalls) != 1 {
		t.Fatalf("with a call: %#v %v", completion, err)
	}
}
