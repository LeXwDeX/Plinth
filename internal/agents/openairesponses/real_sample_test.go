package openairesponses_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
)

// The Adapter against what the real gateway said and accepted on 2026-11-20:
// the sanitized excerpt of the two probes that the fake of the gateway is
// aligned to (internal/testutil/fakeupstream/openai/v1/testdata). Its streams
// are read here exactly as the gateway sent them, and the requests the Adapter
// builds for the probe's conversations are compared, member by member, with the
// requests the gateway accepted.

const sampleFile = "../../testutil/fakeupstream/openai/v1/testdata/responses-2026-11-20.sample.json"

type sampleExchange struct {
	Name    string `json:"name"`
	Request struct {
		Body json.RawMessage `json:"body"`
	} `json:"request"`
	Response struct {
		Status int `json:"status"`
		Events []struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		} `json:"events"`
	} `json:"response"`
}

func loadSample(t *testing.T) map[string]sampleExchange {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Exchanges []sampleExchange `json:"exchanges"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	out := map[string]sampleExchange{}
	for _, exchange := range file.Exchanges {
		out[exchange.Name] = exchange
	}
	return out
}

// sse is the exchange's stream as the gateway sent it.
func (e sampleExchange) sse() string {
	var out strings.Builder
	for _, event := range e.Response.Events {
		out.WriteString("event: " + event.Event + "\ndata: " + string(event.Data) + "\n\n")
	}
	return out.String()
}

// doneItems are the items of the stream's output_item.done events.
func (e sampleExchange) doneItems(t *testing.T) []map[string]any {
	t.Helper()
	var items []map[string]any
	for _, event := range e.Response.Events {
		if event.Event != "response.output_item.done" {
			continue
		}
		var data struct {
			Item map[string]any `json:"item"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		items = append(items, data.Item)
	}
	return items
}

