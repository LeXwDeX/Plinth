package openaicompatible_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

func streamOf(t *testing.T, body string) agents.Completion {
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
	completion, err := adapter.Stream(context.Background(), agents.Request{Model: "configured-model",
		Messages: []agents.Message{{Role: "user", Content: "hello"}}}, func(agents.Delta) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return completion
}

// #665: a stream that ended at the output limit or by the content filter says
// so; an ordinary stop reports nothing.
func TestAdapterReportsTheFinishReasonsTheTurnActsOn(t *testing.T) {
	for reason, want := range map[string]string{
		"stop": "", "tool_calls": "", "length": agents.FinishLength, "content_filter": agents.FinishContentFilter,
	} {
		completion := streamOf(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\""+reason+"\"}]}\n\n")
		if completion.FinishReason != want || completion.Content != "hi" {
			t.Fatalf("%s: completion = %#v", reason, completion)
		}
	}
}

// #665: a gateway that repeats the whole call ID and name in every fragment,
// or leaves out the index, still yields whole, separate calls.
func TestAdapterAssemblesRepeatedAndUnindexedToolCallFragments(t *testing.T) {
	repeated := streamOf(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"\\\"paint\\\"}\"}}]}}]}\n\n"+
			"data: [DONE]\n\n")
	if len(repeated.ToolCalls) != 1 || repeated.ToolCalls[0].ID != "call-a" || repeated.ToolCalls[0].Name != "refine_image" ||
		string(repeated.ToolCalls[0].Arguments) != `{"prompt":"paint"}` {
		t.Fatalf("repeated fragments = %#v", repeated.ToolCalls)
	}
	unindexed := streamOf(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-a\",\"function\":{\"name\":\"a\",\"arguments\":\"{\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-b\",\"function\":{\"name\":\"b\",\"arguments\":\"{}\"}}]}}]}\n\n"+
			"data: [DONE]\n\n")
	if len(unindexed.ToolCalls) != 2 || unindexed.ToolCalls[0].Name != "a" || string(unindexed.ToolCalls[0].Arguments) != "{}" ||
		unindexed.ToolCalls[1].ID != "call-b" || unindexed.ToolCalls[1].Name != "b" {
		t.Fatalf("unindexed fragments = %#v", unindexed.ToolCalls)
	}
}

// #665: an unindexed fragment after explicitly indexed ones continues the
// last call instead of reading a call that is not there; a name streamed
// character by character keeps its repeated characters; and a gateway that
// numbers parallel calls all 0 still yields one call per distinct call ID.
func TestAdapterAssemblesMixedSingleCharacterAndSameIndexToolCallFragments(t *testing.T) {
	mixed := streamOf(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":3,\"id\":\"call-a\",\"function\":{\"name\":\"a\",\"arguments\":\"{\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call-a\",\"function\":{\"arguments\":\"}\"}}]}}]}\n\n"+
			"data: [DONE]\n\n")
	if len(mixed.ToolCalls) != 1 || mixed.ToolCalls[0].ID != "call-a" || string(mixed.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("mixed fragments = %#v", mixed.ToolCalls)
	}
	characters := streamOf(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"l\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"l\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"m\",\"arguments\":\"{}\"}}]}}]}\n\n"+
			"data: [DONE]\n\n")
	if len(characters.ToolCalls) != 1 || characters.ToolCalls[0].Name != "llm" {
		t.Fatalf("character fragments = %#v", characters.ToolCalls)
	}
	parallel := streamOf(t,
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"a\",\"arguments\":\"{\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-b\",\"function\":{\"name\":\"b\",\"arguments\":\"{\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n"+
			"data: [DONE]\n\n")
	if len(parallel.ToolCalls) != 2 || parallel.ToolCalls[0].ID != "call-a" || parallel.ToolCalls[0].Name != "a" ||
		string(parallel.ToolCalls[0].Arguments) != "{}" || parallel.ToolCalls[1].ID != "call-b" ||
		parallel.ToolCalls[1].Name != "b" || string(parallel.ToolCalls[1].Arguments) != "{}" {
		t.Fatalf("same-index fragments = %#v", parallel.ToolCalls)
	}
}
