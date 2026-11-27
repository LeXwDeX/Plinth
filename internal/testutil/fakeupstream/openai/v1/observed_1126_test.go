package openaiv1

import (
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The answers formal-test showed on 2026-11-26 (#638, #640), as the fake
// gives them. The Adapters' tests drive the same answers through the real
// Adapters (internal/agents/openairesponses, openaicompatible, internal/app).

// SelfImageMarker: a reasoning item and a picture the model drew itself, the
// three observed progress events, the image_generation tool echoed after the
// request's tools, tool_usage.image_gen, and lines over the 1 MiB line bound.
func TestSelfImageMarkerDrawsAPictureTheRequestNeverAskedFor(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tools := []map[string]any{{"type": "function", "name": "lookup_note"}}
	request := map[string]any{"model": "gpt-astra", "include": []string{"reasoning.encrypted_content"}, "tools": tools,
		"input": []map[string]any{{"role": "user", "content": "draw it " + SelfImageMarker + " " + ToolMarkerFor("lookup_note", "")}}}
	frames := f.respondStream(request)
	want := []string{"response.created", "response.in_progress",
		"response.output_item.added", "response.output_item.done",
		"response.output_item.added", "response.image_generation_call.in_progress", "response.image_generation_call.generating",
		"response.image_generation_call.completed", "response.output_item.done", "response.completed"}
	if got := types(frames); !slices.Equal(got, want) {
		t.Fatalf("events = %v", got)
	}
	done := frames[8].data["item"].(map[string]any)
	result, _ := done["result"].(string)
	picture, err := base64.StdEncoding.DecodeString(result)
	if done["type"] != "image_generation_call" || done["status"] != "completed" || err != nil ||
		len(picture) < SelfImagePictureBytes || !strings.HasPrefix(string(picture), "\x89PNG") {
		t.Fatalf("done item = type %v status %v, %d bytes, %v", done["type"], done["status"], len(picture), err)
	}
	completed := frames[9]
	if len(frames[8].raw) <= 1<<20 || len(completed.raw) <= 1<<20 {
		t.Fatalf("lines = %d and %d bytes, want both over 1 MiB", len(frames[8].raw), len(completed.raw))
	}
	response := completed.data["response"].(map[string]any)
	echoed := response["tools"].([]any)
	if len(echoed) != 2 || dig(t, echoed, 0, "name") != "lookup_note" || dig(t, echoed, 1, "type") != "image_generation" ||
		dig(t, echoed, 1, "model") != "gpt-image-2.6-sunburst" || dig(t, echoed, 1, "output_format") != "png" {
		t.Fatalf("tools = %v", echoed)
	}
	if dig(t, response, "tool_usage", "image_gen", "output_tokens") == nil || dig(t, response, "tool_usage", "web_search", "num_requests") == nil {
		t.Fatalf("tool_usage = %v", response["tool_usage"])
	}
	for _, item := range response["output"].([]any) {
		if kind := item.(map[string]any)["type"]; kind != "reasoning" && kind != "image_generation_call" {
			t.Fatalf("output has a %v item", kind)
		}
	}
	// Non-stream: the same two items.
	request["stream"] = false
	status, body := f.respondJSON(request)
	if status != http.StatusOK || dig(t, body, "output", 0, "type") != "reasoning" || dig(t, body, "output", 1, "type") != "image_generation_call" {
		t.Fatalf("non-stream = %d %v", status, body["output"])
	}
}

// The hold before .completed (the real round took 80 to 125 seconds) is opt-in
// and ends when the client goes away.
func TestSelfImageHoldIsOptIn(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	started := time.Now()
	f.respondStream(map[string]any{"model": "gpt-astra", "input": []map[string]any{{"role": "user", "content": "[[fake:self-image:300]]"}}})
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("held %s, want 300ms", elapsed)
	}
	if hold := selfImageHold(&http.Request{Header: http.Header{fakeupstream.DelayHeader: {"999999999"}}}, ""); hold != SelfImageMaxHold {
		t.Fatalf("hold = %s, want the cap", hold)
	}
}

// EmptyCompletionMarker: a reasoning item only and a terminal line over the
// bound; with X-Fake-Fail-Times the later requests answer as usual.
func TestEmptyCompletionMarkerAnswersOnlyReasoning(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	request := map[string]any{"model": "gpt-astra", "stream": true, "include": []string{"reasoning.encrypted_content"},
		"input": []map[string]any{{"role": "user", "content": "go on " + EmptyCompletionMarker}}}
	frames := f.respondStream(request)
	want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.completed"}
	if got := types(frames); !slices.Equal(got, want) {
		t.Fatalf("events = %v", got)
	}
	if len(frames[3].raw) > 1<<20 || len(frames[4].raw) <= EmptyCompletionLineBytes {
		t.Fatalf("done item %d bytes, terminal %d bytes", len(frames[3].raw), len(frames[4].raw))
	}
	output := dig(t, frames[4].data, "response", "output").([]any)
	if len(output) != 1 || dig(t, output, 0, "type") != "reasoning" || dig(t, frames[4].data, "response", "usage") == nil {
		t.Fatalf("output = %d items, usage %v", len(output), dig(t, frames[4].data, "response", "usage"))
	}
	// Once: the first request is empty, the second answers with text.
	for attempt, want := range []string{"reasoning", "message"} {
		response, raw := f.respond(request, fakeupstream.FailTimesHeader, "1")
		frames := responsesStream(t, raw)
		last := frames[len(frames)-1].data
		output := dig(t, last, "response", "output").([]any)
		if response.StatusCode != http.StatusOK || dig(t, output, len(output)-1, "type") != want {
			t.Fatalf("attempt %d: %d, last item %v", attempt+1, response.StatusCode, dig(t, output, len(output)-1, "type"))
		}
	}
}

// DataInspectionMarker: a streamed request with a picture gets the 200 stream
// whose only frame is the refusal; without its picture, or not streamed, the
// ordinary answer.
func TestDataInspectionMarkerRefusesTheRequestsPictures(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	picture, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	withPicture := []map[string]any{{"role": "user", "content": []map[string]any{
		{"type": "text", "text": "look " + DataInspectionMarker},
		{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture.Data)}},
	}}}
	response, raw := f.send("POST", "/chat/completions", "test-key", map[string]any{"model": "qwen-max", "stream": true, "messages": withPicture})
	chunks, done := sseEvents(t, raw)
	if response.StatusCode != http.StatusOK || done || len(chunks) != 1 ||
		dig(t, chunks[0], "error", "code") != "data_inspection_failed" || dig(t, chunks[0], "error", "type") != "data_inspection_failed" ||
		dig(t, chunks[0], "error", "message") != DataInspectionMessage || !strings.HasPrefix(chunks[0]["id"].(string), "chatcmpl-") {
		t.Fatalf("with a picture = %d %s", response.StatusCode, raw)
	}
	if value, present := chunks[0]["error"].(map[string]any)["param"]; !present || value != nil {
		t.Fatalf("param = %v %v, want null", value, present)
	}
	response, raw = f.send("POST", "/chat/completions", "test-key", map[string]any{"model": "qwen-max", "stream": true, "messages": user("look " + DataInspectionMarker)})
	if chunks, done := sseEvents(t, raw); response.StatusCode != http.StatusOK || !done || strings.Contains(string(raw), "data_inspection_failed") || len(chunks) == 0 {
		t.Fatalf("without a picture = %d %s", response.StatusCode, raw)
	}
	if status, body := f.chat(map[string]any{"model": "qwen-max", "messages": withPicture}); status != http.StatusOK || body["error"] != nil {
		t.Fatalf("not streamed = %d %v", status, body)
	}
}

