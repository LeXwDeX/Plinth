package openaiv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/dsml"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #536: the chat forms the fake gained, held to the contracts of the vendors
// and to the way Plinth reads them.

func pngDataURI(t *testing.T, width, height int) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func withPicture(t *testing.T, text, uri string) []map[string]any {
	t.Helper()
	return []map[string]any{{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": text},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri, "detail": "low"}},
	}}}
}

// --- I29: the stream shape follows the model's family ---

func TestStreamUsageRidesOnTheShapeOfTheModelFamily(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	stream := func(model string, includeUsage bool) []map[string]any {
		body := map[string]any{"model": model, "messages": user("hi"), "stream": true}
		if includeUsage {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
		_, raw := f.send("POST", "/chat/completions", "k", body)
		chunks, done := sseEvents(t, raw)
		if !done {
			t.Fatalf("%s: no [DONE]", model)
		}
		return chunks
	}

	// DashScope: the finish chunk has no usage, the last chunk has empty
	// choices and the usage.
	for _, model := range []string{"qwen3.8-omni-flash", "qwen4-max", "Qwen-Max"} {
		chunks := stream(model, true)
		last := chunks[len(chunks)-1]
		choices, _ := last["choices"].([]any)
		if choices == nil || len(choices) != 0 || dig(t, last, "usage", "total_tokens") == nil {
			t.Fatalf("%s: last chunk = %v", model, last)
		}
		finish := chunks[len(chunks)-2]
		if dig(t, finish, "choices", 0, "finish_reason") != "stop" || dig(t, finish, "usage") != nil {
			t.Fatalf("%s: finish chunk = %v", model, finish)
		}
	}
	// Without include_usage there is no usage chunk at all.
	chunks := stream("qwen3.8-omni-flash", false)
	if dig(t, chunks[len(chunks)-1], "choices", 0, "finish_reason") != "stop" || dig(t, chunks[len(chunks)-1], "usage") != nil {
		t.Fatalf("qwen without include_usage: %v", chunks[len(chunks)-1])
	}
	// DeepSeek (and any other model): the usage rides on the finish chunk.
	for _, model := range []string{"deepseek", "deepseek-v4", "some-other-model"} {
		chunks := stream(model, true)
		last := chunks[len(chunks)-1]
		if dig(t, last, "choices", 0, "finish_reason") != "stop" || dig(t, last, "usage", "total_tokens") == nil {
			t.Fatalf("%s: last chunk = %v", model, last)
		}
	}
}

// --- I27: pictures the model refuses ---

func TestPicturesAreRefusedByTheRulesOfTheModel(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	tiny, big := pngDataURI(t, 1, 1), pngDataURI(t, 64, 64)
	post := func(model string, uri string) (int, map[string]any) {
		return f.chat(map[string]any{"model": model, "messages": withPicture(t, "look", uri)})
	}

	// Observed: a qwen model refuses a 1-pixel picture, in the gateway's body.
	status, body := post("qwen3.8-omni-flash", tiny)
	message, _ := dig(t, body, "error", "message").(string)
	if status != http.StatusBadRequest || dig(t, body, "error", "code") != "invalid_parameter_error" ||
		dig(t, body, "error", "type") != "invalid_parameter_error" || dig(t, body, "error", "param") != nil ||
		message != "The image length and width do not meet the model restrictions. [height:1 or width:1 must be larger than 32]" {
		t.Fatalf("qwen 1px picture: %d %v", status, body)
	}
	// A picture of 32 pixels is still too small ("larger than 32"), 33 is not.
	if status, _ := post("qwen4-max", pngDataURI(t, 32, 100)); status != http.StatusBadRequest {
		t.Fatalf("32px: %d", status)
	}
	for _, uri := range []string{pngDataURI(t, 33, 33), big} {
		if status, body := post("qwen3.8-omni-flash", uri); status != http.StatusOK {
			t.Fatalf("a picture the model reads was refused: %d %v", status, body)
		}
	}
	// A link, or bytes the library cannot read, are not judged (no rule can).
	for _, uri := range []string{"https://assets.example.test/p.png", "data:image/webp;base64,AAAA"} {
		if status, _ := post("qwen3.8-omni-flash", uri); status != http.StatusOK {
			t.Fatalf("%q judged: %d", uri, status)
		}
	}
	// Models with no picture rule read any picture, even a 1-pixel one.
	for _, model := range []string{"gpt-astra", "fake-vision-model", "claude-x"} {
		var status int
		if model == "gpt-astra" {
			// gpt-astra refuses tools and temperature, not pictures; its effort
			// is the model's own.
			status, _ = f.chat(map[string]any{"model": model, "messages": withPicture(t, "look", tiny), "reasoning_effort": "low"})
		} else {
			status, _ = post(model, tiny)
		}
		if status != http.StatusOK {
			t.Fatalf("%s refused a picture: %d", model, status)
		}
	}
	// Observed: a model without image input answers in its own envelope.
	status, body = post(TextOnlyModelPrefix+"-1", big)
	if status != http.StatusBadRequest || dig(t, body, "error", "code") != "invalid_request_error" ||
		dig(t, body, "error", "message") != "this model does not support image input" {
		t.Fatalf("text-only picture: %d %v", status, body)
	}
	// The default stack model reads pictures here: whether the real one does is
	// open, and refusing would make Plinth strip every picture of the fake stack.
	if status, body := post("deepseek", big); status != http.StatusOK {
		t.Fatalf("deepseek picture refused by default: %d %v", status, body)
	}
	// Text only is untouched.
	if status, _ := f.chat(map[string]any{"model": TextOnlyModelPrefix, "messages": user("hi")}); status != http.StatusOK {
		t.Fatalf("text refused: %d", status)
	}
}

// The marker forces the kinds no request reaches by itself, and every refusal
// is read by the Adapter's classifier the way the Schema's vocabulary says.
func TestImageRefusalMarkersAreClassifiedByTheAdapterClassifier(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	uri := pngDataURI(t, 64, 64)
	for kind, want := range map[string][]string{
		"dimensions":  {"dimensions"},
		"size":        {"size"},
		"format":      {"format"},
		"count":       {"count"},
		"unsupported": nil,
	} {
		t.Run(kind, func(t *testing.T) {
			// Chat Completions, on a model with no picture rule of its own.
			response, raw := f.send("POST", "/chat/completions", "k", map[string]any{
				"model": "fake-vision-model", "messages": withPicture(t, "look "+ImageRefusalMarkerPrefix+kind+"]]", uri),
			})
			chatErr := upstreamhttp.Refusal{Status: response.StatusCode, Body: raw, HasImages: true}.Err()
			// Responses.
			response, raw = f.send("POST", "/responses", "k", map[string]any{
				"model": "fake-vision-model",
				"input": []any{map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "input_text", "text": "look " + ImageRefusalMarkerPrefix + kind + "]]"},
					map[string]any{"type": "input_image", "image_url": uri},
				}}},
			})
			responsesErr := upstreamhttp.Refusal{Status: response.StatusCode, Body: raw, HasImages: true}.Err()
			for name, err := range map[string]error{"chat": chatErr, "responses": responsesErr} {
				var status *agents.StatusError
				if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
					t.Fatalf("%s: status lost: %v", name, err)
				}
				if kind == "unsupported" {
					if !errors.Is(err, agents.ErrImagesRejected) || errors.Is(err, agents.ErrImageInvalid) {
						t.Fatalf("%s: a model without image input is read as %v", name, err)
					}
					continue
				}
				var invalidity *agents.ImageInvalidity
				if !errors.As(err, &invalidity) || strings.Join(invalidity.Reasons, ",") != strings.Join(want, ",") ||
					errors.Is(err, agents.ErrImagesRejected) {
					t.Fatalf("%s: %s is read as %v (%v), want reasons %v", name, kind, err, invalidity, want)
				}
			}
		})
	}
	// A request with no picture is not refused by the marker.
	if status, _ := f.chat(map[string]any{"model": "fake-vision-model", "messages": user("hi " + ImageRefusalMarkerPrefix + "size]]")}); status != http.StatusOK {
		t.Fatalf("text with a marker refused: %d", status)
	}
}

