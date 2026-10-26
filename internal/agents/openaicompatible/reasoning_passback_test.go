package openaicompatible_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// #283: DeepSeek thinking mode rejects a follow-up request with 400 ("The
// `reasoning_content` in the thinking mode must be passed back to the API")
// when any assistant tool-call message of the loop lacks reasoning_content.
// A round may legitimately come back with no reasoning at all (observed live
// on 2026-10-20 for a quick repeated call); the field must then still be sent,
// as an empty string, instead of being omitted.
func TestAdapterPassesBackEmptyReasoningOnEveryToolCallMessage(t *testing.T) {
	// Stream replies per request: the first round reasons, the second does
	// not, the third answers in text.
	replies := []string{
		`{"choices":[{"delta":{"reasoning_content":"plan","tool_calls":[{"index":0,"id":"c0","function":{"name":"list_session_models","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"list_session_models","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"done"}}]}`,
	}
	requests := 0
	var bodies []map[string]any
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		bodies = append(bodies, body)
		messages, _ := body["messages"].([]any)
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			if calls, _ := message["tool_calls"].([]any); message["role"] == "assistant" && len(calls) > 0 {
				if _, present := message["reasoning_content"]; !present {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(
							`{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`,
						)),
					}, nil
				}
			}
		}
		reply := replies[requests]
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: " + reply + "\n\ndata: [DONE]\n\n")),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           "https://configured-agent.test/v1/",
		APIKey:            "configured-secret",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		Client:            &http.Client{Transport: transport},
		Thinking: &openaicompatible.ThinkingOptions{
			RequestFields:     map[string]json.RawMessage{"thinking": json.RawMessage(`{"type":"enabled"}`)},
			ReasoningPassback: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agents.Request{
		Model:    "deepseek",
		Messages: []agents.Message{{Role: "user", Content: "call it three times"}},
		Tools:    []agents.Tool{{Name: "list_session_models", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	// The same accumulation as the orchestrator's follow-up requests.
	for round := 0; ; round++ {
		completion, err := adapter.Stream(context.Background(), request, nil)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if len(completion.ToolCalls) == 0 {
			if completion.Content != "done" || round != 2 {
				t.Fatalf("round %d: completion = %#v", round, completion)
			}
			break
		}
		request.Messages = append(request.Messages, agents.Message{
			Role:             "assistant",
			ToolCalls:        completion.ToolCalls,
			ReasoningContent: completion.ReasoningContent,
		})
		for _, call := range completion.ToolCalls {
			request.Messages = append(request.Messages, agents.Message{
				Role: "tool", ToolCallID: call.ID, Content: fmt.Sprintf(`{"round":%d}`, round),
			})
		}
	}
	last, _ := bodies[len(bodies)-1]["messages"].([]any)
	first, _ := last[1].(map[string]any)
	second, _ := last[3].(map[string]any)
	if first["reasoning_content"] != "plan" || second["reasoning_content"] != "" {
		t.Fatalf("passed-back reasoning = %#v / %#v", first["reasoning_content"], second["reasoning_content"])
	}
	for _, index := range []int{0, 2, 4} {
		if message, _ := last[index].(map[string]any); message["reasoning_content"] != nil {
			t.Fatalf("non-assistant message carries reasoning_content: %#v", message)
		}
	}
}
