package openairesponses_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	// The stream is cut at the picture's first sign, so a call or text before
	// it does not make the round an answer: what follows was never read.
	withCall := frames(
		"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"lookup_note","arguments":"{}"}}`,
		"response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"image_generation_call","status":"in_progress"}}`,
	) + oversizedCompleted()
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", withCall)
	if _, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello, Tools: lookup}, nil); !errors.Is(err, agents.ErrInlineImageOutput) {
		t.Fatalf("with a call: %v", err)
	}
}

// #638: the picture is billed as a generated image and its result runs to
// megabytes, so the Adapter stops reading and drops the connection at the
// first image_generation_call sign instead of downloading the rest. The
// upstream here sends the item added, then holds the stream open: the call
// returns at once, and the upstream sees its client go away.
func TestAPictureTheModelDrawsItselfCutsTheStream(t *testing.T) {
	for name, opening := range map[string]string{
		"item added": frames("response.output_item.added",
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"image_generation_call","status":"in_progress"}}`),
		"progress event": frames("response.image_generation_call.generating",
			`{"type":"response.image_generation_call.generating","output_index":0}`),
	} {
		gone := make(chan struct{})
		rest := make(chan bool, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, opening)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				close(gone)
				rest <- false
			case <-time.After(5 * time.Second):
				// Only an Adapter still reading gets the picture.
				_, _ = io.WriteString(w, frames("response.output_item.done",
					`{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","result":"AAAA"}}`))
				rest <- true
			}
		}))
		adapter := newAdapter(t, server.URL, &recorder{}, nil)
		started := time.Now()
		_, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello}, nil)
		if !errors.Is(err, agents.ErrInlineImageOutput) || time.Since(started) > 2*time.Second {
			t.Fatalf("%s: after %s: %v", name, time.Since(started), err)
		}
		select {
		case <-gone:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the upstream never saw the connection dropped", name)
		}
		if <-rest {
			t.Fatalf("%s: the rest of the stream was sent", name)
		}
		server.Close()
	}
}