func TestResponsesJudgesPicturesByTheRulesOfTheModelToo(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	send := func(model, uri string) (int, []byte) {
		response, raw := f.send("POST", "/responses", "k", map[string]any{
			"model": model,
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "look"},
				map[string]any{"type": "input_image", "image_url": uri},
			}}},
		})
		return response.StatusCode, raw
	}
	if status, raw := send("qwen3.8-omni-flash", pngDataURI(t, 1, 1)); status != http.StatusBadRequest ||
		!strings.Contains(string(raw), "[height:1 or width:1 must be larger than 32]") ||
		!strings.Contains(string(raw), `"param":"input"`) {
		t.Fatalf("responses 1px: %d %s", status, raw)
	}
	if status, raw := send("qwen3.8-omni-flash", pngDataURI(t, 64, 64)); status != http.StatusOK {
		t.Fatalf("responses 64px: %d %s", status, raw)
	}
	// gpt-astra reads pictures on /responses (the sample's input_image request).
	if status, raw := send("gpt-astra", pngDataURI(t, 1, 1)); status != http.StatusOK {
		t.Fatalf("gpt-astra picture: %d %s", status, raw)
	}
}

// --- m9: DSML tool calls and a picture of the model's own ---

func TestDSMLMarkerWritesTheCallsIntoTheReplyTextTheSchemaParses(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat.DSML
	schema.Enabled = true
	f := newFixture(t, fakeupstream.Config{})
	prompt := "go " + ToolMarkerFor("lookup_note", `{"topic":"cube","limit":3,"deep":true}`) + " " + DSMLMarker

	_, body := f.chat(map[string]any{"model": "deepseek", "messages": user(prompt), "tools": lookupTools})
	message := dig(t, body, "choices", 0, "message").(map[string]any)
	if _, has := message["tool_calls"]; has && message["tool_calls"] != nil {
		t.Fatalf("a DSML reply carries no tool_calls: %v", message)
	}
	content, _ := message["content"].(string)
	parsed := dsml.Parse(schema, content)
	if len(parsed.ToolCalls) != 1 || parsed.ToolCalls[0].Name != "lookup_note" {
		t.Fatalf("DSML not read: %q -> %+v", content, parsed)
	}
	var arguments map[string]any
	if err := json.Unmarshal(parsed.ToolCalls[0].Arguments, &arguments); err != nil ||
		arguments["topic"] != "cube" || arguments["limit"] != float64(3) || arguments["deep"] != true {
		t.Fatalf("arguments = %s (%v)", parsed.ToolCalls[0].Arguments, err)
	}

	// Streamed, the block is split over several chunks and joins to the same text.
	_, raw := f.send("POST", "/chat/completions", "k", map[string]any{
		"model": "deepseek", "messages": user(prompt), "tools": lookupTools, "stream": true,
	})
	chunks, done := sseEvents(t, raw)
	var streamed strings.Builder
	pieces := 0
	for _, chunk := range chunks {
		if piece, ok := dig(t, chunk, "choices", 0, "delta", "content").(string); ok && piece != "" {
			streamed.WriteString(piece)
			pieces++
		}
		if _, has := fakeupstream.Dig(chunk, "choices", 0, "delta", "tool_calls"); has {
			t.Fatalf("a streamed DSML reply carries tool_calls: %v", chunk)
		}
	}
	if !done || pieces < 2 || streamed.String() != content ||
		dig(t, chunks[len(chunks)-1], "choices", 0, "finish_reason") != "stop" {
		t.Fatalf("streamed DSML: done=%t pieces=%d text=%q", done, pieces, streamed.String())
	}
	// No tool marker: the fixed text, DSML or not.
	_, body = f.chat(map[string]any{"model": "deepseek", "messages": user("hi " + DSMLMarker), "tools": lookupTools})
	if dig(t, body, "choices", 0, "message", "content") != FixedReply {
		t.Fatalf("no call to write: %v", body)
	}
}

