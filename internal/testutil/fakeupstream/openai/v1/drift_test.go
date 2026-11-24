package openaiv1

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func loadSamples(t *testing.T) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

// hasToolCalls reports whether a sample's answer carries a tool call.
func hasToolCalls(exchange wirefixture.Exchange) bool {
	return strings.Contains(string(exchange.Response.Body.JSON), `"tool_calls"`) ||
		strings.Contains(exchange.Response.Body.SSE, `"tool_calls"`)
}

// newestSamples keeps the newest committed sample of every route and answer
// kind (models, a 400 error, a text or tool-call completion, streamed or not).
// The fake templates from the newest capture, so an older sample of the same
// kind is evidence of what the gateway answered then, not of what the fake
// answers now (#96, #497).
func newestSamples(samples *fakeupstream.Samples) []wirefixture.Exchange {
	kind := func(exchange wirefixture.Exchange) string {
		switch {
		case exchange.Request.Path == "/v1/models":
			return "GET /v1/models"
		case exchange.Response.Status != http.StatusOK:
			return exchange.Request.Method + " " + exchange.Request.Path + " " + strconv.Itoa(exchange.Response.Status)
		case exchange.Response.Body.Encoding == wirefixture.PayloadSSE:
			return "POST /v1/chat/completions sse tool=" + strconv.FormatBool(hasToolCalls(exchange))
		default:
			return "POST /v1/chat/completions json tool=" + strconv.FormatBool(hasToolCalls(exchange))
		}
	}
	sorted := append([]wirefixture.Exchange(nil), samples.Exchanges...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Metadata.CapturedAt.Before(sorted[j].Metadata.CapturedAt)
	})
	held := map[string]bool{}
	var newest []wirefixture.Exchange
	for i := len(sorted) - 1; i >= 0; i-- {
		if held[kind(sorted[i])] {
			continue
		}
		held[kind(sorted[i])] = true
		newest = append(newest, sorted[i])
	}
	return newest
}

func shapeOf(t *testing.T, raw []byte) fakeupstream.Shape {
	t.Helper()
	shape, err := fakeupstream.ShapeOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func paths(shape fakeupstream.Shape) []string {
	out := make([]string, 0, len(shape))
	for path := range shape {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// unionPaths merges the field sets of SSE chunks. SSE compares field sets
// only: the committed sample is a normalized stream whose merged chunks lost
// the null typing of intermediate chunks (documented in the alignment report).
func unionPaths(t *testing.T, chunks []string) []string {
	t.Helper()
	set := map[string]bool{}
	for _, chunk := range chunks {
		for _, path := range paths(shapeOf(t, []byte(chunk))) {
			set[path] = true
		}
	}
	out := make([]string, 0, len(set))
	for path := range set {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func sampleChunks(exchange wirefixture.Exchange) []string {
	var chunks []string
	for _, block := range strings.Split(exchange.Response.Body.SSE, "\n\n") {
		if payload, ok := strings.CutPrefix(strings.TrimSpace(block), "data: "); ok && payload != "[DONE]" {
			chunks = append(chunks, payload)
		}
	}
	return chunks
}

func fakeChunks(t *testing.T, f *fixture, body map[string]any) []string {
	t.Helper()
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	_, raw := f.send("POST", "/chat/completions", "k", body)
	var chunks []string
	for _, block := range strings.Split(string(raw), "\n\n") {
		if payload, ok := strings.CutPrefix(strings.TrimSpace(block), "data: "); ok && payload != "[DONE]" {
			chunks = append(chunks, payload)
		}
	}
	return chunks
}

// The fake's response structure must stay identical to the latest real sample
// of this version for every endpoint (issue #96).
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
	marker := ToolMarkerFor("lookup_note", `{"topic":"cube"}`)

	compareJSON := func(what string, sample wirefixture.Exchange, raw []byte) {
		t.Helper()
		if diff := fakeupstream.DiffShape(shapeOf(t, sample.Response.Body.JSON), shapeOf(t, raw)); !diff.Empty() {
			t.Errorf("%s drifted from the real sample: %s", what, diff)
		}
	}

	checked := map[string]bool{}
	for _, exchange := range newestSamples(samples) {
		if exchange.Request.Path == "/v1/models" {
			if exchange.Response.Status != http.StatusOK {
				continue
			}
			_, raw := f.send("GET", "/models", "k", nil)
			compareJSON("models", exchange, raw)
			continue
		}
		if exchange.Response.Status == http.StatusBadRequest {
			_, raw := f.send("POST", "/chat/completions", "k", map[string]any{})
			compareJSON("error", exchange, raw)
			checked["error"] = true
			continue
		}
		if exchange.Response.Status != http.StatusOK {
			// The 429 daily-quota refusal: evidence for the quota scenario (see
			// newest_capture_test.go), not an answer this route always gives.
			continue
		}
		hasTool := hasToolCalls(exchange)
		text := "hello"
		if hasTool {
			text = marker
		}
		body := map[string]any{"model": "deepseek", "messages": user(text), "thinking": map[string]any{"type": "enabled"}}
		if hasTool {
			body["tools"] = lookupTools
		}
		switch exchange.Response.Body.Encoding {
		case wirefixture.PayloadJSON:
			_, raw := f.send("POST", "/chat/completions", "k", body)
			compareJSON("chat completion (tools="+map[bool]string{true: "yes", false: "no"}[hasTool]+")", exchange, raw)
			checked["json"+map[bool]string{true: "-tool", false: ""}[hasTool]] = true
		case wirefixture.PayloadSSE:
			want := unionPaths(t, sampleChunks(exchange))
			got := unionPaths(t, fakeChunks(t, f, body))
			if !slices.Equal(want, got) {
				t.Errorf("stream (tools=%t) field set drifted:\n sample %v\n fake   %v", hasTool, want, got)
			}
			checked["sse"+map[bool]string{true: "-tool", false: ""}[hasTool]] = true
		}
	}
	for _, want := range []string{"error", "json", "json-tool", "sse", "sse-tool"} {
		if !checked[want] {
			t.Errorf("the sample has no %s exchange to compare", want)
		}
	}
}

// Every endpoint row is a real endpoint with a committed sample, or a
// constructed one that names the sample of its own it is aligned to (#401), and
// the table matches the routes.
func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
	for _, endpoint := range endpoints {
		switch endpoint.Evidence {
		case fakeupstream.EvidenceReal:
			if len(samples.Find(endpoint.Sample, "")) == 0 {
				t.Errorf("%s has no real sample %q", endpoint.Key(), endpoint.Sample)
			}
		case fakeupstream.EvidenceConstructed:
			// POST /responses is the one constructed endpoint: its probe sample is
			// not a wirefixture candidate (see responses_sample.go), and its note
			// says which parts it does not cover.
			if endpoint.Key() != "POST /responses" || !strings.Contains(endpoint.Note, responsesSampleFile) {
				t.Errorf("%s is constructed and does not name its sample", endpoint.Key())
			}
		default:
			t.Errorf("%s has evidence %q", endpoint.Key(), endpoint.Evidence)
		}
		if !strings.HasPrefix(endpoint.DocURL, "https://") || endpoint.CheckedAt == "" {
			t.Errorf("%s: needs a documentation URL and a check date", endpoint.Key())
		}
		response, _ := f.send(endpoint.Method, endpoint.Path, "k", json.RawMessage(`{}`))
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("declared endpoint %s is not routed (%d)", endpoint.Key(), response.StatusCode)
		}
	}
}
