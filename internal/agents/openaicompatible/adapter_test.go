package openaicompatible_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

func TestAdapterStreamsTextAndAssemblesToolCallFragments(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() !=
			"https://configured-agent.test/v1/chat/completions" {
			t.Fatalf("url = %q", request.URL)
		}
		if request.Header.Get("Configured-Authorization") !=
			"Configured configured-secret" {
			t.Fatalf("authorization = %q", request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"world\",\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"refine_\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"image\",\"arguments\":\"\\\"paint\\\"}\"}}]}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})
	adapter, err := openaicompatible.New(
		openaicompatible.Config{
			BaseURL:           "https://configured-agent.test/v1/",
			APIKey:            "configured-secret",
			EndpointPath:      "/chat/completions",
			AuthorizationName: "Configured-Authorization",
			AuthorizationType: "Configured",
			RequestTimeout:    time.Minute,
			ScannerMaxBytes:   1 << 20,
			ErrorBodyMaxBytes: 1 << 16,
			Client:            &http.Client{Transport: transport},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var streamed string
	completion, err := adapter.Stream(
		context.Background(),
		agents.Request{
			Model:       "configured-model",
			Temperature: 0,
			Messages: []agents.Message{{
				Role:    "configured-user",
				Content: "hello",
			}},
			Tools: []agents.Tool{{
				Name:        "configured-tool",
				Description: "configured-description",
				Parameters:  []byte(`{"type":"object"}`),
			}},
		},
		func(delta agents.Delta) error {
			streamed += delta.Content
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if streamed != "hello world" || completion.Content != "hello world" {
		t.Fatalf("streamed=%q completion=%#v", streamed, completion)
	}
	if len(completion.ToolCalls) != 1 ||
		completion.ToolCalls[0].ID != "call-a" ||
		completion.ToolCalls[0].Name != "refine_image" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"paint"}` {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func TestAdapterThinkingWireFieldsFollowTypedConfig(t *testing.T) {
	tests := []struct {
		name        string
		thinking    *openaicompatible.ThinkingOptions
		wantEnabled any
		wantEffort  any
	}{
		{
			name:        "qwen profile fields",
			thinking:    &openaicompatible.ThinkingOptions{RequestFields: map[string]json.RawMessage{"enable_thinking": json.RawMessage(`true`), "reasoning_effort": json.RawMessage(`"xhigh"`)}},
			wantEnabled: true,
			wantEffort:  "xhigh",
		},
		{
			// #253: a model without a vendor profile gets standard fields only.
			name:        "no vendor profile",
			thinking:    &openaicompatible.ThinkingOptions{},
			wantEnabled: nil,
			wantEffort:  nil,
		},
		{
			name:        "config omitted",
			thinking:    nil,
			wantEnabled: nil,
			wantEffort:  nil,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			requestBody := make(chan map[string]any, 1)
			transport := roundTripFunc(func(
				request *http.Request,
			) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					return nil, err
				}
				requestBody <- body
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
					},
					Body: io.NopCloser(strings.NewReader(
						`{"choices":[{"message":{"content":"ok"}}]}`,
					)),
				}, nil
			})
			adapter, err := openaicompatible.New(
				openaicompatible.Config{
					BaseURL:           "https://configured-agent.test/v1/",
					APIKey:            "configured-secret",
					EndpointPath:      "/chat/completions",
					AuthorizationName: "Configured-Authorization",
					AuthorizationType: "Configured",
					RequestTimeout:    time.Minute,
					ScannerMaxBytes:   1 << 20,
					ErrorBodyMaxBytes: 1 << 16,
					Client:            &http.Client{Transport: transport},
					Thinking:          testCase.thinking,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Complete(
				context.Background(),
				agents.Request{
					Model: "configured-model",
					Messages: []agents.Message{{
						Role:    "configured-user",
						Content: "hello",
					}},
				},
			); err != nil {
				t.Fatal(err)
			}
			body := <-requestBody
			enabled, hasEnabled := body["enable_thinking"]
			if testCase.wantEnabled == nil {
				if hasEnabled {
					t.Errorf(
						"enable_thinking = %#v, want absent",
						enabled,
					)
				}
			} else if !hasEnabled || enabled != testCase.wantEnabled {
				t.Errorf(
					"enable_thinking = %#v (present=%v), want %#v",
					enabled,
					hasEnabled,
					testCase.wantEnabled,
				)
			}
			effort, hasEffort := body["reasoning_effort"]
			if testCase.wantEffort == nil {
				if hasEffort {
					t.Errorf(
						"reasoning_effort = %#v, want absent",
						effort,
					)
				}
			} else if !hasEffort || effort != testCase.wantEffort {
				t.Errorf(
					"reasoning_effort = %#v (present=%v), want %#v",
					effort,
					hasEffort,
					testCase.wantEffort,
				)
			}
			if _, exists := body["thinking_budget"]; exists {
				t.Errorf(
					"thinking_budget must never be sent: %#v",
					body,
				)
			}
		})
	}
}

func TestAdapterThinkingEnabledKeepsStreamToolCallsAndDropsReasoning(
	t *testing.T,
) {
	requestBody := make(chan map[string]any, 1)
	transport := roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		requestBody <- body
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private reasoning one\"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"world\",\"reasoning_content\":\"private reasoning two\",\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"function\":{\"name\":\"refine_\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"image\",\"arguments\":\"\\\"paint\\\"}\"}}]}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})
	adapter, err := openaicompatible.New(
		openaicompatible.Config{
			BaseURL:           "https://configured-agent.test/v1/",
			APIKey:            "configured-secret",
			EndpointPath:      "/chat/completions",
			AuthorizationName: "Configured-Authorization",
			AuthorizationType: "Configured",
			RequestTimeout:    time.Minute,
			ScannerMaxBytes:   1 << 20,
			ErrorBodyMaxBytes: 1 << 16,
			Client:            &http.Client{Transport: transport},
			Thinking: &openaicompatible.ThinkingOptions{
				RequestFields: map[string]json.RawMessage{"enable_thinking": json.RawMessage(`true`), "reasoning_effort": json.RawMessage(`"xhigh"`)},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	completion, err := adapter.Stream(
		context.Background(),
		agents.Request{
			Model: "configured-model",
			Messages: []agents.Message{{
				Role:    "configured-user",
				Content: "hello",
			}},
		},
		func(delta agents.Delta) error {
			streamed.WriteString(delta.Content)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "hello world" ||
		completion.Content != "hello world" {
		t.Fatalf(
			"streamed=%q completion=%#v",
			streamed.String(),
			completion,
		)
	}
	if strings.Contains(streamed.String(), "private reasoning") ||
		strings.Contains(completion.Content, "private reasoning") {
		t.Fatalf("reasoning content leaked into completion")
	}
	if len(completion.ToolCalls) != 1 ||
		completion.ToolCalls[0].ID != "call-a" ||
		completion.ToolCalls[0].Name != "refine_image" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"paint"}` {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
	body := <-requestBody
	if body["enable_thinking"] != true ||
		body["reasoning_effort"] != "xhigh" {
		t.Fatalf("wire thinking fields = %#v", body)
	}
}

// DeepSeek thinking mode (https://api-docs.deepseek.com/guides/thinking_mode,
// checked 2026-10-19): the switch is {"thinking":{"type":"enabled"}}, the
// highest effort is reasoning_effort "max", and with tools every follow-up
// request must send reasoning_content back or the API returns 400. The 400 was
// also observed live on the test gateway on 2026-10-19. Coverage added after
// the implementation; it is not test-first.
func TestAdapterVendorThinkingProfileSendsFieldsAndPassesReasoningBack(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		bodies <- body
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"plan \"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"two\",\"tool_calls\":[{\"index\":0,\"id\":\"call-b\",\"function\":{\"name\":\"refine_image\",\"arguments\":\"{}\"}}]}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
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
			RequestFields: map[string]json.RawMessage{
				"thinking":         json.RawMessage(`{"type":"enabled"}`),
				"reasoning_effort": json.RawMessage(`"max"`),
			},
			ReasoningPassback: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	completion, err := adapter.Stream(context.Background(), agents.Request{
		Model: "deepseek",
		Messages: []agents.Message{
			{Role: "user", Content: "draw"},
			{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "call-a", Name: "list_offerings", Arguments: []byte(`{}`)}}, ReasoningContent: "plan one"},
			{Role: "tool", ToolCallID: "call-a", Content: "[]"},
		},
	}, func(delta agents.Delta) error {
		streamed.WriteString(delta.Content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ReasoningContent != "plan two" || completion.Content != "" || streamed.Len() != 0 {
		t.Fatalf("completion=%#v streamed=%q", completion, streamed.String())
	}
	body := <-bodies
	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || body["reasoning_effort"] != "max" {
		t.Fatalf("vendor thinking fields = %#v", body)
	}
	if _, exists := body["enable_thinking"]; exists {
		t.Fatalf("generic enable_thinking sent with a vendor profile: %#v", body)
	}
	messages, _ := body["messages"].([]any)
	assistant, _ := messages[1].(map[string]any)
	if assistant["reasoning_content"] != "plan one" {
		t.Fatalf("assistant reasoning not passed back: %#v", assistant)
	}
	if user, _ := messages[0].(map[string]any); user["reasoning_content"] != nil {
		t.Fatalf("empty reasoning must be omitted: %#v", user)
	}
}

func TestAdapterWithoutPassbackNeitherKeepsNorSendsReasoning(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		bodies <- body
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok","reasoning_content":"private"}}]}`)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           "https://configured-agent.test/v1/",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		Client:            &http.Client{Transport: transport},
		Thinking:          &openaicompatible.ThinkingOptions{RequestFields: map[string]json.RawMessage{"enable_thinking": json.RawMessage(`true`), "reasoning_effort": json.RawMessage(`"xhigh"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := adapter.Complete(context.Background(), agents.Request{
		Model:    "qwen-max",
		Messages: []agents.Message{{Role: "assistant", Content: "earlier", ReasoningContent: "stale"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ReasoningContent != "" {
		t.Fatalf("reasoning kept without passback: %#v", completion)
	}
	body := <-bodies
	messages, _ := body["messages"].([]any)
	if assistant, _ := messages[0].(map[string]any); assistant["reasoning_content"] != nil {
		t.Fatalf("reasoning sent without passback: %#v", assistant)
	}
}

// #253: a refusal of a request that carried pictures is reported as such, so
// the person learns the model may not read images instead of an unknown error.
func TestAdapterMarksARefusedRequestWithImages(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"image input is not supported"}}`)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://configured-agent.test/v1/", APIKey: "k", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16, Client: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	withImage := agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: "what is this"}, {Kind: "image_url", URL: "data:image/png;base64,QQ=="},
	}}}}
	if _, err := adapter.Complete(context.Background(), withImage); !errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("with image: %v", err)
	}
	textOnly := agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Content: "hello"}}}
	if _, err := adapter.Complete(context.Background(), textOnly); err == nil || errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("text only: %v", err)
	}
}

