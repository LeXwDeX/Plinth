package openairesponses_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// A stream that stops before its last event is a cut one, whatever it said
// before: half an answer or half the arguments of a call must not pass as a
// whole one. A [DONE] line (some compatible servers send one) ends a stream
// that carries a terminal event, and a read error is the error returned.
func TestAStreamThatEndsEarlyIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"after a delta": frames("response.created", `{"type":"response.created"}`,
			"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"half an answ"}`),
		"empty":         "",
		"only comments": ": keep-alive\n\n",
		"[DONE] first":  "data: [DONE]\n\n",
		"call cut mid-arguments": frames("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c","name":"n"}}`,
			"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a"}`),
	} {
		adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
		_, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
		if err == nil || !strings.Contains(err.Error(), "ended before the response completed") {
			t.Errorf("%s: %v", name, err)
		}
	}
	done, _ := scripted(t, http.StatusOK, "text/event-stream", frames(
		"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"ok"}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed"}}`)+"data: [DONE]\n\n")
	if completion, err := done.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil); err != nil || completion.Content != "ok" {
		t.Fatalf("with [DONE]: %#v %v", completion, err)
	}
	for name, body := range map[string]string{
		"not JSON":     "event: response.output_text.delta\ndata: {not json}\n\n",
		"bad known":    "data: {\"type\":\"response.output_text.delta\",\"delta\":5}\n\n",
		"bad item":     "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":\"text\"}\n\n",
		"bad response": "data: {\"type\":\"response.completed\",\"response\":{\"output\":[1]}}\n\n",
	} {
		adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
		if _, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil); err == nil || !strings.Contains(err.Error(), "decode agent") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A frame without data: or with the type only in the event: line.
	byEvent, _ := scripted(t, http.StatusOK, "text/event-stream",
		"event: response.output_text.delta\ndata: {\"delta\":\"typed by the event line\"}\n\nevent: response.completed\ndata: {\"response\":{\"status\":\"completed\"}}\n\n")
	if completion, err := byEvent.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil); err != nil || completion.Content != "typed by the event line" {
		t.Fatalf("event line: %#v %v", completion, err)
	}
}

// What a done item says wins over the fragments streamed before it, the done
// events settle a call whose item never closed, and the output of the terminal
// event settles every item the stream left open.
func TestDoneItemsWinOverStreamedFragments(t *testing.T) {
	body := frames(
		// A message whose streamed text differs from its done item.
		"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"par"}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"tial"}`,
		"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"the whole answer"},{"type":"refusal","refusal":" (declined)"}]}}`,
		// A call settled by its arguments.done event only.
		"response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"one","arguments":""}}`,
		"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"x\":"}`,
		"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"x\":1}"}`,
		// A call whose arguments only streamed, on a response that completed.
		"response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"c2","name":"two","arguments":""}}`,
		"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{}"}`,
		// A call left open that the terminal event's output closes with other arguments.
		"response.output_item.added", `{"type":"response.output_item.added","output_index":3,"item":{"type":"function_call","call_id":"c3","name":"three","arguments":""}}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"message","content":[{"type":"output_text","text":"the whole answer"},{"type":"refusal","refusal":" (declined)"}]},`+
			`{"type":"function_call","call_id":"c1","name":"one","arguments":"{\"x\":1}"},`+
			`{"type":"function_call","call_id":"c2","name":"two","arguments":"{}"},`+
			`{"type":"function_call","call_id":"c3","name":"three","arguments":"{\"z\":3}"}]}}`)
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	var live []string
	completion, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, func(delta agents.Delta) error {
		live = append(live, delta.Content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "the whole answer (declined)" || strings.Join(live, "") != "partial" {
		t.Fatalf("content = %q, streamed live = %q", completion.Content, live)
	}
	var got []string
	for _, call := range completion.ToolCalls {
		got = append(got, call.ID+" "+call.Name+" "+string(call.Arguments))
	}
	if strings.Join(got, "|") != `c1 one {"x":1}|c2 two {}|c3 three {"z":3}` {
		t.Fatalf("calls = %v", got)
	}
}

// An upstream that sends the whole message only in its done item (no deltas)
// still delivers the text to the caller, once.
func TestTextThatWasNeverStreamedIsDeliveredOnce(t *testing.T) {
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", frames(
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"message","content":[{"type":"output_text","text":"all at once"}]}],"usage":{"input_tokens":1,"output_tokens":2}}}`))
	completion, deltas, err := collect(adapter, agents.Request{Model: "m", Messages: userHello})
	if err != nil || completion.Content != "all at once" || len(deltas) != 1 || deltas[0].Content != "all at once" ||
		completion.Usage == nil || completion.Usage.CompletionTokens != 2 {
		t.Fatalf("completion = %#v deltas = %#v %v", completion, deltas, err)
	}
}

