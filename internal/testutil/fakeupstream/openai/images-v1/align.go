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
const Model = "gpt-image-2.6-sunburst"

const prompt = "a plain red cube on a white background"

// generationBody is the cheapest request the Offering can send: low quality,
// one 1024x1024 picture.
func generationBody() map[string]any {
	return map[string]any{
		"model": Model, "prompt": prompt, "size": "1024x1024", "quality": "low", "n": 1,
		"output_format": "png", "background": "auto", "moderation": "auto",
	}
}

// sizedBody is generationBody with another size, quality and count: the
// requests of the L5 acceptance round (#540).
func sizedBody(size, quality string, count int) map[string]any {
	body := generationBody()
	body["size"], body["quality"], body["n"] = size, quality, count
	return body
}

// Profile is the alignment script of the OpenAI-compatible image fake. Each
// paid request is sent once.
//
// The steps after "edits" are the acceptance round of #540 (opt-in: an official run sends them only
// when -steps names them; each is marked paid because the gateway bills it when it accepts it):
// two pictures at 1536x1152, the quality=max ruling of the Catalog default
// (B2), a size the documented rules forbid (1000x1000 is not a multiple of 16:
// the expected answer is a free 400), and an edit with two reference pictures
// and a mask. Their official answers are not committed yet, so the fake's
// matching behavior stays constructed-from-docs until a real run replaces it.
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
			{Name: "generations-n2-1536x1152", Method: "POST", Path: "/images/generations", Paid: true, OptIn: true,
				Body: sizedBody("1536x1152", "low", 2)},
			{Name: "generations-quality-max", Method: "POST", Path: "/images/generations", Paid: true, OptIn: true,
				Body: sizedBody("1024x1024", "max", 1)},
			{Name: "generations-invalid-size", Method: "POST", Path: "/images/generations", Paid: true, OptIn: true,
				Body: sizedBody("1000x1000", "low", 1)},
			{Name: "edits-two-images-mask", Method: "POST", Path: "/images/edits", Paid: true, OptIn: true,
				Form: []align.Field{
					{Name: "model", Value: Model}, {Name: "prompt", Value: "fill the transparent square with a small yellow star"},
					{Name: "size", Value: "1024x1024"}, {Name: "quality", Value: "low"}, {Name: "n", Value: "1"},
					{Name: "output_format", Value: "png"}, {Name: "background", Value: "auto"}, {Name: "moderation", Value: "auto"},
				},
				Files: []align.File{
					{Field: "image[]", Filename: "reference-1.png", MediaType: "image/png", Data: reference},
					{Field: "image[]", Filename: "reference-2.png", MediaType: "image/png", Data: align.TranslucentPNG(512)},
					{Field: "mask", Filename: "mask.png", MediaType: "image/png", Data: align.MaskPNG(512)},
				}},
		},
		Capture: align.CaptureConfig{
			Connection: "openai", Scenario: "openai-images-v1-alignment", APIVersion: "v1", AliasBaseHost: true,
			ExtraKeys:  []string{"image_tokens", "text_tokens"},
			Projection: map[string]string{"b64_json": "text", "created": "timestamp", "input_tokens": "number", "output_tokens": "number", "total_tokens": "number", "image_tokens": "number", "text_tokens": "number", "requestid": "id"},
		},
		RequestNotes: []string{
			"网关为 gpt-image-2 的 OpenAI 兼容中转（Plinth 的 OPENAI_BASE_URL）；为压低费用每个付费端点只发一次，取 1024x1024、quality=low、n=1：Plinth 默认 2048x2048、quality=max，响应结构相同，只有 usage 的数值不同。",
			"edits 的参考图在付费运行里用的是 `image[]` 分片名（Plinth 的真实写法，网关返回 200）；wirefixture 的键规则不接受方括号，样本把分片名写成 `image`，fake 两种写法都接受。",
			"（OptIn：默认的官方运行不发这些步骤，只有 -steps 点名才发，每次发起前按 AGENTS.md 说明次数与预计费用并取得当次同意）验收一轮（#540）新增的四个步骤（generations-n2-1536x1152、generations-quality-max、generations-invalid-size、edits-two-images-mask）各发一次：n=2 的真实尺寸与 usage、quality=max 是否被网关受理（Catalog 默认值裁决，约 0.11 美元）、1000x1000 的 400 信封（官方规则：边长须为 16 的倍数；预期免费）、两张参考图加同尺寸 mask 的 edits。两张参考图与 mask 用 `image[]` 与 `mask` 分片名（Plinth 的真实写法）；wirefixture 的样本键规则不接受方括号，导出样本前须按「edits」步骤的做法处理分片名。这些步骤在真实运行前没有官方样本，fake 的对应行为是 constructed-from-docs。",
			"内容审核拒绝（400 moderation_blocked）、402、429、5xx 没有在官方网关上触发，fake 沿用真实 400 信封并换成文档里的 code 与 message。",
		},
		Notes: align.Notes{},
	}
}
