package openairesponses_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// A streamed answer in text: the deltas add up to the answer, the usage is the
// upstream's, and the request is exactly what the real gateway accepted in the
// probe of 2026-11-20 (instructions from the system message, a user message
// with string content, reasoning.effort, store false) and nothing else: no
// temperature (the gpt-astra profile omits it), no tools, no include.
func TestStreamsATextAnswerAndSendsTheObservedRequestShape(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, deltas, err := collect(adapter, agents.Request{
		Model: "gpt-astra", Temperature: 0, ReasoningEffort: "xhigh",
		Messages: []agents.Message{{Role: "system", Content: "Be brief."}, userMessage("hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != openaiv1.FixedReply || texts(deltas) != openaiv1.FixedReply || len(deltas) < 2 {
		t.Fatalf("completion %q, %d deltas %q", completion.Content, len(deltas), texts(deltas))
	}
	if len(completion.ToolCalls) != 0 || completion.ReasoningContent != "" {
		t.Fatalf("completion = %#v", completion)
	}
	if completion.Usage == nil || completion.Usage.PromptTokens != 22 || completion.Usage.CompletionTokens != 9 {
		t.Fatalf("usage = %#v", completion.Usage)
	}
	const want = `{"model":"gpt-astra","instructions":"Be brief.","input":[{"role":"user","content":"hello"}],` +
		`"stream":true,"store":false,"reasoning":{"effort":"xhigh"}}`
	if got := s.recorder.raw(0); got != want {
		t.Fatalf("request:\n got %s\nwant %s", got, want)
	}
}

// A tool call: the arguments arrive in pieces and are joined, the call keeps
// the upstream's call_id and name, and the request carries the flat function
// tool, the strict flag, the encrypted reasoning include and the profile's
// default level.
func TestStreamsAToolCallWithItsArguments(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, deltas, err := collect(adapter, agents.Request{
		Model: "gpt-astra", Tools: lookup,
		Messages: []agents.Message{userMessage("note " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "lookup_note" ||
		completion.ToolCalls[0].ID != "call_fake_0" || string(completion.ToolCalls[0].Arguments) != `{"topic":"cube"}` ||
		completion.Content != "" {
		t.Fatalf("completion = %#v deltas = %v", completion, deltas)
	}
	// The call's start is the one signal of the round (#431).
	if len(deltas) != 1 || !deltas[0].ToolCalling || deltas[0].Thinking || deltas[0].Content != "" {
		t.Fatalf("deltas = %v", deltas)
	}
	const wantTools = `"tools":[{"type":"function","name":"lookup_note","description":"d","parameters":{"type":"object"},"strict":false}],` +
		`"stream":true,"store":false,"include":["reasoning.encrypted_content"],"reasoning":{"effort":"high"}}`
	if got := s.recorder.raw(0); !strings.HasSuffix(got, wantTools) {
		t.Fatalf("request tail:\n got %s\nwant ...%s", got, wantTools)
	}
}

// The tool loop of an Agent turn over two rounds, as the orchestrator builds
// it: the second request carries the assistant message with the call and the
// tool message with its result, and the function_call and its output meet on
// the call_id the Port carries, which the orchestrator rewrites (#275), not on
// the upstream's.
func TestToolLoopSendsTheCallAndItsOutputOnTheSameCallID(t *testing.T) {
	for name, rewrite := range map[string]func(string) string{
		"the upstream's call id": func(id string) string { return id },
		"a rewritten call id":    func(string) string { return "msg_1_r0_tool_0" },
	} {
		t.Run(name, func(t *testing.T) {
			s := newStack(t)
			adapter := s.adapter(nil)
			request := agents.Request{
				Model: "gpt-astra", Tools: lookup, ReasoningEffort: "medium",
				Messages: []agents.Message{{Role: "system", Content: "rules"}, userMessage("go " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))},
			}
			first, err := adapter.Stream(ctx, request, nil)
			if err != nil || len(first.ToolCalls) != 1 {
				t.Fatalf("round 1: %#v %v", first, err)
			}
			first.ToolCalls[0].ID = rewrite(first.ToolCalls[0].ID)
			request.Messages = append(request.Messages,
				agents.Message{Role: "assistant", ToolCalls: first.ToolCalls, ReasoningContent: first.ReasoningContent},
				agents.Message{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: `{"note":"a cube"}`})
			second, deltas, err := collect(adapter, request)
			if err != nil {
				t.Fatalf("round 2 (the fake refuses a call and output that do not pair): %v", err)
			}
			if second.Content != openaiv1.FixedReply || texts(deltas) != openaiv1.FixedReply || len(second.ToolCalls) != 0 {
				t.Fatalf("round 2 = %#v", second)
			}
			input := s.recorder.body(t, 0)["input"].([]any)
			if len(input) != 3 {
				t.Fatalf("input = %v", input)
			}
			call, output := input[1].(map[string]any), input[2].(map[string]any)
			callID := first.ToolCalls[0].ID
			if call["type"] != "function_call" || call["call_id"] != callID || call["name"] != "lookup_note" ||
				call["arguments"] != `{"topic":"cube"}` || call["id"] != nil || call["status"] != nil {
				t.Fatalf("function_call = %v", call)
			}
			if output["type"] != "function_call_output" || output["call_id"] != callID || output["output"] != `{"note":"a cube"}` {
				t.Fatalf("function_call_output = %v", output)
			}
			if body := s.recorder.body(t, 0); body["instructions"] != "rules" || input[0].(map[string]any)["role"] != "user" {
				t.Fatalf("request = %v", body)
			}
		})
	}
}

// Two independent calls in one reply: both come back in order with their own
// call_id, and both outputs go back (in the order the tools finished).
func TestParallelToolCallsAreReturnedInOrderAndAnsweredTogether(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(func(config *openairesponses.Config) { config.ParallelToolCalls = true })
	tools := append(append([]agents.Tool{}, lookup...), agents.Tool{Name: "get_weather", Parameters: json.RawMessage(`{"type":"object"}`)})
	request := agents.Request{Model: "gpt-astra", Tools: tools, Messages: []agents.Message{userMessage("go " +
		openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`) + " " + openaiv1.ToolMarkerFor("get_weather", `{"city":"Paris"}`))}}
	first, err := adapter.Stream(ctx, request, nil)
	if err != nil || len(first.ToolCalls) != 2 {
		t.Fatalf("round 1: %#v %v", first, err)
	}
	if first.ToolCalls[0].Name != "lookup_note" || first.ToolCalls[1].Name != "get_weather" ||
		first.ToolCalls[0].ID == first.ToolCalls[1].ID || string(first.ToolCalls[1].Arguments) != `{"city":"Paris"}` {
		t.Fatalf("calls = %#v", first.ToolCalls)
	}
	if body := s.recorder.body(t, 0); body["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls missing: %v", body)
	}
	request.Messages = append(request.Messages,
		agents.Message{Role: "assistant", ToolCalls: first.ToolCalls},
		agents.Message{Role: "tool", ToolCallID: first.ToolCalls[1].ID, Content: "sunny"},
		agents.Message{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: "a cube"})
	second, err := adapter.Stream(ctx, request, nil)
	if err != nil || second.Content != openaiv1.FixedReply {
		t.Fatalf("round 2: %#v %v", second, err)
	}
	items := s.recorder.body(t, 0)["input"].([]any)
	var kinds []string
	for _, item := range items[1:] {
		kinds = append(kinds, item.(map[string]any)["type"].(string))
	}
	if strings.Join(kinds, ",") != "function_call,function_call,function_call_output,function_call_output" {
		t.Fatalf("input items = %v", kinds)
	}
	if got := items[3].(map[string]any)["call_id"]; got != first.ToolCalls[1].ID {
		t.Fatalf("outputs are not sent in the order given: %v", items)
	}
}

// Without the option, no parallel_tool_calls member is sent, and none is sent
// on a request without tools.
func TestParallelToolCallsIsSentOnlyWhenEnabledAndWithTools(t *testing.T) {
	s := newStack(t)
	enabled := s.adapter(func(config *openairesponses.Config) { config.ParallelToolCalls = true })
	plain := s.adapter(nil)
	for name, call := range map[string]func() error{
		"enabled, no tools": func() error {
			_, err := enabled.Complete(ctx, agents.Request{Model: "m", Messages: userHello})
			return err
		},
		"disabled, tools": func() error {
			_, err := plain.Complete(ctx, agents.Request{Model: "m", Messages: userHello, Tools: lookup})
			return err
		},
	} {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, sent := s.recorder.body(t, 0)["parallel_tool_calls"]; sent {
			t.Errorf("%s: parallel_tool_calls is sent: %s", name, s.recorder.raw(0))
		}
	}
}

// A picture goes as an input_image part with its detail, beside the text parts;
// the fake records it the way it records a chat picture.
func TestSendsPicturesAsInputImageParts(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	picture := "data:image/png;base64,AAAA"
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: "look"},
		{Kind: "image_url", URL: picture, Detail: "low"},
		{Kind: "text", Text: "@img1"},
		{Kind: "image_url", URL: picture},
		{Kind: "text"}, // an empty part is not sent
	}}}}
	if _, err := adapter.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	const want = `"input":[{"role":"user","content":[{"type":"input_text","text":"look"},` +
		`{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"low"},` +
		`{"type":"input_text","text":"@img1"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]`
	if got := s.recorder.raw(0); !strings.Contains(got, want) {
		t.Fatalf("request:\n got %s\nwant ...%s", got, want)
	}
	records := s.fake.Env().ChatRequests()
	if len(records) != 1 || len(records[0].Messages) != 1 || len(records[0].Messages[0].Images) != 2 ||
		records[0].Messages[0].Images[0].Detail != "low" || records[0].Messages[0].Images[0].Bytes != 3 {
		t.Fatalf("the fake saw %+v", records)
	}
	// A refusal of a request with pictures that names image input is the
	// images-rejected class, as on Chat Completions (#253).
	refusing, _ := scripted(t, http.StatusBadRequest, "application/json",
		`{"error":{"type":"invalid_request_error","param":"input[0].content[1]","message":"this model does not support image input"}}`)
	if _, err := refusing.Complete(ctx, request); !errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("err = %v", err)
	}
	if _, err := refusing.Complete(ctx, agents.Request{Model: "m", Messages: userHello}); errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("a request without pictures is not an images refusal: %v", err)
	}
}

// Every request path of Plinth goes through the Adapter and none of them sends
// a temperature to the model whose profile omits it, whatever the request
// carries; a model without the option gets it as the request carries it.
func TestEveryRequestPathLeavesTheTemperatureOutWhenTheProfileOmitsIt(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	picture := agents.Message{Role: "user", Parts: []agents.ContentPart{{Kind: "text", Text: "look"}, {Kind: "image_url", URL: "data:image/png;base64,AAAA"}}}
	for name, send := range map[string]func(float64) error{
		"agent turn": func(temperature float64) error {
			_, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Tools: lookup, Messages: userHello}, nil)
			return err
		},
		"title": func(temperature float64) error {
			_, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Temperature: temperature, Messages: []agents.Message{{Role: "system", Content: "title"}, userMessage("a robot cat")}})
			return err
		},
		"summary": func(temperature float64) error {
			_, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "low", Purpose: agents.PurposeSummary, Messages: userHello})
			return err
		},
		"connection test": func(temperature float64) error {
			_, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "low", Messages: []agents.Message{picture}})
			return err
		},
	} {
		for _, temperature := range []float64{0, 0.7} {
			if err := send(temperature); err != nil {
				t.Fatalf("%s (%v): %v", name, temperature, err)
			}
			if raw := s.recorder.raw(0); strings.Contains(raw, "temperature") {
				t.Errorf("%s (%v): the request carries a temperature: %s", name, temperature, raw)
			}
		}
	}
	// Another model: the temperature is sent, 0 included, and no reasoning member.
	for _, temperature := range []float64{0, 0.7} {
		if _, err := adapter.Complete(ctx, agents.Request{Model: "deepseek", Temperature: temperature, ReasoningEffort: "high", Messages: userHello}); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"model":"deepseek","input":[{"role":"user","content":"hello"}],"stream":false,"store":false,"temperature":%v}`, temperature)
		if got := s.recorder.raw(0); got != want {
			t.Errorf("another model:\n got %s\nwant %s", got, want)
		}
	}
}

// The level a request chooses is sent when the profile offers it, else the
// profile's default; a model without levels sends no reasoning member.
func TestReasoningEffortFollowsTheProfileLevels(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	for chosen, want := range map[string]string{"low": "low", "max": "max", "": "high", "none": "high", "extreme": "high"} {
		if _, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", ReasoningEffort: chosen, Messages: userHello}); err != nil {
			t.Fatalf("level %q: %v", chosen, err)
		}
		reasoning, _ := s.recorder.body(t, 0)["reasoning"].(map[string]any)
		if reasoning["effort"] != want {
			t.Errorf("level %q: sent %v, want %q", chosen, reasoning, want)
		}
	}
}

// The non-stream paths (title, compaction summary, prompt enhancement,
// connection test) read the response object: its text, and the usage with the
// reasoning and cached counts that are parts of the totals, not additions.
func TestCompleteReadsTheResponseObjectAndItsUsage(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: userHello})
	if err != nil || completion.Content != openaiv1.FixedReply || len(completion.ToolCalls) != 0 ||
		completion.Usage == nil || *completion.Usage != (agents.Usage{PromptTokens: 22, CompletionTokens: 9}) {
		t.Fatalf("completion = %#v %v", completion, err)
	}
	// The fake's reasoning answer reports 5 reasoning tokens and 10 cached
	// ones inside 22 + 14; the Port counts 22 and 14.
	reasoned, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("think " + openaiv1.ReasoningMarker)}})
	if err != nil || reasoned.Usage == nil || *reasoned.Usage != (agents.Usage{PromptTokens: 22, CompletionTokens: 14}) {
		t.Fatalf("reasoned = %#v %v", reasoned, err)
	}
	streamed, _, err := collect(adapter, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("think " + openaiv1.ReasoningMarker)}})
	if err != nil || streamed.Usage == nil || *streamed.Usage != (agents.Usage{PromptTokens: 22, CompletionTokens: 14}) {
		t.Fatalf("streamed = %#v %v", streamed, err)
	}
	// A response without usage has none: the Adapter never estimates it.
	bare, _ := scripted(t, http.StatusOK, "application/json",
		`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`)
	if completion, err := bare.Complete(ctx, agents.Request{Model: "m", Messages: userHello}); err != nil || completion.Content != "hi" || completion.Usage != nil {
		t.Fatalf("bare = %#v %v", completion, err)
	}
}

// A tool call on the non-stream path, for the requests that carry tools.
func TestCompleteReturnsToolCalls(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Tools: lookup,
		Messages: []agents.Message{userMessage("go " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))}})
	if err != nil || len(completion.ToolCalls) != 1 || string(completion.ToolCalls[0].Arguments) != `{"topic":"cube"}` ||
		completion.ToolCalls[0].Name != "lookup_note" || completion.Content != "" {
		t.Fatalf("completion = %#v %v", completion, err)
	}
}

