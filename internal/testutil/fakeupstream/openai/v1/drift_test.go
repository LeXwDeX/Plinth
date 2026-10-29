package openaiv1

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func loadSamples(t *testing.T) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		t.Fatal(err)
	}
	return samples
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

	for _, exchange := range samples.Find("GET /v1/models", "") {
		if exchange.Response.Status != http.StatusOK {
			continue
		}
		_, raw := f.send("GET", "/models", "k", nil)
		compareJSON("models", exchange, raw)
	}
	checked := map[string]bool{}
	for _, exchange := range samples.Find("POST /v1/chat/completions", "") {
		if exchange.Response.Status == http.StatusBadRequest {
			_, raw := f.send("POST", "/chat/completions", "k", map[string]any{})
			compareJSON("error", exchange, raw)
			checked["error"] = true
			continue
		}
		hasTool := strings.Contains(string(exchange.Response.Body.JSON), `"tool_calls"`) || strings.Contains(exchange.Response.Body.SSE, `"tool_calls"`)
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

// Every endpoint row names a committed sample, and the table matches the routes.
func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
	for _, endpoint := range endpoints {
		if endpoint.Evidence != fakeupstream.EvidenceReal || len(samples.Find(endpoint.Sample, "")) == 0 {
			t.Errorf("%s has no real sample %q", endpoint.Key(), endpoint.Sample)
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
