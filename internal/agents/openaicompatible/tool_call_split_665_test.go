package openaicompatible_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

func streamWithTools(t *testing.T, body string, tools ...string) agents.Completion {
	t.Helper()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://configured-agent.test/v1/", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16, Client: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agents.Request{Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hello"}}}
	for _, name := range tools {
		request.Tools = append(request.Tools, agents.Tool{Name: name, Parameters: json.RawMessage(`{"type":"object"}`)})
	}
	completion, err := adapter.Stream(context.Background(), request, func(agents.Delta) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return completion
}

// #665 wave 4: on a gateway without indexes, a call that is not whole (a
// hallucinated or prefixed name, arguments that are not JSON) followed by a
// fragment with a new complete ID and a name is two calls, not one call with
// both IDs joined and both names appended. ID pieces still join.
func TestAdapterSplitsANewCallAfterOneThatIsNotWhole665(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []agents.ToolCall
	}{
		"hallucinated name, then a new call": {
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_a1\",\"function\":{\"name\":\"made_up_tool\",\"arguments\":\"{}\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_b2\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{}\"}}]}}]}\n\n",
			[]agents.ToolCall{{ID: "call_a1", Name: "made_up_tool", Arguments: []byte(`{}`)},
				{ID: "call_b2", Name: "refine_image", Arguments: []byte(`{}`)}},
		},
		"prefixed name, then a new call at index 0": {
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"functions.refine_image\",\"arguments\":\"{}\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-b\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{}\"}}]}}]}\n\n",
			[]agents.ToolCall{{ID: "call-a", Name: "functions.refine_image", Arguments: []byte(`{}`)},
				{ID: "call-b", Name: "refine_image", Arguments: []byte(`{}`)}},
		},
		"arguments not JSON, then a new call": {
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"toolu_01\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"toolu_02\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{}\"}}]}}]}\n\n",
			[]agents.ToolCall{{ID: "toolu_01", Name: "refine_image", Arguments: []byte(`{"prompt":`)},
				{ID: "toolu_02", Name: "refine_image", Arguments: []byte(`{}`)}},
		},
		"id, name and arguments in pieces with declared tools": {
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-\",\"function\":{\"name\":\"refine_\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"image\",\"arguments\":\"\\\"paint\\\"}\"}}]}}]}\n\n",
			[]agents.ToolCall{{ID: "call-a", Name: "refine_image", Arguments: []byte(`{"prompt":"paint"}`)}},
		},
	} {
		got := streamWithTools(t, tc.body+"data: [DONE]\n\n", "refine_image").ToolCalls
		if len(got) != len(tc.want) {
			t.Fatalf("%s: calls = %#v", name, got)
		}
		for i := range got {
			if got[i].ID != tc.want[i].ID || got[i].Name != tc.want[i].Name || string(got[i].Arguments) != string(tc.want[i].Arguments) {
				t.Fatalf("%s: call %d = %#v, want %#v", name, i, got[i], tc.want[i])
			}
		}
	}
}
