package jimengapiv3

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase is the official base URL the Catalog's jimeng_base_url
// defaults to, plus the api/v3 route version.
const OfficialBase = "https://ark.cn-beijing.volces.com/api/v3"

// Model is the image model the Catalog's Jimeng Offerings send.
const Model = "doubao-seedream-4-5-251128"

func body(prompt, format string, extra map[string]any) map[string]any {
	request := map[string]any{
		"model": Model, "prompt": prompt, "size": "2048x2048", "sequential_image_generation": "disabled",
		"watermark": false, "response_format": format, "optimize_prompt_options": map[string]any{"mode": "standard"},
	}
	for key, value := range extra {
		request[key] = value
	}
	return request
}

// Profile is the alignment script of the Jimeng fake. The smallest size the
// model accepts (2048x2048) and one request per response shape; each paid
// request is sent once.
func Profile() align.Profile {
	reference := align.DataURI(align.ReferencePNG(512))
	return align.Profile{
		Provider: "jimeng", API: "api-v3", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "generations-no-auth", Method: "POST", Path: "/images/generations", NoAuth: true,
				Body: body("a plain red cube on a white background", "b64_json", nil)},
			{Name: "generations-invalid", Method: "POST", Path: "/images/generations", Body: map[string]any{"model": Model}},
			{Name: "generations-b64", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("a plain red cube on a white background", "b64_json", nil)},
			{Name: "generations-url", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("a plain red cube on a white background", "url", nil), Fetch: []string{"data.*"}},
			{Name: "edits-b64", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("make the sphere blue", "b64_json", map[string]any{"image": []string{reference}})},
		},
		Capture: align.CaptureConfig{
			Connection: "jimeng", Scenario: "jimeng-api-v3-alignment", APIVersion: "v3",
			PublicHosts: []string{"ark.cn-beijing.volces.com", "ark-content-generation-v2-cn-beijing.tos-cn-beijing.volces.com"},
			Projection: map[string]string{
				"b64_json": "text", "created": "timestamp", "image": "opaque", "url": "url", "message": "text",
				"generated_images": "number", "output_tokens": "number", "total_tokens": "number",
			},
		},
		RequestNotes: []string{
			"每个付费请求只发一次：2048x2048（模型接受的最小尺寸）；b64_json 与 url 两种响应形态各一次，图生图一次（b64_json）。Plinth 的 Offering 把 response_format 固定为 b64_json，url 形态由 Adapter 支持但目前不被 Schema 使用。",
			"图生图的 `image`（data URI 数组）在样本里是不透明占位 ok（wirefixture 的标识符投影拒绝 data URI），fake 只检查提示词。",
			"官方签名 URL 用 TOS V4 签名（X-Tos-Expires=86400）；样本把它投影成占位。已过期 URL 的响应不是自然过期观测，而是把 X-Tos-Date 改到过去后由官方主机返回的 403 JSON（Code=AccessDenied、Message=Request has expired）。",
			"内容审核拒绝、402、429、5xx 没有在官方 API 上触发，fake 沿用真实 400 信封并换成文档里的 code。",
		},
		Notes: align.Notes{},
	}
}