func TestInlineImageMarkerDrawsAPictureInDeltaAndMessage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, body := f.chat(map[string]any{"model": "gpt-astra", "messages": user("draw " + InlineImageMarker), "reasoning_effort": "low"})
	url, _ := dig(t, body, "choices", 0, "message", "images", 0, "image_url", "url").(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") || dig(t, body, "choices", 0, "message", "content") != FixedReply {
		t.Fatalf("message.images: %v", body)
	}
	if width, height, ok := pictureSize(url); !ok || width != 1 || height != 1 {
		t.Fatalf("the drawn picture is no PNG: %v %v %v", width, height, ok)
	}
	_, raw := f.send("POST", "/chat/completions", "k", map[string]any{
		"model": "gpt-astra", "messages": user("draw " + InlineImageMarker), "stream": true, "reasoning_effort": "low",
	})
	chunks, done := sseEvents(t, raw)
	drawn := 0
	for _, chunk := range chunks {
		if _, has := fakeupstream.Dig(chunk, "choices", 0, "delta", "images"); has {
			drawn++
		}
	}
	if !done || drawn != 1 {
		t.Fatalf("delta.images chunks = %d (done=%t)", drawn, done)
	}
	// Without the marker nothing is drawn.
	_, body = f.chat(map[string]any{"model": "gpt-astra", "messages": user("hi"), "reasoning_effort": "low"})
	if _, has := dig(t, body, "choices", 0, "message").(map[string]any)["images"]; has {
		t.Fatalf("a picture nobody asked for: %v", body)
	}
}

// --- m9: 429 variants ---

