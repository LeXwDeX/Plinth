package openairesponses_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// wireSignals renders the deltas a caller saw as the Chat Completions tests do
// for #375: "thinking", "tool_calling" or "text:<content>", joined by "|". A
// signal never carries content.
func wireSignals(t *testing.T, deltas []agents.Delta) string {
	t.Helper()
	var seen []string
	for _, delta := range deltas {
		if (delta.Thinking || delta.ToolCalling) && delta.Content != "" {
			t.Fatalf("signal delta carries content %q", delta.Content)
		}
		if delta.Thinking {
			seen = append(seen, "thinking")
		}
		if delta.ToolCalling {
			seen = append(seen, "tool_calling")
		}
		if delta.Content != "" {
			seen = append(seen, "text:"+delta.Content)
		}
	}
	return strings.Join(seen, "|")
}

// TestAdapterReportsTheStartOfAToolCallAsAContentFreeSignal pins #431, the
// Responses counterpart of #375: the model reasons and then writes a function
// call (the shape of the 2026-11-20 sample: a reasoning item, one
// output_item.added of the function_call item, its arguments fragments and
// done events), so the caller sees the thinking signal and then one
// content-free tool-calling signal. The signal is per round: a second stream
// reports again. The assembled call is untouched.
func TestAdapterReportsTheStartOfAToolCallAsAContentFreeSignal(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Tools: lookup, Messages: []agents.Message{
		userMessage(openaiv1.ReasoningMarker + " " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))}}
	for round := 1; round <= 2; round++ {
		completion, deltas, err := collect(adapter, request)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if got := wireSignals(t, deltas); got != "thinking|tool_calling" {
			t.Fatalf("round %d: deltas = %s", round, got)
		}
		if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "lookup_note" ||
			string(completion.ToolCalls[0].Arguments) != `{"topic":"cube"}` {
			t.Fatalf("round %d: calls = %#v", round, completion.ToolCalls)
		}
	}
}

// TestAdapterSignalsParallelCallsOnce pins that two calls, each its own run of
// events at output_index 0 and 1, are one tool-calling signal, not two, and
// that both calls still assemble.
func TestAdapterSignalsParallelCallsOnce(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Tools: lookup, Messages: []agents.Message{
		userMessage(openaiv1.ToolMarkerFor("lookup_note", `{"topic":"one"}`) + " " +
			openaiv1.ToolMarkerFor("lookup_note", `{"topic":"two"}`))}}
	completion, deltas, err := collect(adapter, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := wireSignals(t, deltas); got != "tool_calling" {
		t.Fatalf("deltas = %s", got)
	}
	if len(completion.ToolCalls) != 2 ||
		string(completion.ToolCalls[0].Arguments) != `{"topic":"one"}` ||
		string(completion.ToolCalls[1].Arguments) != `{"topic":"two"}` {
		t.Fatalf("calls = %#v", completion.ToolCalls)
	}
}

// TestAdapterSendsNoToolCallingSignalForAPlainAnswer pins that an answer without
// a call streams only its text: no status is announced for a call that is not
// there.
func TestAdapterSendsNoToolCallingSignalForAPlainAnswer(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, deltas, err := collect(adapter, agents.Request{Model: "gpt-astra", Messages: userHello})
	if err != nil {
		t.Fatal(err)
	}
	if texts(deltas) != openaiv1.FixedReply || len(deltas) == 0 {
		t.Fatalf("deltas = %v", deltas)
	}
	for _, delta := range deltas {
		if delta.Thinking || delta.ToolCalling {
			t.Fatalf("a plain answer announced a signal: %#v", delta)
		}
	}
	if completion.Content != openaiv1.FixedReply || len(completion.ToolCalls) != 0 {
		t.Fatalf("completion = %#v", completion)
	}
}

// TestAdapterSignalsACallWhoseArgumentsStreamWithoutItsAddedEvent pins the
// second sign: a stream that starts at the first arguments fragment (its
// function_call item never seen, for example a line over the bound) still
// reports the call, once, and the call assembles.
func TestAdapterSignalsACallWhoseArgumentsStreamWithoutItsAddedEvent(t *testing.T) {
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", frames(
		"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{}"}`,
		"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{}"}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"function_call","call_id":"c1","name":"one","arguments":"{}"}]}}`))
	completion, deltas, err := collect(adapter, agents.Request{Model: "m", Messages: userHello})
	if err != nil {
		t.Fatal(err)
	}
	if got := wireSignals(t, deltas); got != "tool_calling" {
		t.Fatalf("deltas = %s", got)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "c1" {
		t.Fatalf("calls = %#v", completion.ToolCalls)
	}
}

// TestAdapterDoesNotSignalACallThatNeverStreamed pins that a call delivered
// only by the terminal event has no start to report: the caller sees the call
// when it is already whole.
func TestAdapterDoesNotSignalACallThatNeverStreamed(t *testing.T) {
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", frames(
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"function_call","call_id":"c1","name":"one","arguments":"{}"}]}}`))
	completion, deltas, err := collect(adapter, agents.Request{Model: "m", Messages: userHello})
	if err != nil {
		t.Fatal(err)
	}
	if got := wireSignals(t, deltas); got != "" || len(completion.ToolCalls) != 1 {
		t.Fatalf("deltas = %q calls = %#v", got, completion.ToolCalls)
	}
}