func (e sampleExchange) body(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(e.Request.Body, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// Every stream of the probes is read to what it said: the calls with the IDs,
// names and arguments of their done items, the text of the message, the
// reasoning item (carried in the replayed form, with the token of its done
// event), and a single Thinking signal when the answer started with reasoning.
func TestReadsTheStreamsTheRealGatewaySent(t *testing.T) {
	sample := loadSample(t)
	for _, name := range []string{"tool-call-stream", "tool-output-stream", "parallel-call-stream", "input-image-stream", "reasoning-tool-stream"} {
		t.Run(name, func(t *testing.T) {
			exchange, found := sample[name]
			if !found || len(exchange.Response.Events) == 0 {
				t.Fatalf("the sample has no %s stream", name)
			}
			adapter, _ := scripted(t, http.StatusOK, "text/event-stream", exchange.sse())
			completion, deltas, err := collect(adapter, agents.Request{Model: "gpt-astra", Messages: userHello})
			if err != nil {
				t.Fatal(err)
			}
			var wantCalls []agents.ToolCall
			var wantText, wantToken string
			reasoningItems := 0
			for _, item := range exchange.doneItems(t) {
				switch item["type"] {
				case "function_call":
					wantCalls = append(wantCalls, agents.ToolCall{ID: item["call_id"].(string), Name: item["name"].(string),
						Arguments: json.RawMessage(item["arguments"].(string))})
				case "message":
					for _, part := range item["content"].([]any) {
						wantText += part.(map[string]any)["text"].(string)
					}
				case "reasoning":
					reasoningItems++
					wantToken = item["encrypted_content"].(string)
				}
			}
			if !reflect.DeepEqual(completion.ToolCalls, wantCalls) && !(len(completion.ToolCalls) == 0 && len(wantCalls) == 0) {
				t.Errorf("calls = %#v, want %#v", completion.ToolCalls, wantCalls)
			}
			if completion.Content != wantText || texts(deltas) != wantText {
				t.Errorf("content %q, streamed %q, want %q", completion.Content, texts(deltas), wantText)
			}
			if completion.Usage == nil {
				t.Error("the completed event's usage was not read")
			}
			thinking := 0
			for _, delta := range deltas {
				if delta.Thinking {
					thinking++
				}
			}
			var carried []struct {
				Before int            `json:"before"`
				Item   map[string]any `json:"item"`
			}
			if completion.ReasoningContent != "" {
				if err := json.Unmarshal([]byte(completion.ReasoningContent), &carried); err != nil {
					t.Fatal(err)
				}
			}
			if reasoningItems == 0 && (thinking != 0 || len(carried) != 0) {
				t.Errorf("no reasoning item, yet thinking %d and %d carried", thinking, len(carried))
			}
			if reasoningItems > 0 {
				if thinking != 1 || len(deltas) == 0 || !deltas[0].Thinking {
					t.Errorf("thinking signals = %d, deltas = %#v", thinking, deltas)
				}
				// The item that goes back is the done event's: the one of the
				// added event is shorter and the one in the output of
				// response.completed is another value.
				if len(carried) != 1 || carried[0].Before != 0 || carried[0].Item["encrypted_content"] != wantToken ||
					len(carried[0].Item) != 3 || carried[0].Item["type"] != "reasoning" {
					t.Errorf("carried = %#v, want one item with the token %q", carried, wantToken)
				}
			}
		})
	}
}

// requestEqual compares the request the Adapter sent with the one the gateway
// accepted, as JSON documents (key order is not part of a request).
func requestEqual(t *testing.T, name string, got string, want map[string]any) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, want) {
		gotJSON, _ := json.MarshalIndent(decoded, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("%s: the Adapter's request differs from the one the gateway accepted:\n got %s\nwant %s", name, gotJSON, wantJSON)
	}
}

// The requests the Adapter builds for the probes' conversations are the
// requests the gateway accepted, member by member: the Agent turn with its tool
// (strict false, an optional property), the level and the encrypted-reasoning
// include; the round after a tool call under the orchestrator's rewritten
// call_id, with the reasoning item replayed without its id and without it; the
// parallel-call request; and a user message with a picture.
func TestSendsTheRequestsTheRealGatewayAccepted(t *testing.T) {
	sample := loadSample(t)
	// Any valid stream will do as the answer: only the request is looked at.
	canned := sample["tool-output-stream"].sse()
	record := func(mutate func(*openairesponses.Config), request agents.Request) string {
		t.Helper()
		adapter, rec := scriptedConfigured(t, http.StatusOK, "text/event-stream", canned, mutate)
		if _, err := adapter.Stream(ctx, request, nil); err != nil {
			t.Fatal(err)
		}
		return rec.raw(0)
	}
	// The tool and the question of each probe are read from the sample, so
	// nothing here restates what the gateway was sent.
	toolOf := func(body map[string]any) agents.Tool {
		tool := body["tools"].([]any)[0].(map[string]any)
		parameters, _ := json.Marshal(tool["parameters"])
		return agents.Tool{Name: tool["name"].(string), Description: tool["description"].(string), Parameters: parameters}
	}
	instructionsOf := func(body map[string]any) agents.Message {
		return agents.Message{Role: "system", Content: body["instructions"].(string)}
	}
	userOf := func(body map[string]any) agents.Message {
		return agents.Message{Role: "user", Content: body["input"].([]any)[0].(map[string]any)["content"].(string)}
	}

	// The Agent turn that returned a reasoning item and a call (xhigh).
	turn := sample["reasoning-tool-stream"].body(t)
	request := agents.Request{Model: "gpt-astra", ReasoningEffort: "xhigh", Tools: []agents.Tool{toolOf(turn)},
		Messages: []agents.Message{instructionsOf(turn), userOf(turn)}}
	requestEqual(t, "reasoning-tool-stream", record(nil, request), turn)

	// The next round: what that turn's real stream returned (the call, the
	// reasoning item), the call's ID rewritten as the probe did, the call's
	// result.
	first, err := scriptedStream(t, sample["reasoning-tool-stream"]).Stream(ctx, request, nil)
	if err != nil || len(first.ToolCalls) != 1 || first.ReasoningContent == "" {
		t.Fatalf("the real stream: %#v %v", first, err)
	}
	call := first.ToolCalls[0]
	call.ID = "msg_x_r1_tool_1"
	next := request
	next.Messages = append(append([]agents.Message{}, request.Messages...),
		agents.Message{Role: "assistant", ToolCalls: []agents.ToolCall{call}, ReasoningContent: first.ReasoningContent},
		agents.Message{Role: "tool", ToolCallID: call.ID, Content: `{"temp_c":21}`})
	requestEqual(t, "reasoning-replay-accepted", record(nil, next), sample["reasoning-replay-accepted"].body(t))

	// The same round without the reasoning item.
	next.Messages = append([]agents.Message{}, next.Messages...)
	next.Messages[2].ReasoningContent = ""
	requestEqual(t, "reasoning-dropped-accepted", record(nil, next), sample["reasoning-dropped-accepted"].body(t))

	// A call under a rewritten call_id, effort high, an optional property.
	rewritten := sample["rewritten-call-accepted"].body(t)
	function := rewritten["input"].([]any)[1].(map[string]any)
	requestEqual(t, "rewritten-call-accepted", record(nil, agents.Request{Model: "gpt-astra", ReasoningEffort: "high", Tools: []agents.Tool{toolOf(rewritten)}, Messages: []agents.Message{
		instructionsOf(rewritten), userOf(rewritten),
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: function["call_id"].(string), Name: function["name"].(string), Arguments: json.RawMessage(function["arguments"].(string))}}},
		{Role: "tool", ToolCallID: function["call_id"].(string), Content: rewritten["input"].([]any)[2].(map[string]any)["output"].(string)}}}), rewritten)

	// parallel_tool_calls on a request with tools.
	parallel := sample["parallel-call-stream"].body(t)
	requestEqual(t, "parallel-call-stream", record(func(config *openairesponses.Config) { config.ParallelToolCalls = true },
		agents.Request{Model: "gpt-astra", ReasoningEffort: "high", Tools: []agents.Tool{toolOf(parallel)},
			Messages: []agents.Message{instructionsOf(parallel), userOf(parallel)}}), parallel)

	// A picture: an input_text and an input_image part, detail low, and no
	// reasoning member (the probe sent none).
	image := sample["input-image-stream"].body(t)
	parts := image["input"].([]any)[0].(map[string]any)["content"].([]any)
	requestEqual(t, "input-image-stream", record(func(config *openairesponses.Config) {
		config.OptionsFor = func(string) *openairesponses.Options { return &openairesponses.Options{OmitTemperature: true} }
	}, agents.Request{Model: "gpt-astra", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: parts[0].(map[string]any)["text"].(string)},
		{Kind: "image_url", URL: parts[1].(map[string]any)["image_url"].(string), Detail: parts[1].(map[string]any)["detail"].(string)}}}}}), image)
}

// scriptedStream is an Adapter that answers every request with the exchange's
// stream, as the gateway sent it.
func scriptedStream(t *testing.T, exchange sampleExchange) *openairesponses.Adapter {
	t.Helper()
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", exchange.sse())
	return adapter
}