// #253: only a 400/415/422 whose error names image, vision or multimodal
// input means "this model cannot read images"; any other refusal of a request
// with pictures (a too-long context, a bad parameter, a 404 model) stays a
// generic failure so the person is not told to switch models wrongly.
func TestAdapterMarksImagesRejectedOnlyForImageRefusals(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		rejected bool
	}{
		{"400 image input", http.StatusBadRequest, `{"error":{"message":"Image input is not supported"}}`, true},
		{"415 vision", http.StatusUnsupportedMediaType, `{"error":{"message":"model has no VISION capability"}}`, true},
		{"422 multimodal", http.StatusUnprocessableEntity, `{"error":{"message":"Multimodal content not allowed"}}`, true},
		{"400 image_url part", http.StatusBadRequest, `{"error":{"message":"unknown content type image_url"}}`, true},
		{"400 context length", http.StatusBadRequest, `{"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 8192 tokens"}}`, false},
		{"400 bad parameter", http.StatusBadRequest, `{"error":{"message":"temperature must be <= 2"}}`, false},
		{"404 mentions image", http.StatusNotFound, `{"error":{"message":"model not found for image input"}}`, false},
		{"409 mentions vision", http.StatusConflict, `{"error":{"message":"vision busy"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			})
			adapter, err := openaicompatible.New(openaicompatible.Config{
				BaseURL: "https://configured-agent.test/v1/", APIKey: "k", EndpointPath: "/chat/completions",
				AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Minute,
				ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16, Client: &http.Client{Transport: transport},
			})
			if err != nil {
				t.Fatal(err)
			}
			withImage := agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
				{Kind: "text", Text: "what is this"}, {Kind: "image_url", URL: "data:image/png;base64,QQ=="},
			}}}}
			_, err = adapter.Complete(context.Background(), withImage)
			if err == nil || errors.Is(err, agents.ErrImagesRejected) != test.rejected {
				t.Fatalf("err = %v, want images rejected = %v", err, test.rejected)
			}
		})
	}
}