func TestQuotaMarkerAnswersEveryDeclaredCodeAndTheRateLimit(t *testing.T) {
	// Every quota code the chat connection's error_mapping declares has a marker.
	declared := map[string]bool{}
	product := catalog.DefaultDocument()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(product, &root); err != nil {
		t.Fatal(err)
	}
	// The connections sit under application.<some key>; find the chat one.
	var find func(raw json.RawMessage)
	find = func(raw json.RawMessage) {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil {
			if id, ok := object["id"]; ok && string(id) == `"chat-llm"` {
				var mapping struct {
					ErrorMapping struct {
						UpstreamCodeMap map[string]json.RawMessage `json:"upstream_code_map"`
					} `json:"error_mapping"`
				}
				if json.Unmarshal(raw, &mapping) == nil {
					for code := range mapping.ErrorMapping.UpstreamCodeMap {
						declared[code] = true
					}
				}
			}
			for _, child := range object {
				find(child)
			}
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			for _, child := range list {
				find(child)
			}
		}
	}
	for _, raw := range root {
		find(raw)
	}
	if len(declared) == 0 {
		t.Fatal("the chat connection declares no quota codes")
	}
	for code := range declared {
		if _, has := QuotaCodes[code]; !has {
			t.Errorf("the Schema declares %q as a quota code and the fake has no marker for it", code)
		}
	}

	f := newFixture(t, fakeupstream.Config{})
	for code, kind := range QuotaCodes {
		for _, path := range []string{"/chat/completions", "/responses"} {
			payload := map[string]any{"model": "deepseek", "messages": user("hi " + QuotaMarkerPrefix + code + "]]")}
			if path == "/responses" {
				payload = map[string]any{"model": "deepseek", "input": "hi " + QuotaMarkerPrefix + code + "]]"}
			}
			response, raw := f.send("POST", path, "k", payload)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			if response.StatusCode != http.StatusTooManyRequests || dig(t, body, "error", "code") != code ||
				dig(t, body, "error", "type") != kind {
				t.Fatalf("%s %s: %d %s", path, code, response.StatusCode, raw)
			}
			if (code == "rate_limit_exceeded") != (response.Header.Get("Retry-After") != "") {
				t.Fatalf("%s %s: Retry-After %q", path, code, response.Header.Get("Retry-After"))
			}
		}
	}
	// An unknown code is no marker.
	if status, _ := f.chat(map[string]any{"model": "deepseek", "messages": user("hi " + QuotaMarkerPrefix + "made_up]]")}); status != http.StatusOK {
		t.Fatalf("unknown quota code: %d", status)
	}
}

// --- m9: DeepSeek reasoning_effort ---

func TestDeepSeekRefusesAReasoningEffortItDoesNotKnow(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, effort := range []string{"low", "high", "max", "medium", "xhigh", "minimal", "ultra", "none"} {
		if status, body := f.chat(map[string]any{"model": "deepseek", "messages": user("hi"), "reasoning_effort": effort}); status != http.StatusOK {
			t.Fatalf("effort %q refused: %d %v", effort, status, body)
		}
	}
	for _, model := range []string{"deepseek", "deepseek-v4"} {
		status, body := f.chat(map[string]any{"model": model, "messages": user("hi"), "reasoning_effort": "extreme"})
		if status != http.StatusBadRequest || dig(t, body, "error", "param") != "reasoning_effort" ||
			dig(t, body, "error", "code") != "invalid_value" || dig(t, body, "error", "type") != "invalid_request_error" {
			t.Fatalf("%s effort extreme: %d %v", model, status, body)
		}
	}
	// A model with no list takes any value, as before.
	if status, _ := f.chat(map[string]any{"model": "qwen3.8-omni-flash", "messages": user("hi"), "reasoning_effort": "extreme"}); status != http.StatusOK {
		t.Fatalf("qwen effort: %d", status)
	}
}

// --- I28: the broad reading of the reasoning_content rule, behind a switch ---

func TestStrictReasoningMarkerSwitchesToTheBroadReading(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	history := func(extra string, textRoundReasoning *string) []map[string]any {
		textRound := map[string]any{"role": "assistant", "content": "an earlier text answer"}
		if textRoundReasoning != nil {
			textRound["reasoning_content"] = *textRoundReasoning
		}
		return []map[string]any{
			{"role": "user", "content": "first " + extra},
			textRound,
			{"role": "user", "content": "second"},
		}
	}
	chat := func(messages []map[string]any) int {
		status, _ := f.chat(map[string]any{"model": "deepseek", "messages": messages, "tools": lookupTools})
		return status
	}
	empty := ""
	// The default reading: only the assistant message that calls tools needs it.
	if status := chat(history("", nil)); status != http.StatusOK {
		t.Fatalf("default reading refused a text round: %d", status)
	}
	// The broad reading: with tools in the request, every assistant message.
	if status := chat(history(StrictReasoningMarker, nil)); status != http.StatusBadRequest {
		t.Fatalf("strict reading accepted a text round without reasoning_content: %d", status)
	}
	if status := chat(history(StrictReasoningMarker, &empty)); status != http.StatusOK {
		t.Fatalf("strict reading refused the empty reasoning_content a round without reasoning is sent back with: %d", status)
	}
	// No tools in the request: nothing to pass back, strict or not.
	if status, _ := f.chat(map[string]any{"model": "deepseek", "messages": history(StrictReasoningMarker, nil)}); status != http.StatusOK {
		t.Fatalf("strict reading without tools: %d", status)
	}
}
