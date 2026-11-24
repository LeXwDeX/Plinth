package wirefixture_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #536 (I26): a stream whose events carry an event: line (the OpenAI Responses
// wire) is recorded in the same normalized form as a Chat stream, with the
// event name kept and no [DONE] invented; it is complete at its terminal event.

func namedPolicy() wirefixture.Policy {
	policy := testPolicy()
	allowPolicy(&policy,
		[]string{
			"type", "sequence_number", "response", "item", "item_id", "output_index", "content_index",
			"delta", "text", "arguments", "name", "call_id", "output", "usage", "input_tokens",
			"output_tokens", "total_tokens", "model", "object", "part", "city", "summary", "encrypted_content",
		},
		[]string{
			"response.created", "response.output_item.added", "response.output_text.delta",
			"response.output_text.done", "response.function_call_arguments.delta",
			"response.function_call_arguments.done", "response.output_item.done", "response.completed",
			"response.incomplete", "response", "in_progress", "completed", "message", "output_text",
			"function_call", "get_weather", "gpt-test", "Hello world", `{"city":"Paris"}`, "Paris", "3", "4", "5",
		},
	)
	policy.Aliases["msg_live"] = "call-1"
	policy.Aliases["resp_live"] = "call-1"
	return policy
}

func namedBlock(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func respondStream(t *testing.T) string {
	t.Helper()
	return namedBlock("response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"resp_live","object":"response","model":"gpt-test","status":"in_progress"}}`) +
		namedBlock("response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_live","type":"message","status":"in_progress","role":"assistant"}}`) +
		namedBlock("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_live","output_index":0,"content_index":0,"delta":"Hello"}`) +
		namedBlock("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_live","output_index":0,"content_index":0,"delta":" world"}`) +
		namedBlock("response.output_text.done", `{"type":"response.output_text.done","sequence_number":4,"item_id":"msg_live","output_index":0,"content_index":0,"text":"Hello world"}`) +
		namedBlock("response.completed", `{"type":"response.completed","sequence_number":5,"response":{"id":"resp_live","object":"response","model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
}

func TestNamedSSERecordsWithoutDONEAndReplaysByteForByte(t *testing.T) {
	policy := namedPolicy()
	exchanges, body := recordSSE(t, policy, respondStream(t))
	if string(body) != respondStream(t) {
		t.Fatalf("business response changed: %s", body)
	}
	if len(exchanges) != 1 || !exchanges[0].Complete || exchanges[0].Invalid != "" || exchanges[0].Metadata.Protocol != "sse" {
		t.Fatalf("named SSE exchange: %+v", exchanges)
	}
	sse := exchanges[0].Response.Body.SSE
	if !strings.HasPrefix(sse, "event: response.created\ndata: {") || strings.Contains(sse, "[DONE]") {
		t.Fatalf("named SSE body keeps event lines and invents no [DONE]: %s", sse)
	}
	if strings.Contains(sse, "resp_live") || strings.Contains(sse, "msg_live") {
		t.Fatalf("live identifiers leaked: %s", sse)
	}
	// The two text fragments are one approved value, written once in the last
	// fragment (the Chat rule).
	if !strings.Contains(sse, `"delta":"Hello world"`) || !strings.Contains(sse, `"delta":""`) {
		t.Fatalf("delta fragments not merged: %s", sse)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/chat", ""))
	if err != nil {
		t.Fatal(err)
	}
	replayed, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(replayed) != sse {
		t.Fatalf("replayed = %s, want %s", replayed, sse)
	}
}

func TestNamedSSEFunctionArgumentFragmentsAreJoinedAsJSON(t *testing.T) {
	policy := namedPolicy()
	stream := namedBlock("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","sequence_number":1,"item_id":"msg_live","output_index":0,"delta":"{\"city\":"}`) +
		namedBlock("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"msg_live","output_index":0,"delta":"\"Paris\"}"}`) +
		namedBlock("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","sequence_number":3,"item_id":"msg_live","output_index":0,"arguments":"{\"city\":\"Paris\"}"}`) +
		namedBlock("response.completed", `{"type":"response.completed","sequence_number":4,"response":{"status":"completed"}}`)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || !exchanges[0].Complete || exchanges[0].Invalid != "" {
		t.Fatalf("argument fragments exchange: %+v", exchanges)
	}
	if !strings.Contains(exchanges[0].Response.Body.SSE, `"delta":"{\"city\":\"Paris\"}"`) {
		t.Fatalf("argument fragments not joined: %s", exchanges[0].Response.Body.SSE)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
}

func TestNamedSSEFragmentsOfDifferentItemsDoNotMix(t *testing.T) {
	policy := namedPolicy()
	delete(policy.Allowed, "Hello world")
	allowPolicy(&policy, nil, []string{"Hello"})
	stream := namedBlock("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_live","output_index":0,"content_index":0,"delta":"Hello"}`) +
		namedBlock("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_live","output_index":1,"content_index":0,"delta":"Paris"}`) +
		namedBlock("response.completed", `{"type":"response.completed","response":{"status":"completed"}}`)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || !exchanges[0].Complete || exchanges[0].Invalid != "" {
		t.Fatalf("each output item is approved on its own: %+v", exchanges)
	}
	if strings.Contains(exchanges[0].Response.Body.SSE, "HelloParis") {
		t.Fatalf("fragments of two items were joined: %s", exchanges[0].Response.Body.SSE)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
}

func TestNamedSSEUnapprovedTextIsRefused(t *testing.T) {
	policy := namedPolicy()
	stream := namedBlock("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_live","output_index":0,"content_index":0,"delta":"private value"}`) +
		namedBlock("response.completed", `{"type":"response.completed","response":{"status":"completed"}}`)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || exchanges[0].Complete || exchanges[0].Invalid == "" {
		t.Fatalf("unapproved text accepted: %+v", exchanges)
	}
	if err := wirefixture.Validate(exchanges, policy); err == nil {
		t.Fatal("unapproved text validated")
	}
}