// The response objects of a stream echo the instructions and the tools, so a
// large system prompt makes response.created, response.in_progress and
// response.completed longer than the line bound. The Adapter skips such a line
// instead of failing the stream: the answer comes from the item events, and a
// completed event that was too long ends the response without a usage.
func TestOversizedResponseObjectsDoNotFailTheStream(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	big := strings.Repeat("a", (1<<20)+4096)
	request := agents.Request{Model: "gpt-astra", Tools: lookup, Messages: []agents.Message{
		{Role: "system", Content: big},
		userMessage("go " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))}}
	completion, err := adapter.Stream(ctx, request, nil)
	if err != nil || len(completion.ToolCalls) != 1 || string(completion.ToolCalls[0].Arguments) != `{"topic":"cube"}` {
		t.Fatalf("tool call: %#v %v", completion, err)
	}
	if completion.Usage != nil {
		t.Fatalf("the usage came from a line that was skipped: %#v", completion.Usage)
	}
	request.Messages[1] = userMessage("plain")
	text, deltas, err := collect(adapter, request)
	if err != nil || text.Content != openaiv1.FixedReply || texts(deltas) != openaiv1.FixedReply {
		t.Fatalf("text: %#v %v", text, err)
	}
	// A failed or in-stream error event that was too long is a failure, with
	// its details lost with the line.
	longFailure := frames("response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"`+strings.Repeat("x", 1<<20)+`"}}}`)
	failing, _ := scripted(t, http.StatusOK, "text/event-stream", longFailure)
	if _, err := failing.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil); err == nil || !strings.Contains(err.Error(), "longer than the stream line bound") {
		t.Fatalf("long failure: %v", err)
	}
	// The same prompt on the non-stream path exceeds the body bound: an error
	// that says so, not a truncated read.
	if _, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{{Role: "system", Content: big}, userMessage("hi")}}); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("complete: %v", err)
	}
	// A tool call whose item events fit the bound survives a skipped
	// response.completed even when it was the only thing said.
	if request.Messages[0].Content != big {
		t.Fatal("test setup")
	}
}

// A model's refusal is its answer to the person, so it is shown as text.
func TestARefusalIsShownAsText(t *testing.T) {
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", frames(
		"response.refusal.delta", `{"type":"response.refusal.delta","output_index":0,"delta":"I can't help "}`,
		"response.refusal.delta", `{"type":"response.refusal.delta","output_index":0,"delta":"with that."}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[]}}`))
	completion, deltas, err := collect(adapter, agents.Request{Model: "m", Messages: userHello})
	if err != nil || completion.Content != "I can't help with that." || texts(deltas) != completion.Content {
		t.Fatalf("completion = %#v %v", completion, err)
	}
}

// A read error in the middle of the stream is the error returned.
func TestAStreamReadErrorIsReturned(t *testing.T) {
	server := newBrokenStream(t)
	adapter := newAdapter(t, server, &recorder{}, nil)
	_, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
	if err == nil || errors.Is(err, errors.New("x")) || !strings.Contains(err.Error(), "read agent stream") {
		t.Fatalf("err = %v", err)
	}
}

// New options reach the wire through OptionsFor only: a nil function and a nil
// result send the request as a model without a profile gets it.
func TestNoOptionsSendsTheTemperatureAndNoReasoning(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(func(config *openairesponses.Config) { config.OptionsFor = nil })
	if _, err := adapter.Complete(ctx, agents.Request{Model: "gpt-4o", Temperature: 0, ReasoningEffort: "high", Messages: userHello}); err != nil {
		t.Fatal(err)
	}
	if got := s.recorder.raw(0); got != `{"model":"gpt-4o","input":[{"role":"user","content":"hello"}],"stream":false,"store":false,"temperature":0}` {
		t.Fatalf("request = %s", got)
	}
}

// A reasoning item whose done event never came is taken from the output of
// response.completed, and one whose done event came keeps its own token (the
// gateway's output carried another value for the same item).
func TestReasoningTokenComesFromTheDoneEventElseFromTheOutput(t *testing.T) {
	for name, test := range map[string]struct {
		done  string // the done event of the reasoning item, or ""
		token string
	}{
		"closed by its done event": {frames("response.output_item.done",
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"content":[],"encrypted_content":"token-of-the-done-event"}}`), "token-of-the-done-event"},
		"closed by the output only": {"", "token-of-the-output"},
	} {
		body := frames("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"content":[],"encrypted_content":"token-of-the-added-event"}}`) +
			test.done + frames("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"reasoning","id":"rs_1","summary":[],"content":[],"encrypted_content":"token-of-the-output"}]}}`)
		adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
		completion, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
		if err != nil || !strings.Contains(completion.ReasoningContent, `"encrypted_content":"`+test.token+`"`) ||
			strings.Contains(completion.ReasoningContent, "token-of-the-added-event") {
			t.Errorf("%s: %#v %v", name, completion, err)
		}
	}
}

// #579: the model's refusal (the fake's RefusalMarker: a refusal content part
// streamed as response.refusal.delta and response.refusal.done) reaches the
// person as the answer text, streamed and whole.
func TestARefusalIsTheAnswerText(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.RefusalMarker)}}
	text, deltas, err := collect(adapter, request)
	if err != nil || text.Content != openaiv1.FixedRefusal || texts(deltas) != openaiv1.FixedRefusal {
		t.Fatalf("stream: %#v %q %v", text, texts(deltas), err)
	}
	whole, err := adapter.Complete(ctx, request)
	if err != nil || whole.Content != openaiv1.FixedRefusal {
		t.Fatalf("complete: %#v %v", whole, err)
	}
}
