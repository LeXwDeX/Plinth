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
	return configuredBody(content, "1:1", "1K")
}

// configuredBody is requestBody with another aspect ratio and resolution: the
// requests of the L5 acceptance round (#540).
func configuredBody(content []any, aspectRatio, imageSize string) map[string]any {
	return map[string]any{
		"model": Model, "messages": []any{map[string]any{"role": "user", "content": content}},
		"modalities": []string{"text", "image"}, "stream": false,
		"eca_image_config": map[string]any{"aspect_ratio": aspectRatio, "image_size": imageSize},
	}
}

// Profile is the alignment script of the Gemini image fake: the cheapest
// resolution (1K), one request per Offering.
//
// The steps after "edit" are the acceptance round of #540 (opt-in: an official run sends them only
// when -steps names them; each is marked paid because the gateway bills it when it accepts it):
// a 16:9 picture at 4K (the Offering default; the answer is large and slow, a
// lost answer may still have been billed), a 3:4 edit with two reference
// pictures one of which has an alpha channel, and an aspect ratio no official
// list names (7:1), whose answer the gateway has never shown: it may refuse it
// with a 400 or ignore it and draw a picture. Their official answers are not
// committed yet, so the fake's matching behavior stays constructed until a real
// run replaces it.
func Profile() align.Profile {
	reference := align.DataURI(align.ReferencePNG(512))
	translucent := align.DataURI(align.TranslucentPNG(512))
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
			{Name: "generate-16x9-4k", Method: "POST", Path: "/chat/completions", Paid: true, OptIn: true,
				Body: configuredBody([]any{text("a plain red cube on a white background")}, "16:9", "4K")},
			{Name: "edit-3x4-two-references", Method: "POST", Path: "/chat/completions", Paid: true, OptIn: true,
				Body: configuredBody([]any{text("recolor the shapes to bright green and keep the layout unchanged"),
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": reference}},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": translucent}}}, "3:4", "1K")},
			{Name: "generate-invalid-ratio", Method: "POST", Path: "/chat/completions", Paid: true, OptIn: true,
				Body: configuredBody([]any{text("a plain red cube on a white background")}, "7:1", "1K")},
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
			"（OptIn：默认的官方运行不发这些步骤，只有 -steps 点名才发，每次发起前按 AGENTS.md 说明次数与预计费用并取得当次同意）验收一轮（#540）新增的三个步骤（generate-16x9-4k、edit-3x4-two-references、generate-invalid-ratio）各发一次，没有官方样本：4K 的响应结构、大小与耗时，两张参考图（其一带 alpha）的图生图，以及网关对 7:1 这种官方列表之外的比例的应答（400 或静默出图）都是真实运行要取的证据，fake 的对应行为在那之前是 constructed。",
			"网关契约没有公开官方文档（供应商文档页需登录并跳转到模型目录）；端点结构唯一依据是已提交的网关真实样本。",
		},
		Notes: align.Notes{},
	}
}
