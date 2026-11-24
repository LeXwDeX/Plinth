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
//
// The steps after "edits-b64" are the acceptance round of #540 (select them
// with -steps; each is marked paid because the service bills it when it
// accepts it): the Catalog's 4096x4096 text-to-image and two-reference edit, a
// sequential request that asks for at most two pictures, and a size below the
// documented minimum area (1024x1024 is 1048576 pixels, the documented minimum
// is 3686400): the expected answer is a free 400 InvalidParameter. Their
// official answers are not committed yet, so the fake's matching behavior stays
// constructed-from-docs until a real run replaces it.
func Profile() align.Profile {
	reference := align.DataURI(align.ReferencePNG(512))
	translucent := align.DataURI(align.TranslucentPNG(512))
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
			{Name: "generations-4096", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("a plain red cube on a white background", "b64_json", map[string]any{"size": "4096x4096"})},
			{Name: "edits-two-references-4096", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("recolor the shapes to bright green and keep the layout unchanged", "b64_json",
					map[string]any{"size": "4096x4096", "image": []string{reference, translucent}})},
			{Name: "generations-sequential-max2", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("two simple icons of a red cube, one from the front and one from above, white background", "b64_json",
					map[string]any{"sequential_image_generation": "auto",
						"sequential_image_generation_options": map[string]any{"max_images": 2}})},
			{Name: "generations-invalid-size", Method: "POST", Path: "/images/generations", Paid: true,
				Body: body("a plain red cube on a white background", "b64_json", map[string]any{"size": "1024x1024"})},
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
			"验收一轮（#540）新增的四个步骤（generations-4096、edits-two-references-4096、generations-sequential-max2、generations-invalid-size）各发一次，没有官方样本：4096x4096 的响应字段与尺寸、两张参考图（其一带 alpha）的顺序与前缀、组图实际张数与 usage.generated_images、低于官方面积下限（3686400 像素）的 400 信封都是真实运行要取的证据，fake 的对应行为在那之前是 constructed-from-docs。",
			"图生图的 `image`（data URI 数组）在样本里是不透明占位 ok（wirefixture 的标识符投影拒绝 data URI），fake 只检查提示词。",
			"官方签名 URL 用 TOS V4 签名（X-Tos-Expires=86400）；样本把它投影成占位。已过期 URL 的响应不是自然过期观测，而是把 X-Tos-Date 改到过去后由官方主机返回的 403 JSON（Code=AccessDenied、Message=Request has expired）。",
			"内容审核拒绝、402、429、5xx 没有在官方 API 上触发，fake 沿用真实 400 信封并换成文档里的 code。",
		},
		Notes: align.Notes{},
	}
}