// An event type the Adapter does not know and members it does not read (the
// real gateway's responses carry many) change nothing.
func TestUnknownEventsAndMembersAreIgnored(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("hi " + openaiv1.UnknownEventMarker)}}
	streamed, deltas, err := collect(adapter, request)
	if err != nil || streamed.Content != openaiv1.FixedReply || texts(deltas) != openaiv1.FixedReply {
		t.Fatalf("stream: %#v %v", streamed, err)
	}
	complete, err := adapter.Complete(ctx, request)
	if err != nil || complete.Content != openaiv1.FixedReply {
		t.Fatalf("complete: %#v %v", complete, err)
	}
	// The same stream, scripted with new members on a known event, an unknown
	// event between two known ones and a comment line.
	body := ": keep-alive\n\n" + frames(
		"response.created", `{"type":"response.created","response":{"status":"in_progress","future":1},"sequence_number":0}`,
		"response.queued", `{"type":"response.queued","anything":{"nested":[1,2]}}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi","output_index":0,"item_id":"m","logprobs":[],"obfuscation":"x","extra":{"a":1}}`,
		"response.output_text.annotation.added", `{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation"}}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4,"future_details":{}}}}`)
	adapter2, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	if completion, err := adapter2.Stream(ctx, request, nil); err != nil || completion.Content != "hi" ||
		completion.Usage == nil || completion.Usage.PromptTokens != 3 {
		t.Fatalf("scripted: %#v %v", completion, err)
	}
}

