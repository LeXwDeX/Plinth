package openaicompatible_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// 实际链路依据（核对日期 2026-10-18）：
//   - 测试栈 LLM_BASE_URL 指向 llms 网关的 /v1，对话与图片请求都经
//     该 LLMS 中转。2026-10-18 从 dev-test 只读探测 /v1/chat/completions
//     未带凭据返回 401：这证明存在鉴权墙；路由是否存在由网关源码
//     （internal/proxy/forward.go）确认。
//   - 中转对外暴露 OpenAI 兼容 Chat Completions：POST /v1/chat/completions，
//     流式 chunk 为 object=chat.completion.chunk，choices[].delta 递增，
//     finish_reason 只在最后一个 choice chunk 出现，usage 出现在末尾
//     chunk（此时 choices 可为空）。末尾 usage chunk 是网关的兼容扩展，
//     测试把它当作容错输入，不要求所有上游都发送。
//   - enable_thinking 与 reasoning_effort 不是 OpenAI 官方字段，而是本项目的
//     Schema 约束：default_catalog.json 的 thinking 配置由
//     internal/app/provider_connections.go 传入适配器。合同测试按生产配置
//     （enabled=true、reasoning_effort=xhigh）断言实际 wire 外形。
//
// 依据分类：网关对外的 OpenAI 兼容外形、真实运行观察与项目 Schema 约束；
// 本适配器不依赖任何厂商私有字段。测试使用固定合成值，不调用真实上游。

