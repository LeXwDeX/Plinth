package openaiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// frame is one server-sent event of a /v1/responses stream.
type frame struct {
	event string
	data  map[string]any
	raw   string
}

// responsesStream reads a streamed answer into frames and checks the framing
// the gateway uses: an event: line equal to data.type, then one data: line,
// then a blank line, and no [DONE].
func responsesStream(t *testing.T, raw []byte) []frame {
	t.Helper()
	var frames []frame
	for _, block := range strings.Split(strings.TrimSuffix(string(raw), "\n\n"), "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("a frame is an event line and a data line: %q", block)
		}
		var data map[string]any
		decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(lines[1], "data: ")))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil {
			t.Fatalf("bad data %q: %v", lines[1], err)
		}
		if event := strings.TrimPrefix(lines[0], "event: "); data["type"] != event {
			t.Fatalf("event %q carries type %v", event, data["type"])
		}
		frames = append(frames, frame{event: strings.TrimPrefix(lines[0], "event: "), data: data, raw: lines[1]})
	}
	if strings.Contains(string(raw), "[DONE]") {
		t.Fatal("a Responses stream has no [DONE]")
	}
	return frames
}

func (f *fixture) respond(body map[string]any, headers ...string) (*http.Response, []byte) {
	f.t.Helper()
	return f.send("POST", "/responses", "test-key", body, headers...)
}

func (f *fixture) respondJSON(body map[string]any) (int, map[string]any) {
	f.t.Helper()
	response, raw := f.respond(body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return response.StatusCode, decoded
}

func (f *fixture) respondStream(body map[string]any) []frame {
	f.t.Helper()
	body["stream"] = true
	response, raw := f.respond(body)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		f.t.Fatalf("stream = %d %s %s", response.StatusCode, response.Header.Get("Content-Type"), raw)
	}
	return responsesStream(f.t, raw)
}

func (f *fixture) sample() *responsesSample {
	f.t.Helper()
	sample, err := loadResponsesSample()
	if err != nil {
		f.t.Fatal(err)
	}
	return sample
}

func types(frames []frame) []string {
	var out []string
	for _, f := range frames {
		out = append(out, f.event)
	}
	return out
}

// collapsed drops a repeat of the previous element: the deltas of a stream.
func collapsed(values []string) []string {
	var out []string
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

// unionShape is the union of the shapes of the data of every frame (of the
// sample or of the fake) of one event type.
func unionShape(t *testing.T, datas []map[string]any) fakeupstream.Shape {
	t.Helper()
	union := fakeupstream.Shape{}
	for _, data := range datas {
		raw, _ := json.Marshal(data)
		for path, kind := range shapeOf(t, raw) {
			if known, seen := union[path]; seen && known != kind {
				kind = known + "|" + kind
			}
			union[path] = kind
		}
	}
	return union
}

func dataByType(frames []frame) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for _, f := range frames {
		out[f.event] = append(out[f.event], f.data)
	}
	return out
}

func sampleDataByType(exchange responsesExchange) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for _, event := range exchange.Response.Events {
		out[event.Event] = append(out[event.Event], event.Data)
	}
	return out
}

