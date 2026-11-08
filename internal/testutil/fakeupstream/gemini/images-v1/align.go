package geminiimagesv1

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase names the real upstream the fake is aligned with: the
// OpenAI-compatible /v1 gateway the dev-test stack's GEMINI_BASE_URL points at
// (its address lives only in the stack's runtime environment, never in the
// repository). Plinth appends chat/completions to it.
const OfficialBase = "dev-test OpenAI-compatible Gemini image gateway /v1"

// Model is the image model the Catalog's Gemini Offerings send.
const Model = "gemini-3-pro-image"

func requestBody(content []any) map[string]any {
	return map[string]any{
		"model": Model, "messages": []any{map[string]any{"role": "user", "content": content}},
		"modalities": []string{"text", "image"}, "stream": false,
		"eca_image_config": map[string]any{"aspect_ratio": "1:1", "image_size": "1K"},
	}
}

// Profile is the alignment script of the Gemini image fake: the cheapest
// resolution (1K), one request per Offering.
func Profile() align.Profile {
	reference := align.DataURI(align.ReferencePNG(512))
	text := func(value string) map[string]any { return map[string]any{"type": "text", "text": value} }
	return align.Profile{
		Provider: "gemini", API: "images-v1", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "generate-no-auth", Method: "POST", Path: "/chat/completions", NoAuth: true,
				Body: requestBody([]any{text("a plain red cube on a white background")})},
			{Name: "generate-invalid", Method: "POST", Path: "/chat/completions", Body: map[string]any{"model": Model}},
			{Name: "generate", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: requestBody([]any{text("a plain red cube on a white background")})},
			{Name: "edit", Method: "POST", Path: "/chat/completions", Paid: true,
				Body: requestBody([]any{text("make the sphere blue"),
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": reference}}})},
		},
		Capture: align.CaptureConfig{
			Connection: "gemini", Scenario: "gemini-images-v1-alignment", APIVersion: "v1", AliasBaseHost: true,
			ExtraKeys: []string{"reasoning_tokens", "image_tokens", "cached_tokens", "cache_write_tokens", "text_tokens"},
			Projection: map[string]string{
				"url": "text", "created": "timestamp", "id": "id", "requestid": "id", "reasoning_content": "text",
				"prompt_tokens": "number", "completion_tokens": "number", "total_tokens": "number",
				"reasoning_tokens": "number", "image_tokens": "number", "cached_tokens": "number",
				"cache_write_tokens": "number", "text_tokens": "number",
			},
		},
		RequestNotes: []string{
			"网关是 Gemini 图片模型的 OpenAI 兼容中转（Plinth 的 GEMINI_BASE_URL）；每个付费请求只发一次，取 1K 分辨率与 1:1：Plinth 默认 4K，响应结构相同。",
			"文生图的真实响应是 JPEG data URI，图生图是 PNG data URI（网关按模型输出而定）；fake 一律返回 PNG data URI，Plinth 按字节判断类型。",
			"图生图响应多出顶层 service_tier 与 usage.prompt_tokens_details.image_tokens，fake 对带参考图的请求使用图生图样本的结构。",
			"内容审核拒绝、内容过滤、402/403/404/429/5xx 没有在官方网关上触发；fake 按 Gemini 公开 API 错误页与网关信封构造（constructed-from-docs，核对日期见端点表），内容过滤沿用成功信封并把 finish_reason 设为 content_filter。",
			"网关契约没有公开官方文档（供应商文档页需登录并跳转到模型目录）；端点结构唯一依据是已提交的网关真实样本。",
		},
		Notes: align.Notes{},
	}
}