func newOfficialChatAdapter(t *testing.T, endpoint string) *openaicompatible.Adapter {
	t.Helper()
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           endpoint,
		APIKey:            "official-key",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    5 * time.Second,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		// 生产配置来自 Catalog 的 thinking 字段（enabled=true,
		// reasoning_effort=xhigh），这里保持一致以覆盖真实 wire 外形。
		Thinking: &openaicompatible.ThinkingOptions{RequestFields: map[string]json.RawMessage{"enable_thinking": json.RawMessage(`true`), "reasoning_effort": json.RawMessage(`"xhigh"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func officialMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	mapped, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", label, value)
	}
	return mapped
}

func officialList(t *testing.T, value any, label string) []any {
	t.Helper()
	list, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want array", label, value)
	}
	return list
}

func TestChatCompletionsRequestAndResponseMatchOfficialContract(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer official-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "decode request", http.StatusBadRequest)
			return
		}
		bodies <- body
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-synthetic",
			"object":"chat.completion",
			"created":1760000000,
			"model":"qwen-max",
			"choices":[{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"好的",
					"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"refine_image","arguments":"{\"prompt\":\"fox\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`)
	}))
	defer upstream.Close()

	completion, err := newOfficialChatAdapter(t, upstream.URL+"/v1").Complete(
		context.Background(),
		agents.Request{
			Model:       "qwen-max",
			Temperature: 0,
			Messages: []agents.Message{
				{Role: "system", Content: "你是助手"},
				{Role: "user", Parts: []agents.ContentPart{
					{Kind: "text", Text: "把这张图改成红色"},
					{Kind: "image_url", URL: "data:image/png;base64,c3ludGhldGlj", Detail: "low"},
				}},
				{Role: "assistant", ToolCalls: []agents.ToolCall{{
					ID: "call_0", Name: "refine_image", Arguments: json.RawMessage(`{"prompt":"fox"}`),
				}}},
				{Role: "tool", Content: `{"ok":true}`, ToolCallID: "call_0"},
			},
			Tools: []agents.Tool{{
				Name:        "refine_image",
				Description: "编辑图片",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "好的" || len(completion.ToolCalls) != 1 ||
		completion.ToolCalls[0].ID != "call_1" ||
		completion.ToolCalls[0].Name != "refine_image" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"fox"}` {
		t.Fatalf("completion = %#v", completion)
	}

	body := <-bodies
	if body["model"] != "qwen-max" || body["stream"] != false || body["temperature"] != float64(0) {
		t.Errorf("top-level request fields = %#v", body)
	}
	if body["enable_thinking"] != true || body["reasoning_effort"] != "xhigh" {
		t.Errorf("Schema-declared thinking fields = %#v", body)
	}
	messages := officialList(t, body["messages"], "messages")
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", body["messages"])
	}
	system := officialMap(t, messages[0], "system message")
	if system["role"] != "system" || system["content"] != "你是助手" {
		t.Errorf("system message = %#v", system)
	}
	user := officialMap(t, messages[1], "user message")
	parts := officialList(t, user["content"], "user content")
	if user["role"] != "user" || len(parts) != 2 {
		t.Fatalf("user message = %#v", user)
	}
	textPart := officialMap(t, parts[0], "text part")
	if textPart["type"] != "text" || textPart["text"] != "把这张图改成红色" {
		t.Errorf("text part = %#v", textPart)
	}
	imagePart := officialMap(t, parts[1], "image part")
	imageURL := officialMap(t, imagePart["image_url"], "image_url")
	if imagePart["type"] != "image_url" || imageURL["url"] != "data:image/png;base64,c3ludGhldGlj" || imageURL["detail"] != "low" {
		t.Errorf("image part = %#v", imagePart)
	}
	assistant := officialMap(t, messages[2], "assistant message")
	assistantCalls := officialList(t, assistant["tool_calls"], "assistant tool_calls")
	if len(assistantCalls) != 1 {
		t.Fatalf("assistant tool_calls = %#v", assistant["tool_calls"])
	}
	call := officialMap(t, assistantCalls[0], "assistant tool call")
	function := officialMap(t, call["function"], "assistant function")
	if assistant["role"] != "assistant" || call["id"] != "call_0" || call["type"] != "function" ||
		function["name"] != "refine_image" || function["arguments"] != `{"prompt":"fox"}` {
		t.Errorf("assistant tool call = %#v", assistant)
	}
	tool := officialMap(t, messages[3], "tool message")
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_0" || tool["content"] != `{"ok":true}` {
		t.Errorf("tool message = %#v", tool)
	}
	declaredTools := officialList(t, body["tools"], "tools")
	if len(declaredTools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	declared := officialMap(t, declaredTools[0], "declared tool")
	declaredFunction := officialMap(t, declared["function"], "declared function")
	if declared["type"] != "function" || declaredFunction["name"] != "refine_image" ||
		declaredFunction["description"] != "编辑图片" ||
		officialMap(t, declaredFunction["parameters"], "declared parameters")["type"] != "object" {
		t.Errorf("declared tool = %#v", declared)
	}
}

func TestChatCompletionsStreamChunksMatchOfficialContract(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer official-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "decode request", http.StatusBadRequest)
			return
		}
		bodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			`data: {"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1760000000,"model":"qwen-max","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}`+"\n\n"+
				`data: {"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1760000000,"model":"qwen-max","choices":[{"index":0,"delta":{"content":"你好"},"logprobs":null,"finish_reason":null}]}`+"\n\n"+
				`data: {"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1760000000,"model":"qwen-max","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"refine_image","arguments":"{\"prompt\":\"fox\"}"}}]},"logprobs":null,"finish_reason":null}]}`+"\n\n"+
				`data: {"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1760000000,"model":"qwen-max","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}]}`+"\n\n"+
				`data: {"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1760000000,"model":"qwen-max","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\n"+
				"data: [DONE]\n\n")
	}))
	defer upstream.Close()
	var streamed strings.Builder
	completion, err := newOfficialChatAdapter(t, upstream.URL+"/v1").Stream(
		context.Background(),
		agents.Request{Model: "qwen-max", Messages: []agents.Message{{Role: "user", Content: "你好"}}},
		func(delta agents.Delta) error {
			streamed.WriteString(delta.Content)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "你好" || completion.Content != "你好" {
		t.Fatalf("streamed=%q completion=%#v", streamed.String(), completion)
	}
	if len(completion.ToolCalls) != 1 ||
		completion.ToolCalls[0].ID != "call_1" ||
		completion.ToolCalls[0].Name != "refine_image" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"fox"}` {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
	body := <-bodies
	if body["model"] != "qwen-max" || body["stream"] != true {
		t.Errorf("stream request fields = %#v", body)
	}
	if body["enable_thinking"] != true || body["reasoning_effort"] != "xhigh" {
		t.Errorf("Schema-declared thinking fields = %#v", body)
	}
}

func TestChatCompletionsMalformedResponsesFail(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		stream      bool
		wantMessage string
	}{
		{"completion missing choices", http.StatusOK, `{"id":"chatcmpl-synthetic","object":"chat.completion"}`, false, "no choices"},
		{"completion empty choices", http.StatusOK, `{"choices":[]}`, false, "no choices"},
		{"completion wrong content type", http.StatusOK, `{"choices":[{"message":{"content":123}}]}`, false, "decode"},
		{"stream chunk not json", http.StatusOK, "data: {not-json}\n\n", true, "decode"},
		{"upstream rejects request", http.StatusTooManyRequests, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`, false, "429"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			adapter := newOfficialChatAdapter(t, upstream.URL+"/v1")
			request := agents.Request{Model: "qwen-max", Messages: []agents.Message{{Role: "user", Content: "hi"}}}
			var err error
			if tc.stream {
				_, err = adapter.Stream(context.Background(), request, nil)
			} else {
				_, err = adapter.Complete(context.Background(), request)
			}
			// 这里断言适配器当前的内部错误文本，属于实现细节耦合：仅用于
			// 区分失败分支，修改错误文案时需同步更新。
			if err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantMessage)
			}
		})
	}
}