// Thinking is signalled once, before the answer, from the reasoning item; the
// reasoning itself never reaches the caller, and a text-only stream signals
// nothing.
func TestReasoningIsSignalledOnceAndNeverShown(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("think " + openaiv1.ReasoningMarker)}}
	_, deltas, err := collect(adapter, request)
	if err != nil {
		t.Fatal(err)
	}
	var thinking, contents []int
	for index, delta := range deltas {
		if delta.Thinking {
			thinking = append(thinking, index)
		}
		if delta.Content != "" {
			contents = append(contents, index)
		}
		if delta.Thinking && delta.Content != "" {
			t.Errorf("a delta carries both: %#v", delta)
		}
	}
	if len(thinking) != 1 || thinking[0] != 0 || len(contents) < 2 || texts(deltas) != openaiv1.FixedReply {
		t.Errorf("deltas = %#v", deltas)
	}
	// A stream that streams a summary of the reasoning (the model was asked
	// for one), twice, still signals a single stretch each.
	body := frames(
		"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		"response.reasoning_summary_part.added", `{"type":"response.reasoning_summary_part.added","item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"secret plan"}`,
		"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":" more"}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`,
		"response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"type":"reasoning","id":"rs_2","summary":[]}}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","output_index":3,"delta":"!"}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[]}}`)
	scriptedAdapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	completion, deltas, err := collect(scriptedAdapter, agents.Request{Model: "m", Messages: userHello})
	if err != nil {
		t.Fatal(err)
	}
	want := []agents.Delta{{Thinking: true}, {Content: "answer"}, {Thinking: true}, {Content: "!"}}
	if fmt.Sprint(deltas) != fmt.Sprint(want) || completion.Content != "answer!" {
		t.Fatalf("deltas = %#v completion = %#v", deltas, completion)
	}
	if strings.Contains(fmt.Sprint(deltas), "secret") || strings.Contains(completion.Content, "secret") || strings.Contains(completion.ReasoningContent, "secret") {
		t.Fatal("reasoning text reached the caller")
	}
}

// The reasoning items of a tool loop come back to the model: the first reply
// carries them in ReasoningContent (opaque), and the second request sends them
// ahead of the function_call they led to, exactly as the upstream returned
// them. The fake refuses a reasoning item that leads nowhere.
func TestReasoningItemsAreSentBackAheadOfTheirFunctionCall(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	request := agents.Request{Model: "gpt-astra", Tools: lookup, Messages: []agents.Message{
		userMessage("go " + openaiv1.ReasoningMarker + " " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`))}}
	first, err := adapter.Stream(ctx, request, nil)
	if err != nil || len(first.ToolCalls) != 1 || first.ReasoningContent == "" {
		t.Fatalf("round 1: %#v %v", first, err)
	}
	request.Messages = append(request.Messages,
		agents.Message{Role: "assistant", ToolCalls: first.ToolCalls, ReasoningContent: first.ReasoningContent},
		agents.Message{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: "x"})
	second, err := adapter.Stream(ctx, request, nil)
	if err != nil || second.Content != openaiv1.FixedReply || second.ReasoningContent != "" {
		t.Fatalf("round 2: %#v %v", second, err)
	}
	input := s.recorder.body(t, 0)["input"].([]any)
	var kinds []string
	for _, item := range input {
		object := item.(map[string]any)
		if kind, _ := object["type"].(string); kind != "" {
			kinds = append(kinds, kind)
		} else {
			kinds = append(kinds, object["role"].(string))
		}
	}
	if strings.Join(kinds, ",") != "user,reasoning,function_call,function_call_output" {
		t.Fatalf("input = %v", kinds)
	}
	// The item goes back in the form the probe's replay had and the gateway
	// accepted: type, summary and encrypted_content, without the item's id and
	// content, with the token of the done event (the added event's was shorter,
	// and the one in the output of response.completed another).
	reasoning := input[1].(map[string]any)
	var members []string
	for member := range reasoning {
		members = append(members, member)
	}
	slices.Sort(members)
	if strings.Join(members, ",") != "encrypted_content,summary,type" || reasoning["type"] != "reasoning" ||
		reasoning["encrypted_content"] != openaiv1.FixedEncryptedDone || len(reasoning["summary"].([]any)) != 0 {
		t.Fatalf("the reasoning item is not sent in the replayed form: %v", reasoning)
	}
	if strings.Contains(s.recorder.raw(0), openaiv1.FixedEncryptedPartial) || strings.Contains(s.recorder.raw(0), openaiv1.FixedEncryptedFinal) {
		t.Fatalf("a token other than the done event's was sent: %s", s.recorder.raw(0))
	}
	// Without the carried reasoning (a resumed turn) the same loop still works.
	request.Messages[1] = agents.Message{Role: "assistant", ToolCalls: first.ToolCalls}
	if _, err := adapter.Stream(ctx, request, nil); err != nil {
		t.Fatalf("without reasoning: %v", err)
	}
	// Another Adapter's ReasoningContent is not this one's: it is ignored.
	request.Messages[1] = agents.Message{Role: "assistant", ToolCalls: first.ToolCalls, ReasoningContent: "plain chain of thought text"}
	if _, err := adapter.Stream(ctx, request, nil); err != nil {
		t.Fatalf("foreign reasoning: %v", err)
	}
	if strings.Contains(s.recorder.raw(0), "chain of thought") {
		t.Fatal("a foreign ReasoningContent was sent")
	}
}

// A request without tools does not ask for encrypted reasoning, so the reply
// holds no reasoning to keep, and a reply with a reasoning item that has no
// encrypted content has none to carry back.
func TestReasoningWithoutEncryptedContentIsNotCarried(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	completion, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("think " + openaiv1.ReasoningMarker)}})
	if err != nil || completion.ReasoningContent != "" || strings.Contains(s.recorder.raw(0), "include") {
		t.Fatalf("completion = %#v %v request %s", completion, err, s.recorder.raw(0))
	}
}
