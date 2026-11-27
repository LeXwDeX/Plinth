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

// #684: narration and tool-call fragments that arrive in the same chunk or
// interleaved never mix. The reply text and every streamed text delta hold
// only the narration; the call's arguments stay whole in the call.
func TestToolArgumentsNeverReachTheReplyText(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"我用工作证转 3D。\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"\",\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"create_model_3d_from_image\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"稍等\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"badge\\\",\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"reference_handles\\\":[\\\"@selected\\\"]}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
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
	var streamed strings.Builder
	completion, err := adapter.Stream(context.Background(), agents.Request{Model: "configured-model",
		Messages: []agents.Message{{Role: "user", Content: "hello"}}}, func(delta agents.Delta) error {
		streamed.WriteString(delta.Content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "我用工作证转 3D。稍等" || streamed.String() != completion.Content {
		t.Fatalf("reply text = %q, streamed = %q", completion.Content, streamed.String())
	}
	if len(completion.ToolCalls) != 1 ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"badge","reference_handles":["@selected"]}` {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
	for _, leaked := range []string{"prompt", "reference_handles", "{"} {
		if strings.Contains(completion.Content, leaked) {
			t.Fatalf("reply text carries argument text %q: %q", leaked, completion.Content)
		}
	}
}