// GatewayMuteMarker: the 429 first, then the gateway's own 503s, muted for 1,
// 2, then 4 minutes with a recovering answer between, on either route.
func TestGatewayMuteMarkerPlaysTheGatewaysQuotaMute(t *testing.T) {
	for _, route := range []string{"/chat/completions", "/responses"} {
		f := newFixture(t, fakeupstream.Config{})
		body := map[string]any{"model": "gpt-astra", "messages": user("hi " + GatewayMuteMarker),
			"input": []map[string]any{{"role": "user", "content": "hi " + GatewayMuteMarker}}}
		wantMinutes := []int{0, 1, 0, 2, 0, 4, 0, 4}
		for n, minutes := range wantMinutes {
			started := time.Now().UTC().Truncate(time.Second)
			response, raw := f.send("POST", route, "test-key", body)
			text := string(raw)
			switch {
			case n == 0:
				if response.StatusCode != http.StatusTooManyRequests || !strings.Contains(text, `"code":"quota_exceeded"`) ||
					!strings.Contains(text, "Quota exceeded (weekly). Limit: $20.00, used: $20.24. Resets at ") || !strings.Contains(text, `"dimension":"weekly"`) {
					t.Fatalf("%s request 1 = %d %s", route, response.StatusCode, raw)
				}
			case minutes > 0:
				_, rest, found := strings.Cut(text, GatewayMutedPrefix)
				until, err := time.Parse(time.RFC3339, strings.SplitN(rest, `"`, 2)[0])
				if response.StatusCode != http.StatusServiceUnavailable || !found || err != nil ||
					until.Sub(started) < time.Duration(minutes)*time.Minute || until.Sub(started) > time.Duration(minutes)*time.Minute+2*time.Second {
					t.Fatalf("%s request %d = %d %s, want muted %d minutes", route, n+1, response.StatusCode, raw, minutes)
				}
			default:
				if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(text, GatewayRecoveringMessage) {
					t.Fatalf("%s request %d = %d %s, want recovering", route, n+1, response.StatusCode, raw)
				}
			}
		}
	}
}