// The sample's own request, with the fake-only marker that asks for the tool
// call the real model chose by itself.
func sampleRequest(t *testing.T, exchange responsesExchange, marker string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(exchange.Request.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if marker != "" {
		input, _ := body["input"].([]any)
		first, _ := input[0].(map[string]any)
		first["content"] = fmt.Sprint(first["content"]) + " " + marker
	}
	return body
}

// The fake answers the sample's requests with the events of the sample, in the
// same order and with the same fields (issue #96 for this version): every
// event type of the real stream appears in the fake's with the same field set
// and types, and the fake's stream has no event type the real one lacks.
func TestResponsesStreamsMatchTheRealSample(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	sample := f.sample()
	for _, test := range []struct {
		name, marker string
	}{
		{"tool-call-stream", ToolMarkerFor("get_weather", `{"city":"Paris"}`)},
		// The second round of the sample: the function_call and its output are
		// in input, so no marker is needed for the model to answer in text.
		{"tool-output-stream", ""},
		// Two calls in one answer: each is its own run of events.
		{"parallel-call-stream", ToolMarkerFor("get_weather", `{"city":"Paris","unit":"c"}`) + " " + ToolMarkerFor("get_weather", `{"city":"Tokyo","unit":"c"}`)},
		// A picture in the request, answered in text.
		{"input-image-stream", ""},
		// A reasoning item ahead of the call, as the real model returned one at
		// effort xhigh.
		{"reasoning-tool-stream", ReasoningMarker + " " + ToolMarkerFor("get_weather", `{"city":"Kyoto","unit":"c"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			exchange, found := sample.exchange(test.name)
			if !found {
				t.Fatalf("the sample has no %s exchange", test.name)
			}
			frames := f.respondStream(sampleRequest(t, exchange, test.marker))
			wantTypes := make([]string, 0, len(exchange.Response.Events))
			for _, event := range exchange.Response.Events {
				wantTypes = append(wantTypes, event.Event)
			}
			if got, want := collapsed(types(frames)), collapsed(wantTypes); !slices.Equal(got, want) {
				t.Fatalf("event order:\n fake   %v\n sample %v", got, want)
			}
			for index, frame := range frames {
				if fmt.Sprint(frame.data["sequence_number"]) != fmt.Sprint(index) {
					t.Fatalf("event %d (%s) has sequence_number %v", index, frame.event, frame.data["sequence_number"])
				}
			}
			real, fake := sampleDataByType(exchange), dataByType(frames)
			for eventType, want := range real {
				if diff := fakeupstream.DiffShape(unionShape(t, want), unionShape(t, fake[eventType])); !diff.Empty() {
					t.Errorf("%s drifted from the real sample: %s", eventType, diff)
				}
			}
		})
	}
}

// Every request the real gateway answered with 200 is answered with 200 by the
// fake too: it is never stricter than the real one. The sample's requests
// include the shapes Plinth's Adapter sends: a tool with strict false and an
// optional property, a function_call with no id or status under a rewritten
// call_id, a reasoning item replayed without its id, a round with no reasoning
// item and an input_image part.
func TestResponsesAcceptTheRequestsTheRealGatewayAccepted(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	checked := 0
	for _, exchange := range f.sample().Exchanges {
		if exchange.Response.Status != http.StatusOK {
			continue
		}
		body := sampleRequest(t, exchange, "")
		response, raw := f.respond(body)
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", exchange.Name, response.StatusCode, raw)
			continue
		}
		if stream, _ := body["stream"].(bool); stream {
			frames := responsesStream(t, raw)
			if last := frames[len(frames)-1]; last.event != "response.completed" {
				t.Errorf("%s ends with %s", exchange.Name, last.event)
			}
		}
		checked++
	}
	if checked < 9 {
		t.Fatalf("only %d requests of the sample were checked", checked)
	}
}

// The reasoning item of the sample (observed at effort xhigh) is what the fake
// returns for the reasoning marker: content and summary as empty arrays, an
// id, the token, no status; the token of the added event is not the one of the
// done event, and the one in the output of response.completed is a third; a
// request that asked for no token gets an item without one.
func TestResponsesReasoningItemHasTheObservedShapeAndThreeTokens(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	request := map[string]any{"model": "gpt-astra", "include": []string{"reasoning.encrypted_content"},
		"tools": []map[string]any{{"type": "function", "name": "get_weather"}},
		"input": "go " + ReasoningMarker + " " + ToolMarkerFor("get_weather", `{"city":"Kyoto"}`)}
	frames := f.respondStream(request)
	if got := types(frames)[2:5]; !slices.Equal(got, []string{"response.output_item.added", "response.output_item.done", "response.output_item.added"}) {
		t.Fatalf("events = %v", types(frames))
	}
	keys := func(item any) string {
		var names []string
		for key := range item.(map[string]any) {
			names = append(names, key)
		}
		slices.Sort(names)
		return strings.Join(names, ",")
	}
	added, done := dig(t, frames[2].data, "item"), dig(t, frames[3].data, "item")
	final := dig(t, frames[len(frames)-1].data, "response", "output", 0)
	for name, item := range map[string]any{"added": added, "done": done, "final": final} {
		if keys(item) != "content,encrypted_content,id,summary,type" || dig(t, item, "type") != "reasoning" ||
			len(dig(t, item, "content").([]any)) != 0 || len(dig(t, item, "summary").([]any)) != 0 {
			t.Errorf("%s item = %v", name, item)
		}
	}
	if dig(t, added, "encrypted_content") != FixedEncryptedPartial || dig(t, done, "encrypted_content") != FixedEncryptedDone ||
		dig(t, final, "encrypted_content") != FixedEncryptedFinal || FixedEncryptedPartial == FixedEncryptedDone || FixedEncryptedDone == FixedEncryptedFinal {
		t.Errorf("tokens: added %v, done %v, final %v", dig(t, added, "encrypted_content"), dig(t, done, "encrypted_content"), dig(t, final, "encrypted_content"))
	}
	if dig(t, added, "id") != dig(t, done, "id") || dig(t, done, "id") != dig(t, final, "id") {
		t.Errorf("the item changes id: %v %v %v", dig(t, added, "id"), dig(t, done, "id"), dig(t, final, "id"))
	}
	// The non-stream answer carries the final form; with no include there is
	// no token at all.
	delete(request, "stream")
	_, body := f.respondJSON(request)
	if dig(t, body, "output", 0, "encrypted_content") != FixedEncryptedFinal {
		t.Errorf("non-stream token = %v", dig(t, body, "output", 0))
	}
	delete(request, "include")
	_, body = f.respondJSON(request)
	if keys(dig(t, body, "output", 0)) != "content,id,summary,type" {
		t.Errorf("item without include = %v", dig(t, body, "output", 0))
	}
}

// The text answer is cut into several deltas that add up to the fixed reply,
// and the done events carry the whole text.
func TestResponsesTextAnswerIsStreamedInDeltas(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	frames := f.respondStream(map[string]any{"model": "deepseek", "input": "hi"})
	var text strings.Builder
	deltas := 0
	for _, frame := range frames {
		if frame.event == "response.output_text.delta" {
			deltas++
			text.WriteString(frame.data["delta"].(string))
		}
	}
	if text.String() != FixedReply || deltas < 2 {
		t.Fatalf("deltas %d = %q", deltas, text.String())
	}
	completed := frames[len(frames)-1]
	if completed.event != "response.completed" || dig(t, completed.data, "response", "output", 0, "content", 0, "text") != FixedReply ||
		dig(t, completed.data, "response", "status") != "completed" {
		t.Fatalf("completed = %v", completed.data)
	}
	// Echoed from the request, as the real response objects are.
	first := frames[0]
	if first.event != "response.created" || dig(t, first.data, "response", "status") != "in_progress" ||
		dig(t, first.data, "response", "model") != "deepseek" || dig(t, first.data, "response", "usage") != nil {
		t.Fatalf("created = %v", first.data)
	}
}

// The non-stream response has the shape of the sample's, down to the usage.
func TestResponsesJSONMatchesTheRealSample(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	exchange, _ := f.sample().exchange("json-text")
	request := sampleRequest(t, exchange, "")
	_, raw := f.respond(request)
	want, _ := json.Marshal(exchange.Response.Body)
	if diff := fakeupstream.DiffShape(shapeOf(t, want), shapeOf(t, raw)); !diff.Empty() {
		t.Fatalf("the response drifted from the real sample: %s", diff)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if dig(t, body, "output", 0, "content", 0, "text") != FixedReply || dig(t, body, "status") != "completed" ||
		dig(t, body, "model") != "gpt-6.2-astra" || dig(t, body, "max_output_tokens") != float64(200) ||
		dig(t, body, "usage", "input_tokens") != float64(22) || dig(t, body, "usage", "output_tokens") != float64(9) ||
		dig(t, body, "usage", "output_tokens_details", "reasoning_tokens") != float64(0) {
		t.Fatalf("body = %s", raw)
	}
	if !bytes.HasPrefix(raw, []byte(`{"access_programs":null,"background":false,`)) {
		t.Fatalf("keys are not in the gateway's order: %.80s", raw)
	}
}

// The 400s of the probes, byte for byte, for every name of the model: a
// temperature, an unknown level (named in the message), and none and minimal,
// which the gateway knows and the model does not support.
func TestResponsesRefuseTemperatureAndUnsupportedLevelsOnTheConstrainedModel(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	sample := f.sample()
	temperature, _ := sample.exchange("error-temperature")
	effort, _ := sample.exchange("error-effort")
	minimal, _ := sample.exchange("error-effort-minimal")
	none, _ := sample.exchange("error-effort-none")
	tools := []map[string]any{{"type": "function", "name": "get_weather"}}
	for _, model := range []string{"gpt-astra", "gpt-6.2-astra", "GPT-Astra"} {
		for name, test := range map[string]struct {
			body map[string]any
			want string
		}{
			"temperature 0":            {map[string]any{"input": "hi", "temperature": 0}, temperature.Response.BodyText},
			"temperature 1":            {map[string]any{"input": "hi", "temperature": 1}, temperature.Response.BodyText},
			"streamed":                 {map[string]any{"input": "hi", "temperature": 0.7, "stream": true}, temperature.Response.BodyText},
			"unknown level":            {map[string]any{"input": "hi", "reasoning": map[string]any{"effort": "bogus"}}, effort.Response.BodyText},
			"level before temperature": {map[string]any{"input": "hi", "temperature": 0, "reasoning": map[string]any{"effort": "bogus"}, "stream": true}, effort.Response.BodyText},
			"minimal":                  {map[string]any{"input": "hi", "reasoning": map[string]any{"effort": "minimal"}, "store": false, "max_output_tokens": 50}, minimal.Response.BodyText},
			"none with tools":          {map[string]any{"input": "hi", "tools": tools, "reasoning": map[string]any{"effort": "none"}, "include": []string{"reasoning.encrypted_content"}, "stream": true}, none.Response.BodyText},
			"none before temperature":  {map[string]any{"input": "hi", "temperature": 0, "reasoning": map[string]any{"effort": "none"}}, none.Response.BodyText},
		} {
			body := map[string]any{"model": model}
			for key, value := range test.body {
				body[key] = value
			}
			response, raw := f.respond(body)
			if response.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(raw)) != test.want ||
				!strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
				t.Errorf("%s / %s: %d %s\nwant %s", model, name, response.StatusCode, raw, test.want)
			}
		}
	}
	_, raw := f.respond(map[string]any{"model": "gpt-astra", "input": "hi", "reasoning": map[string]any{"effort": "extreme"}})
	if !strings.Contains(string(raw), "Invalid value: 'extreme'. Supported values are: 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', and 'max'.") ||
		!strings.Contains(string(raw), `"param":"reasoning.effort"`) || !strings.Contains(string(raw), `"code":"invalid_value"`) {
		t.Fatalf("unknown level = %s", raw)
	}
	// The levels the model supports are served, and another model is not judged
	// by these rules.
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		if status, body := f.respondJSON(map[string]any{"model": "gpt-astra", "input": "hi", "reasoning": map[string]any{"effort": level}}); status != http.StatusOK {
			t.Errorf("level %s: %d %v", level, status, body)
		}
	}
	for _, level := range []string{"none", "minimal"} {
		if status, body := f.respondJSON(map[string]any{"model": "deepseek", "input": "hi", "reasoning": map[string]any{"effort": level}}); status != http.StatusOK {
			t.Errorf("another model, level %s: %d %v", level, status, body)
		}
	}
}

// The model accepts what Chat Completions refuses it: function tools with a
// thinking level, and a request that sends no temperature (or a null one).
func TestResponsesServeTheConstrainedModelItsAgentRequestShape(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tools := []map[string]any{{"type": "function", "name": "lookup_note", "parameters": map[string]any{"type": "object"}, "strict": false}}
	for _, model := range []string{"gpt-astra", "gpt-6.2-astra"} {
		for name, body := range map[string]map[string]any{
			"agent turn":        {"input": "hi", "tools": tools, "reasoning": map[string]any{"effort": "high"}, "store": false, "include": []string{"reasoning.encrypted_content"}},
			"no level":          {"input": "hi"},
			"null temperature":  {"input": "hi", "temperature": nil},
			"streamed agent":    {"input": "hi", "tools": tools, "reasoning": map[string]any{"effort": "max"}, "stream": true},
			"instructions only": {"input": []map[string]any{{"role": "user", "content": "hi"}}, "instructions": "Be brief."},
		} {
			request := map[string]any{"model": model}
			for key, value := range body {
				request[key] = value
			}
			response, raw := f.respond(request)
			if response.StatusCode != http.StatusOK {
				t.Errorf("%s / %s: %d %s", model, name, response.StatusCode, raw)
			}
		}
	}
	// The same model on the wire it cannot serve tools on still refuses them.
	status, refusal := f.refuse(map[string]any{"model": "gpt-astra", "messages": user("hi"), "tools": lookupTools})
	if status != http.StatusBadRequest || !strings.Contains(refusal, "use /v1/responses") {
		t.Fatalf("chat completions = %d %s", status, refusal)
	}
	// Another model takes a temperature on this wire.
	if status, _ := f.respondJSON(map[string]any{"model": "deepseek", "input": "hi", "temperature": 0}); status != http.StatusOK {
		t.Fatalf("unconstrained model: %d", status)
	}
	// none is refused as an unsupported value (observed), with the field path
	// of this wire.
	response, raw := f.respond(map[string]any{"model": "gpt-astra", "input": "hi", "reasoning": map[string]any{"effort": "none"}})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), `"param":"reasoning.effort"`) ||
		!strings.Contains(string(raw), `"code":"unsupported_value"`) || !strings.Contains(string(raw), "'gpt-6.2-astra-2026-11-17' model") {
		t.Fatalf("none = %d %s", response.StatusCode, raw)
	}
}

// A function call and its output must pair (the two refusals are the observed
// ones, with the call_id of the request put in), and a reasoning item needs the
// item it led to (constructed from the documents, in the same envelope).
func TestResponsesPairFunctionCallsAndOutputs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	// The probes' own requests are answered byte for byte; no code member.
	for name, want := range map[string]string{"error-orphan-output": "orphan", "error-missing-output": "missing"} {
		exchange, found := f.sample().exchange(name)
		if !found {
			t.Fatalf("the sample has no %s", name)
		}
		response, raw := f.respond(sampleRequest(t, exchange, ""))
		if response.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(raw)) != exchange.Response.BodyText ||
			strings.Contains(string(raw), `"code"`) || !strings.Contains(string(raw), `"param":"input"`) {
			t.Errorf("%s (%s): %d %s\nwant %s", name, want, response.StatusCode, raw, exchange.Response.BodyText)
		}
	}
	user := map[string]any{"role": "user", "content": "hi"}
	call := func(id string) map[string]any {
		return map[string]any{"type": "function_call", "call_id": id, "name": "lookup_note", "arguments": "{}"}
	}
	output := func(id string) map[string]any {
		return map[string]any{"type": "function_call_output", "call_id": id, "output": "{}"}
	}
	reasoning := map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "abc"}
	assistant := map[string]any{"role": "assistant", "content": "ok"}
	for name, test := range map[string]struct {
		input []map[string]any
		want  string // "" is accepted
	}{
		"paired":                {[]map[string]any{user, call("a"), output("a")}, ""},
		"two calls":             {[]map[string]any{user, call("a"), call("b"), output("a"), output("b")}, ""},
		"outputs in any order":  {[]map[string]any{user, call("a"), call("b"), output("b"), output("a")}, ""},
		"two rounds":            {[]map[string]any{user, call("a"), output("a"), call("b"), output("b")}, ""},
		"reasoning then a call": {[]map[string]any{user, reasoning, call("a"), output("a")}, ""},
		"reasoning then text":   {[]map[string]any{user, reasoning, assistant, {"role": "user", "content": "more"}}, ""},
		"output without a call": {[]map[string]any{user, output("a")}, "No tool call found for function call output with call_id a."},
		"output before its call": {[]map[string]any{user, output("a"), call("a")},
			"No tool call found for function call output with call_id a."},
		"call without an output": {[]map[string]any{user, call("a"), call("b"), output("a")}, "No tool output found for function call b."},
		"dangling reasoning":     {[]map[string]any{user, reasoning}, "Item 'rs_1' of type 'reasoning' was provided without its required following item."},
		"reasoning before an output": {[]map[string]any{user, call("a"), reasoning, output("a")},
			"Item 'rs_1' of type 'reasoning' was provided without its required following item."},
		// The replay of the probe: the item without its id (accepted when a call
		// follows); one with no call or text after it is refused too.
		"reasoning without an id then a call": {[]map[string]any{user, {"type": "reasoning", "summary": []any{}, "encrypted_content": "abc"}, call("a"), output("a")}, ""},
		"dangling reasoning without an id":    {[]map[string]any{user, {"type": "reasoning", "summary": []any{}, "encrypted_content": "abc"}}, "Item of type 'reasoning' was provided without its required following item."},
	} {
		status, body := f.respondJSON(map[string]any{"model": "gpt-astra", "input": test.input})
		if test.want == "" {
			if status != http.StatusOK {
				t.Errorf("%s: %d %v", name, status, body)
			}
			continue
		}
		if status != http.StatusBadRequest || dig(t, body, "error", "message") != test.want ||
			dig(t, body, "error", "param") != "input" || dig(t, body, "error", "type") != "invalid_request_error" {
			t.Errorf("%s: %d %v\nwant %s", name, status, body, test.want)
		}
	}
}

// Tool markers work as on Chat Completions: one call per marker the request
// offers, parallel calls in one answer, the arguments streamed in pieces, and
// the call IDs of a later round never repeat an earlier one.
func TestResponsesToolMarkersAndParallelCalls(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tools := []map[string]any{{"type": "function", "name": "lookup_note"}, {"type": "function", "name": "get_weather"}}
	input := []map[string]any{{"role": "user", "content": "go " + ToolMarkerFor("lookup_note", `{"topic":"cube"}`) + " " +
		ToolMarkerFor("get_weather", `{"city":"Paris"}`) + " " + ToolMarkerFor("not_offered", "")}}
	status, body := f.respondJSON(map[string]any{"model": "m", "input": input, "tools": tools})
	output := dig(t, body, "output").([]any)
	if status != http.StatusOK || len(output) != 2 {
		t.Fatalf("output = %d %v", status, body)
	}
	for index, want := range []struct{ name, arguments string }{{"lookup_note", `{"topic":"cube"}`}, {"get_weather", `{"city":"Paris"}`}} {
		item := output[index].(map[string]any)
		if item["type"] != "function_call" || item["name"] != want.name || item["arguments"] != want.arguments ||
			item["status"] != "completed" || item["call_id"] != fmt.Sprintf("call_fake_%d", index) {
			t.Errorf("item %d = %v", index, item)
		}
	}
	// The same request streamed: two runs of events, one per output_index.
	frames := f.respondStream(map[string]any{"model": "m", "input": input, "tools": tools})
	added := 0
	for _, frame := range frames {
		if frame.event == "response.output_item.added" {
			if frame.data["output_index"] != json.Number(fmt.Sprint(added)) {
				t.Errorf("output_index of item %d = %v", added, frame.data["output_index"])
			}
			added++
		}
	}
	if added != 2 || frames[len(frames)-1].event != "response.completed" {
		t.Fatalf("added = %d, last = %s", added, frames[len(frames)-1].event)
	}
	// A second round (the outputs are back) answers in text, and a new call in
	// it gets IDs that differ from the first round's.
	round := append(append([]map[string]any{}, input...),
		map[string]any{"type": "function_call", "call_id": "call_fake_0", "name": "lookup_note", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "call_fake_0", "output": "x"},
		map[string]any{"role": "user", "content": "again " + ToolMarkerFor("lookup_note", "")})
	_, second := f.respondJSON(map[string]any{"model": "m", "input": round, "tools": tools})
	if dig(t, second, "output", 0, "call_id") == "call_fake_0" {
		t.Fatalf("a later round reuses a call ID: %v", second["output"])
	}
	// After the tool result the model answers in text.
	text := append(append([]map[string]any{}, input[:1]...),
		map[string]any{"type": "function_call", "call_id": "c1", "name": "lookup_note", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "x"})
	_, answer := f.respondJSON(map[string]any{"model": "m", "input": text, "tools": tools})
	if dig(t, answer, "output", 0, "type") != "message" || dig(t, answer, "output", 0, "content", 0, "text") != FixedReply {
		t.Fatalf("answer = %v", answer["output"])
	}
}

// Script markers count the rounds of function calls in input: each run of
// function_call items is one round, however many calls it holds.
func TestResponsesScriptMarkerCountsRounds(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tools := []map[string]any{{"type": "function", "name": "a_tool"}, {"type": "function", "name": "b_tool"}}
	user := map[string]any{"role": "user", "content": "go [[fake:script:a_tool {\"n\":1}|b_tool {\"n\":2}|done]]"}
	call := func(id, name string) map[string]any {
		return map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": "{}"}
	}
	output := func(id string) map[string]any {
		return map[string]any{"type": "function_call_output", "call_id": id, "output": "x"}
	}
	for name, test := range map[string]struct {
		input []map[string]any
		want  string // the tool called, or "text"
	}{
		"first round":            {[]map[string]any{user}, "a_tool"},
		"second round":           {[]map[string]any{user, call("1", "a_tool"), output("1")}, "b_tool"},
		"two calls in one round": {[]map[string]any{user, call("1", "a_tool"), call("2", "a_tool"), output("1"), output("2")}, "b_tool"},
		"third round":            {[]map[string]any{user, call("1", "a_tool"), output("1"), call("2", "b_tool"), output("2")}, "text"},
	} {
		_, body := f.respondJSON(map[string]any{"model": "m", "input": test.input, "tools": tools})
		got := "text"
		if dig(t, body, "output", 0, "type") == "function_call" {
			got = dig(t, body, "output", 0, "name").(string)
		}
		if got != test.want {
			t.Errorf("%s: answered %s, want %s", name, got, test.want)
		}
	}
}

// The system-contains question reads the instructions, as Chat Completions
// reads a system message.
func TestResponsesSystemContainsReadsTheInstructions(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	ask := map[string]any{"role": "user", "content": "[[fake:system-contains PLAN_RULES]]"}
	for _, test := range []struct {
		name         string
		instructions string
		want         string
	}{
		{"present", "Follow PLAN_RULES strictly.", "present: PLAN_RULES"},
		{"absent", "Be brief.", "absent: PLAN_RULES"},
	} {
		_, body := f.respondJSON(map[string]any{"model": "m", "instructions": test.instructions, "input": []map[string]any{ask}})
		if got := dig(t, body, "output", 0, "content", 0, "text"); got != test.want {
			t.Errorf("%s: %v", test.name, got)
		}
	}
	// A system message in input counts too.
	_, body := f.respondJSON(map[string]any{"model": "m", "input": []map[string]any{{"role": "system", "content": "PLAN_RULES"}, ask}})
	if dig(t, body, "output", 0, "content", 0, "text") != "present: PLAN_RULES" {
		t.Fatalf("system message = %v", body["output"])
	}
}

// The reasoning marker adds the reasoning item the real model returned at effort
// xhigh (its shape is checked against the sample by
// TestResponsesReasoningItemHasTheObservedShapeAndThreeTokens): it carries
// encrypted content only when the request asked for it, and the usage reports
// the reasoning and cached counts (the counts are not from a sample).
func TestResponsesReasoningMarkerAddsAReasoningItem(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	input := []map[string]any{{"role": "user", "content": "think " + ReasoningMarker + " " + ToolMarkerFor("lookup_note", "")}}
	tools := []map[string]any{{"type": "function", "name": "lookup_note"}}
	for name, include := range map[string][]string{"included": {"reasoning.encrypted_content"}, "not included": nil} {
		_, body := f.respondJSON(map[string]any{"model": "gpt-astra", "input": input, "tools": tools, "include": include})
		first := dig(t, body, "output", 0).(map[string]any)
		_, hasEncrypted := first["encrypted_content"]
		if first["type"] != "reasoning" || hasEncrypted != (include != nil) || dig(t, body, "output", 1, "type") != "function_call" {
			t.Errorf("%s: output = %v", name, body["output"])
		}
		if dig(t, body, "usage", "output_tokens_details", "reasoning_tokens") != float64(5) ||
			dig(t, body, "usage", "input_tokens_details", "cached_tokens") != float64(10) ||
			dig(t, body, "usage", "output_tokens") != float64(14) || dig(t, body, "usage", "total_tokens") != float64(36) {
			t.Errorf("%s: usage = %v", name, body["usage"])
		}
	}
	// Streamed: the item's events come first, with the summary events only when a
	// summary was asked for.
	frames := f.respondStream(map[string]any{"model": "gpt-astra", "input": input, "tools": tools, "include": []string{"reasoning.encrypted_content"}})
	if got := types(frames)[2:5]; !slices.Equal(got, []string{"response.output_item.added", "response.output_item.done", "response.output_item.added"}) {
		t.Fatalf("events = %v", types(frames))
	}
	frames = f.respondStream(map[string]any{"model": "gpt-astra", "input": input, "tools": tools, "reasoning": map[string]any{"effort": "high", "summary": "auto"}})
	if got := types(frames)[2:8]; !slices.Equal(got, []string{"response.output_item.added", "response.reasoning_summary_part.added",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.done", "response.output_item.done"}) {
		t.Fatalf("summary events = %v", types(frames))
	}
}

// The cut, failed and in-stream error answers, and the unknown event.
func TestResponsesEndingsAndUnknownEvents(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tools := []map[string]any{{"type": "function", "name": "lookup_note"}}
	ask := func(marker string) []map[string]any {
		return []map[string]any{{"role": "user", "content": "go " + marker}}
	}
	t.Run("incomplete text", func(t *testing.T) {
		frames := f.respondStream(map[string]any{"model": "m", "input": ask(IncompleteMarker)})
		last := frames[len(frames)-1]
		if last.event != "response.incomplete" || dig(t, last.data, "response", "status") != "incomplete" ||
			dig(t, last.data, "response", "incomplete_details", "reason") != "max_output_tokens" {
			t.Fatalf("last = %v", last.data)
		}
		for _, frame := range frames {
			if strings.HasSuffix(frame.event, ".done") {
				t.Errorf("a cut answer has %s", frame.event)
			}
		}
		if text := dig(t, last.data, "response", "output", 0, "content", 0, "text"); text != FixedReply[:len(FixedReply)/2] ||
			dig(t, last.data, "response", "output", 0, "status") != "incomplete" {
			t.Fatalf("partial = %v", last.data["response"])
		}
	})
	t.Run("incomplete call", func(t *testing.T) {
		frames := f.respondStream(map[string]any{"model": "m", "tools": tools, "input": ask(ToolMarkerFor("lookup_note", `{"topic":"a long topic name"}`) + IncompleteMarker)})
		last := frames[len(frames)-1]
		call := dig(t, last.data, "response", "output", 0).(map[string]any)
		if last.event != "response.incomplete" || call["status"] != "incomplete" || call["arguments"] == `{"topic":"a long topic name"}` {
			t.Fatalf("last = %v", last.data)
		}
	})
	t.Run("incomplete json", func(t *testing.T) {
		_, body := f.respondJSON(map[string]any{"model": "m", "input": ask(IncompleteMarker)})
		if body["status"] != "incomplete" || dig(t, body, "incomplete_details", "reason") != "max_output_tokens" {
			t.Fatalf("body = %v", body)
		}
	})
	t.Run("failed", func(t *testing.T) {
		frames := f.respondStream(map[string]any{"model": "m", "input": ask(FailedMarker)})
		last := frames[len(frames)-1]
		if last.event != "response.failed" || dig(t, last.data, "response", "status") != "failed" ||
			dig(t, last.data, "response", "error", "code") != "server_error" || len(frames) != 3 {
			t.Fatalf("frames = %v", types(frames))
		}
		status, body := f.respondJSON(map[string]any{"model": "m", "input": ask(FailedMarker)})
		if status != http.StatusOK || body["status"] != "failed" || dig(t, body, "error", "code") != "server_error" {
			t.Fatalf("json = %d %v", status, body)
		}
	})
	t.Run("stream error", func(t *testing.T) {
		frames := f.respondStream(map[string]any{"model": "m", "input": ask(StreamErrorMarker)})
		last := frames[len(frames)-1]
		if last.event != "error" || last.data["code"] != "server_is_overloaded" || len(frames) != 3 ||
			slices.Contains(types(frames), "response.completed") {
			t.Fatalf("frames = %v", types(frames))
		}
	})
	t.Run("unknown event", func(t *testing.T) {
		frames := f.respondStream(map[string]any{"model": "m", "input": ask(UnknownEventMarker)})
		if types(frames)[2] != "response.fake_future_event" || frames[len(frames)-1].event != "response.completed" {
			t.Fatalf("frames = %v", types(frames))
		}
		_, body := f.respondJSON(map[string]any{"model": "m", "input": ask(UnknownEventMarker)})
		if body["fake_future_member"] == nil {
			t.Fatalf("body = %v", body)
		}
	})
}

// A missing key, a missing model or input, an unknown model scenario and the
// shared fault scenarios answer as the chat endpoint does.
func TestResponsesRequestFaults(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	response, raw := f.send("POST", "/responses", "", map[string]any{"model": "m", "input": "hi"})
	if response.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") || len(raw) == 0 {
		t.Fatalf("no key = %d %q", response.StatusCode, raw)
	}
	for name, test := range map[string]struct {
		body  map[string]any
		param string
	}{
		"no model":     {map[string]any{"input": "hi"}, "model"},
		"no input":     {map[string]any{"model": "m"}, "input"},
		"empty list":   {map[string]any{"model": "m", "input": []any{}}, "input"},
		"blank string": {map[string]any{"model": "m", "input": "  "}, "input"},
	} {
		status, body := f.respondJSON(test.body)
		if status != http.StatusBadRequest || dig(t, body, "error", "param") != test.param ||
			dig(t, body, "error", "code") != "missing_required_parameter" {
			t.Errorf("%s: %d %v", name, status, body)
		}
	}
	response, raw = f.respond(map[string]any{"model": "m", "input": "hi"}, "X-Fake-Scenario", "model-not-found")
	if response.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "model_not_found") {
		t.Fatalf("model-not-found = %d %s", response.StatusCode, raw)
	}
	response, _ = f.respond(map[string]any{"model": "m", "input": "hi"}, "X-Fake-Scenario", "rate-limit")
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("rate-limit = %d", response.StatusCode)
	}
	response, _ = f.respond(map[string]any{"model": "m", "input": "hi"}, "X-Fake-Scenario", "service-unavailable")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("service-unavailable = %d", response.StatusCode)
	}
	response, raw = f.send("POST", "/responses", "test-key", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("no body = %d %s", response.StatusCode, raw)
	}
}

// The pictures of a Responses request are recorded as those of a chat request,
// for the fake stack's vision-budget assertions (#247).
func TestResponsesRequestsAreRecordedLikeChatRequests(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	f := &fixture{t: t, server: server}
	f.respond(map[string]any{"model": "gpt-astra", "instructions": "system text", "input": []map[string]any{{
		"role": "user", "content": []map[string]any{
			{"type": "input_text", "text": "look at this"},
			{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "high"},
		}}}})
	records := fake.Env().ChatRequests()
	if len(records) != 1 || records[0].Model != "gpt-astra" || len(records[0].Messages) != 2 {
		t.Fatalf("records = %+v", records)
	}
	user := records[0].Messages[1]
	if records[0].Messages[0].Role != "system" || user.Role != "user" || !strings.Contains(user.Text, "look at this") ||
		len(user.Images) != 1 || user.Images[0].Detail != "high" || !user.Images[0].DataURI || user.Images[0].Bytes != 3 {
		t.Fatalf("records = %+v", records)
	}
}

// Identifiers are fixed placeholders of the real shapes and every response
// has its own.
func TestResponsesIdentifiersArePlaceholdersOfTheRealShapes(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, first := f.respondJSON(map[string]any{"model": "m", "input": "hi"})
	_, second := f.respondJSON(map[string]any{"model": "m", "input": "hi"})
	id, other := first["id"].(string), second["id"].(string)
	if id == other || !strings.HasPrefix(id, "resp_") || len(id) != 55 {
		t.Fatalf("ids = %q %q", id, other)
	}
	messageID := dig(t, first, "output", 0, "id").(string)
	if !strings.HasPrefix(messageID, "msg_") || len(messageID) != 54 {
		t.Fatalf("message id = %q", messageID)
	}
	// The sample's own identifiers have these shapes (resp_, msg_, fc_ and rs_
	// with 50 characters, call_ with 24): the fake's are the same length.
	_, calls := f.respondJSON(map[string]any{"model": "m", "tools": []map[string]any{{"type": "function", "name": "t"}},
		"input": "go " + ReasoningMarker + " " + ToolMarkerFor("t", ""), "include": []string{"reasoning.encrypted_content"}})
	reasoningID, callItemID := dig(t, calls, "output", 0, "id").(string), dig(t, calls, "output", 1, "id").(string)
	if !strings.HasPrefix(reasoningID, "rs_") || len(reasoningID) != 53 || !strings.HasPrefix(callItemID, "fc_") || len(callItemID) != 53 {
		t.Fatalf("reasoning id = %q, function call id = %q", reasoningID, callItemID)
	}
}

// #579: RefusalMarker answers with a refusal content part, streamed as the
// official response.refusal.delta / response.refusal.done events (members
// content_index, delta or refusal, item_id, output_index, sequence_number).
func TestResponsesRefusalMarkerStreamsTheRefusalEvents(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	input := []map[string]any{{"role": "user", "content": "go " + RefusalMarker}}
	frames := f.respondStream(map[string]any{"model": "m", "input": input})
	byType := dataByType(frames)
	if len(byType["response.output_text.delta"]) != 0 || len(byType["response.refusal.delta"]) < 2 || len(byType["response.refusal.done"]) != 1 {
		t.Fatalf("frames = %v", types(frames))
	}
	var streamed strings.Builder
	for _, delta := range byType["response.refusal.delta"] {
		for _, member := range []string{"content_index", "delta", "item_id", "output_index", "sequence_number", "type"} {
			if _, has := delta[member]; !has {
				t.Fatalf("refusal.delta lacks %s: %v", member, delta)
			}
		}
		if len(delta) != 6 {
			t.Fatalf("refusal.delta members: %v", delta)
		}
		streamed.WriteString(delta["delta"].(string))
	}
	done := byType["response.refusal.done"][0]
	if len(done) != 6 || done["refusal"] != FixedRefusal || streamed.String() != FixedRefusal || done["content_index"] == nil {
		t.Fatalf("refusal.done = %v, streamed %q", done, streamed.String())
	}
	if part := dig(t, byType["response.content_part.added"][0], "part").(map[string]any); part["type"] != "refusal" || part["refusal"] != "" {
		t.Fatalf("part added = %v", part)
	}
	order := collapsed(types(frames))
	want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added",
		"response.refusal.delta", "response.refusal.done", "response.content_part.done", "response.output_item.done", "response.completed"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v", order)
	}
	last := frames[len(frames)-1]
	if dig(t, last.data, "response", "output", 0, "content", 0, "type") != "refusal" ||
		dig(t, last.data, "response", "output", 0, "content", 0, "refusal") != FixedRefusal {
		t.Fatalf("completed = %v", last.data["response"])
	}
	// The non-stream answer carries the same refusal part.
	status, body := f.respondJSON(map[string]any{"model": "m", "input": input})
	if status != http.StatusOK || dig(t, body, "output", 0, "content", 0, "type") != "refusal" ||
		dig(t, body, "output", 0, "content", 0, "refusal") != FixedRefusal {
		t.Fatalf("json = %d %v", status, body)
	}
	// Without the marker the answer is the text.
	if frames := f.respondStream(map[string]any{"model": "m", "input": "hi"}); len(dataByType(frames)["response.refusal.delta"]) != 0 {
		t.Fatalf("a refusal nobody asked for: %v", types(frames))
	}
}
