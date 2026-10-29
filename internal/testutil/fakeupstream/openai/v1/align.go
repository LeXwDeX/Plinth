package openaiv1

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase names the real upstream the fake is aligned with: the
// OpenAI-compatible /v1 gateway of the dev-test stack (its address lives only
// in the stack's runtime environment, never in the repository).
const OfficialBase = "dev-test OpenAI-compatible LLM gateway /v1"

// Model is the chat model the dev-test stack uses (docs: dev_ops.md,
// "测试栈大模型接线").
const Model = "deepseek"

func lookupTool() []map[string]any {
	return []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name": "lookup_note", "description": "Look up a note by topic.",
			"parameters": map[string]any{
				"type": "object", "properties": map[string]any{"topic": map[string]any{"type": "string"}},
				"required": []string{"topic"},
			},
		},
	}}
}

func chatBody(content string, stream bool, withTools bool) map[string]any {
	body := map[string]any{
		"model":            Model,
		"messages":         []map[string]any{{"role": "user", "content": content}},
		"stream":           stream,
		"max_tokens":       512,
		"thinking":         map[string]any{"type": "enabled"},
		"reasoning_effort": "low",
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if withTools {
		body["tools"] = lookupTool()
		body["tool_choice"] = "auto"
	}
	return body
}

// Profile is the alignment script of the OpenAI-compatible fake. Each request
// is sent once; chat calls are small (a single short reply).
func Profile() align.Profile {
	return align.Profile{
		Provider: "openai", API: "v1", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "models", Method: "GET", Path: "/models"},
			{Name: "models-no-auth", Method: "GET", Path: "/models", NoAuth: true},
			{Name: "chat-no-auth", Method: "POST", Path: "/chat/completions", NoAuth: true,
				Body: chatBody("Reply with the single word ok.", false, false)},
			{Name: "chat-invalid", Method: "POST", Path: "/chat/completions", Body: map[string]any{}},
			{Name: "chat-completion", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: chatBody("Reply with the single word ok.", false, false)},
			{Name: "chat-stream", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: chatBody("Reply with the single word ok.", true, false)},
			{Name: "chat-tool-call", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: chatBody("Call the lookup_note tool with the topic cube. Do not answer in text.", false, true)},
			{Name: "chat-tool-call-stream", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: chatBody("Call the lookup_note tool with the topic cube. Do not answer in text.", true, true)},
		},
		Capture: align.CaptureConfig{
			Connection: "LLM", Scenario: "openai-v1-alignment", APIVersion: "v1", AliasBaseHost: true,
			Projection: map[string]string{"owned_by": "text", "created": "timestamp"},
		},
		FakeBodyHook: markToolCall,
		RequestNotes: []string{
			"工具调用步骤（chat-tool-call、chat-tool-call-stream）：发给 fake 的请求在最后一条 user 消息末尾多了 fake 专用的工具标记 `[[fake:tool:lookup_note {\"topic\":\"cube\"}]]`，其余字节相同；官方模型自行决定调用工具，fake 只在标记出现时返回工具调用。",
			"fake 的流式分片按官方 2026-10-29 实测的分片模式生成（角色、推理、正文或工具调用头与参数、结束分片）；已提交样本经 wirefixture 规范化后合并了中间分片，字段集合一致、中间分片的 null 类型不保留，所以漂移测试对 SSE 只比字段集合。",
		},
		Notes: align.Notes{},
	}
}

// markToolCall appends the fake's tool marker to the last user message of the
// tool-call steps.
func markToolCall(step string, body any) any {
	if step != "chat-tool-call" && step != "chat-tool-call-stream" {
		return body
	}
	request, ok := body.(map[string]any)
	if !ok {
		return body
	}
	messages, _ := request["messages"].([]map[string]any)
	out := make([]map[string]any, len(messages))
	for i, message := range messages {
		copied := map[string]any{}
		for key, value := range message {
			copied[key] = value
		}
		out[i] = copied
	}
	if len(out) > 0 {
		text, _ := out[len(out)-1]["content"].(string)
		out[len(out)-1]["content"] = text + " " + "[[fake:tool:lookup_note {\"topic\":\"cube\"}]]"
	}
	clone := map[string]any{}
	for key, value := range request {
		clone[key] = value
	}
	clone["messages"] = out
	return clone
}
