package openaiimagesv1

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase names the real upstream the fake is aligned with: the
// OpenAI-compatible /v1 gateway the dev-test stack's OPENAI_BASE_URL points at
// (its address lives only in the stack's runtime environment, never in the
// repository). Plinth appends images/generations or images/edits to it.
const OfficialBase = "dev-test OpenAI-compatible image gateway /v1"

// Model is the image model the Catalog's gpt-image-2 Offerings send.
const Model = "gpt-image-2.5-sunburst"

const prompt = "a plain red cube on a white background"

// generationBody is the cheapest request the Offering can send: low quality,
// one 1024x1024 picture.
func generationBody() map[string]any {
	return map[string]any{
		"model": Model, "prompt": prompt, "size": "1024x1024", "quality": "low", "n": 1,
		"output_format": "png", "background": "auto", "moderation": "auto",
	}
}

// Profile is the alignment script of the OpenAI-compatible image fake. Each
// paid request is sent once.
func Profile() align.Profile {
	reference := align.ReferencePNG(512)
	return align.Profile{
		Provider: "openai", API: "images-v1", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "generations-no-auth", Method: "POST", Path: "/images/generations", NoAuth: true, Body: generationBody()},
			{Name: "generations-invalid", Method: "POST", Path: "/images/generations", Body: map[string]any{"model": Model}},
			{Name: "generations", Method: "POST", Path: "/images/generations", Paid: true, Body: generationBody()},
			{Name: "edits-invalid", Method: "POST", Path: "/images/edits", Form: []align.Field{{Name: "model", Value: Model}}},
			{Name: "edits", Method: "POST", Path: "/images/edits", Paid: true,
				Form: []align.Field{
					{Name: "model", Value: Model}, {Name: "prompt", Value: "make the cube blue"},
					{Name: "size", Value: "1024x1024"}, {Name: "quality", Value: "low"}, {Name: "n", Value: "1"},
					{Name: "output_format", Value: "png"}, {Name: "background", Value: "auto"}, {Name: "moderation", Value: "auto"},
				},
				Files: []align.File{{Field: "image", Filename: "reference.png", MediaType: "image/png", Data: reference}}},
		},
		Capture: align.CaptureConfig{
			Connection: "openai", Scenario: "openai-images-v1-alignment", APIVersion: "v1", AliasBaseHost: true,
			ExtraKeys:  []string{"image_tokens", "text_tokens"},
			Projection: map[string]string{"b64_json": "text", "created": "timestamp", "input_tokens": "number", "output_tokens": "number", "total_tokens": "number", "image_tokens": "number", "text_tokens": "number", "requestid": "id"},
		},
		RequestNotes: []string{
			"网关为 gpt-image-2 的 OpenAI 兼容中转（Plinth 的 OPENAI_BASE_URL）；为压低费用每个付费端点只发一次，取 1024x1024、quality=low、n=1：Plinth 默认 2048x2048、quality=max，响应结构相同，只有 usage 的数值不同。",
			"edits 的参考图在付费运行里用的是 `image[]` 分片名（Plinth 的真实写法，网关返回 200）；wirefixture 的键规则不接受方括号，样本把分片名写成 `image`，fake 两种写法都接受。",
			"内容审核拒绝（400 moderation_blocked）、402、429、5xx 没有在官方网关上触发，fake 沿用真实 400 信封并换成文档里的 code 与 message。",
		},
		Notes: align.Notes{},
	}
}