func TestNamedSSEFramingAndFieldRules(t *testing.T) {
	policy := namedPolicy()
	created := namedBlock("response.created", `{"type":"response.created","response":{"status":"in_progress"}}`)
	completed := namedBlock("response.completed", `{"type":"response.completed","response":{"status":"completed"}}`)
	incomplete := namedBlock("response.incomplete", `{"type":"response.incomplete","response":{"status":"completed"}}`)
	for name, tc := range map[string]struct {
		stream string
		want   string // "": the stream is complete; else a part of the refusal text
	}{
		"terminal completed":     {created + completed, ""},
		"terminal incomplete":    {created + incomplete, ""},
		"terminal then [DONE]":   {created + completed + "data: [DONE]\n\n", ""},
		"no terminal event":      {created, "partial SSE"},
		"event after terminal":   {completed + created, "partial SSE"},
		"cut inside a block":     {created + "event: response.completed\ndata: {\"type\":", "partial SSE"},
		"event without data":     {created + "event: response.completed\n\n" + completed, "unsupported body type"},
		"mixed named and bare":   {created + "data: {\"type\":\"x\"}\n\n" + completed, "unsupported body type"},
		"id field":               {"id: 7\n" + created + completed, "unsupported body type"},
		"event name with a bang": {namedBlock("bad name!", `{"type":"response.created"}`) + completed, "unsafe public value"},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
			request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/chat", "")
			_, _ = capture(t, recorder, request, jsonResponse(200, "text/event-stream", tc.stream))
			exchanges := recorder.Exchanges()
			err := wirefixture.Validate(exchanges, policy)
			if tc.want == "" {
				if err != nil || len(exchanges) != 1 || !exchanges[0].Complete {
					t.Fatalf("complete named stream refused: %v %+v", err, exchanges)
				}
				return
			}
			// A refused body is either marked invalid by the recorder or refused
			// by Validate; either way the reason is named.
			reason := exchanges[0].Invalid
			if err != nil {
				reason += " " + err.Error()
			}
			if exchanges[0].Complete || !strings.Contains(reason, tc.want) {
				t.Fatalf("stream accepted or refused for another reason: complete=%v %q, want %q", exchanges[0].Complete, reason, tc.want)
			}
		})
	}
}
