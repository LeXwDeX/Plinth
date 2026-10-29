package tripov3

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase is the official base URL of the API version the Catalog's
// tripo_base_url points at.
const OfficialBase = "https://openapi.tripo3d.ai/v3"

// taskTerminal are the statuses that end a Tripo task.
var taskTerminal = []string{"success", "failed", "cancelled", "banned", "expired"}

func taskPoll() *align.Poll {
	return &align.Poll{
		StatusPath: "data.status", ProgressPath: "data.progress",
		Terminal: taskTerminal, Max: 240, Interval: 4 * time.Second,
	}
}

// uploadPNG is a 64x64 gradient the upload endpoint accepts as a picture.
func uploadPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// figurePNG is a plain figure drawn from distinct colored parts (head, body, two
// arms, two legs) on white: a picture a segmentation has parts to find in.
func figurePNG() []byte {
	const size = 512
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, c)
			}
		}
	}
	fill(196, 170, 316, 330, color.RGBA{R: 200, G: 40, B: 40, A: 255}) // body
	fill(80, 180, 196, 220, color.RGBA{R: 40, G: 90, B: 200, A: 255})  // left arm
	fill(316, 180, 432, 220, color.RGBA{R: 40, G: 90, B: 200, A: 255}) // right arm
	fill(206, 330, 250, 460, color.RGBA{R: 40, G: 150, B: 60, A: 255}) // left leg
	fill(262, 330, 306, 460, color.RGBA{R: 40, G: 150, B: 60, A: 255}) // right leg
	for y := 60; y < 170; y++ {                                        // head
		for x := 201; x < 311; x++ {
			if dx, dy := x-256, y-115; dx*dx+dy*dy < 55*55 {
				img.Set(x, y, color.RGBA{R: 240, G: 190, B: 40, A: 255})
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// Profile is the alignment script of the Tripo v3 fake. Every paid request is
// the cheapest option of its endpoint and is sent once. The chain mirrors what
// Plinth drives: generation, rig check, rigging, animation, segmentation,
// a model export and part completion.
func Profile() align.Profile {
	return align.Profile{
		Provider: "tripo", API: "v3", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "balance", Method: "GET", Path: "/account/balance"},
			{Name: "balance-no-auth", Method: "GET", Path: "/account/balance", NoAuth: true},
			{Name: "task-not-found", Method: "GET", Path: "/tasks/00000000-0000-0000-0000-000000000000"},
			{Name: "create-invalid", Method: "POST", Path: "/generation/text-to-model", Body: map[string]any{}},
			{Name: "upload", Method: "POST", Path: "/files",
				Multipart: &align.File{Field: "file", Filename: "reference.png", MediaType: "image/png", Data: uploadPNG()},
				Capture:   map[string]string{"file_token": "data.file_token"}},
			{Name: "text-to-model", Method: "POST", Path: "/generation/text-to-model", Paid: true,
				Body: map[string]any{
					"model": "v3.1-20260211", "prompt": "a plain low poly humanoid robot standing upright with arms out in a T pose",
					"texture": false, "pbr": false, "face_limit": 10000,
				},
				Capture: map[string]string{"gen": "data.task_id"}},
			{Name: "text-to-model-poll", Method: "GET", Path: "/tasks/{gen}", RequireVar: "gen", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "rig-check", Method: "POST", Path: "/animations/rig-check", RequireVar: "gen",
				Body: map[string]any{"input": "{gen}"}, Capture: map[string]string{"rigcheck": "data.task_id"}},
			{Name: "rig-check-poll", Method: "GET", Path: "/tasks/{rigcheck}", RequireVar: "rigcheck", Poll: taskPoll()},
			{Name: "rig", Method: "POST", Path: "/animations/rig", Paid: true, RequireVar: "gen",
				Body: map[string]any{
					"input": "{gen}", "model": "v2.5-20260210", "out_format": "glb", "rig_type": "biped", "spec": "tripo",
				},
				Capture: map[string]string{"rig": "data.task_id"}},
			{Name: "rig-poll", Method: "GET", Path: "/tasks/{rig}", RequireVar: "rig", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "retarget", Method: "POST", Path: "/animations/retarget", Paid: true, RequireVar: "rig",
				Body: map[string]any{
					"input": "{rig}", "animation": "preset:walk", "out_format": "glb",
					"export_with_geometry": true, "animate_in_place": false,
				},
				Capture: map[string]string{"retarget": "data.task_id"}},
			{Name: "retarget-poll", Method: "GET", Path: "/tasks/{retarget}", RequireVar: "retarget", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "rig-v3", Method: "POST", Path: "/animations/rig", Paid: true, RequireVar: "gen",
				Body: map[string]any{
					"input": "{gen}", "model": "v3.0-20260909", "out_format": "glb", "rig_type": "biped", "spec": "tripo",
				},
				Capture: map[string]string{"rigv3": "data.task_id"}},
			{Name: "rig-v3-poll", Method: "GET", Path: "/tasks/{rigv3}", RequireVar: "rigv3", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "retarget-v3", Method: "POST", Path: "/animations/retarget", Paid: true, RequireVar: "rigv3",
				Body: map[string]any{
					"input": "{rigv3}", "animation": "preset:walk", "out_format": "glb",
					"export_with_geometry": true, "animate_in_place": false,
				},
				Capture: map[string]string{"retargetv3": "data.task_id"}},
			{Name: "retarget-v3-poll", Method: "GET", Path: "/tasks/{retargetv3}", RequireVar: "retargetv3", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "segment", Method: "POST", Path: "/mesh/segment", Paid: true, RequireVar: "gen",
				Body: map[string]any{
					"input": "{gen}", "model": "v2.0-20260430", "segmentation_granularity": "simple", "split_by_connectivity": true,
				},
				Capture: map[string]string{"seg": "data.task_id"}},
			{Name: "segment-poll", Method: "GET", Path: "/tasks/{seg}", RequireVar: "seg", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "segment-model", Method: "GET", Path: "/tasks/{seg}", RequireVar: "seg",
				Capture: map[string]string{"seg_model": "data.output.model_url"}},
			{Name: "convert", Method: "POST", Path: "/models/convert", Paid: true, RequireVar: "seg_model",
				Body: map[string]any{
					"input": "{seg_model}", "format": "STL", "bake": true, "fbx_preset": "blender",
					"texture_format": "PNG", "texture_size": 512, "with_animation": false,
				},
				Capture: map[string]string{"convert": "data.task_id"}},
			{Name: "convert-poll", Method: "GET", Path: "/tasks/{convert}", RequireVar: "convert", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "complete", Method: "POST", Path: "/mesh/complete", Paid: true, RequireVar: "seg",
				Body: map[string]any{
					"input": "{seg}", "model": "v1.0-20250506", "completion_mode": "ai_completion",
				},
				Capture: map[string]string{"complete": "data.task_id"}},
			{Name: "complete-poll", Method: "GET", Path: "/tasks/{complete}", RequireVar: "complete", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			// Seven endpoints that had no real sample (#89), run as a second partial run
			// (-steps from "image-upload" on). Each request is the cheapest option of its
			// endpoint that keeps the request body Plinth sends; every paid request is sent
			// once. The sources are a fresh geometry-only text-to-model task and an uploaded
			// picture.
			{Name: "image-upload", Method: "POST", Path: "/files",
				Multipart: &align.File{Field: "file", Filename: "reference.png", MediaType: "image/png", Data: align.ReferencePNG(512)},
				Capture:   map[string]string{"image_token": "data.file_token"}},
			{Name: "source-model", Method: "POST", Path: "/generation/text-to-model", Paid: true,
				Body: map[string]any{
					"model": "v3.1-20260211", "prompt": "a plain low poly cube",
					"texture": false, "pbr": false, "face_limit": 2000,
				},
				Capture: map[string]string{"source": "data.task_id"}},
			{Name: "source-model-poll", Method: "GET", Path: "/tasks/{source}", RequireVar: "source", Poll: taskPoll()},
			{Name: "image-to-model", Method: "POST", Path: "/generation/image-to-model", Paid: true, RequireVar: "image_token",
				Body: map[string]any{
					"model": "v3.1-20260211", "input": "{image_token}", "texture": false, "pbr": false, "face_limit": 2000,
				},
				Capture: map[string]string{"i2m": "data.task_id"}},
			{Name: "image-to-model-poll", Method: "GET", Path: "/tasks/{i2m}", RequireVar: "i2m", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}, Capture: map[string]string{"i2m_model": "data.output.model_url"}},
			{Name: "image-to-splat", Method: "POST", Path: "/generation/image-to-splat", Paid: true, RequireVar: "image_token",
				Body:    map[string]any{"input": "{image_token}", "model_seed": 42},
				Capture: map[string]string{"splat": "data.task_id"}},
			{Name: "image-to-splat-poll", Method: "GET", Path: "/tasks/{splat}", RequireVar: "splat", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "image-to-multiview", Method: "POST", Path: "/generation/image-to-multiview", Paid: true, RequireVar: "image_token",
				Body:    map[string]any{"input": "{image_token}"},
				Capture: map[string]string{"mv": "data.task_id"}},
			{Name: "image-to-multiview-poll", Method: "GET", Path: "/tasks/{mv}", RequireVar: "mv", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "edit-multiview", Method: "POST", Path: "/generation/edit-multiview", Paid: true, RequireVar: "mv",
				Body: map[string]any{
					"input":   "{mv}",
					"prompts": []map[string]any{{"view": "front", "prompt": "make the object green"}},
				},
				Capture: map[string]string{"emv": "data.task_id"}},
			{Name: "edit-multiview-poll", Method: "GET", Path: "/tasks/{emv}", RequireVar: "emv", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "texture", Method: "POST", Path: "/models/texture", Paid: true, RequireVar: "source",
				Body: map[string]any{
					"input": "{source}", "model": "v3.5-20260815", "texture_prompt": map[string]any{"text": "plain matte grey"},
					"texture_quality": "standard", "pbr": false, "bake": true, "delight": true,
				},
				Capture: map[string]string{"texture": "data.task_id"}},
			{Name: "texture-poll", Method: "GET", Path: "/tasks/{texture}", RequireVar: "texture", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "decimate", Method: "POST", Path: "/mesh/decimate", Paid: true, RequireVar: "source",
				Body: map[string]any{
					"input": "{source}", "model": "v3.0", "quad": false, "bake": true, "smart_uv": true,
				},
				Capture: map[string]string{"decimate": "data.task_id"}},
			{Name: "decimate-poll", Method: "GET", Path: "/tasks/{decimate}", RequireVar: "decimate", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "smartsegment-image", Method: "POST", Path: "/mesh/smartsegment", Paid: true, RequireVar: "image_token",
				Body: map[string]any{
					"input": "{image_token}", "seg_type": "image", "granularity": "coarse", "hint": "separate the sphere from the background",
				},
				Capture: map[string]string{"sseg": "data.task_id"}},
			{Name: "smartsegment-image-poll", Method: "GET", Path: "/tasks/{sseg}", RequireVar: "sseg", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			// A second smartsegment-image request (#89): the first one, on a shaded sphere,
			// ended failed ("Segmentation pipeline failed", not charged); this one sends a
			// picture with distinct parts. A new input of the same endpoint, not a replay.
			{Name: "figure-upload", Method: "POST", Path: "/files",
				Multipart: &align.File{Field: "file", Filename: "figure.png", MediaType: "image/png", Data: figurePNG()},
				Capture:   map[string]string{"figure_token": "data.file_token"}},
			{Name: "smartsegment-figure", Method: "POST", Path: "/mesh/smartsegment", Paid: true, RequireVar: "figure_token",
				Body: map[string]any{
					"input": "{figure_token}", "seg_type": "image", "granularity": "coarse", "hint": "separate the head, body, arms and legs",
				},
				Capture: map[string]string{"fseg": "data.task_id"}},
			{Name: "smartsegment-figure-poll", Method: "GET", Path: "/tasks/{fseg}", RequireVar: "fseg", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
			// Smart Segmentation of a model (#89): the model is a GLB an earlier step produced,
			// uploaded to /files like Plinth does, then segmented with the identity transform.
			{Name: "model-upload", Method: "POST", Path: "/files", RequireVar: "i2m_model",
				Multipart: &align.File{Field: "file", Filename: "model.glb", MediaType: "model/gltf-binary", FromVar: "i2m_model"},
				Capture:   map[string]string{"model_token": "data.file_token"}},
			{Name: "smartsegment-model", Method: "POST", Path: "/mesh/smartsegment", Paid: true, RequireVar: "model_token",
				Body: map[string]any{
					"input": "{model_token}", "seg_type": "model", "granularity": "coarse", "hint": "separate the parts",
					"transform": []int{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
				},
				Capture: map[string]string{"mseg": "data.task_id"}},
			{Name: "smartsegment-model-poll", Method: "GET", Path: "/tasks/{mseg}", RequireVar: "mseg", Poll: taskPoll(),
				Fetch: []string{"data.output.*"}},
		},
		Capture: align.CaptureConfig{
			Connection: "tripo", Scenario: "tripo-v3-alignment", APIVersion: "v3",
			PublicHosts: []string{"openapi.tripo3d.ai"},
			Projection: map[string]string{
				"balance": "number", "frozen": "number", "credits_consumed": "number",
				"created_at": "timestamp", "completed_at": "timestamp",
				// input.file.object.key is the account's storage path (tcli_<account>/<date>/...).
				"key": "text",
			},
		},
		RequestNotes: []string{
			"第二段官方运行（#89，2026-10-29，标签 seven、seg2、mseg）补采七个原先按文档构造的端点：`image-to-model`、`image-to-splat`、`image-to-multiview`、`edit-multiview`、`models/texture`、`mesh/decimate`、`mesh/smartsegment`（image 与 model 两种 seg_type）。每个端点取最便宜且保留 Plinth 请求体字段的选项，只发一次；付费 10 次共 260 积分（余额 3010 → 2750）：text-to-model 源任务 10、image-to-model 20、image-to-splat 30、image-to-multiview 10、edit-multiview 5、texture 10、decimate 35、smartsegment-image 85、smartsegment-model 55；第一个 smartsegment-image 请求失败未计费。",
			"七个端点的任务 type 有两处与原先的假设不同：mesh/decimate 是 `highpoly_to_lowpoly`（不是 `mesh_decimation`），mesh/smartsegment 按 seg_type 是 `smartsegment_image` 或 `smartsegment_model`（不是 `mesh_smart_segmentation`）；其余五个沿用相近类型的假设成立。输出字段与原假设也不同：image-to-model、texture 没有 `generated_image_url`，image-to-splat 只有 `model_url`，两个 multiview 的输出是嵌套对象 `generate_multiview_image`（edit 只含被编辑的视图），smartsegment 输出 `seg_model_url`、`mask_url`、`prompt`、`seg_task_id`、`model_task_id`；部分任务类型的部分状态没有 `progress`。fake 按样本改正。",
			"第一个 smartsegment-image 请求（球体示意图）受理后约四分钟以 `failed`、error_code 1001、`Segmentation pipeline failed` 结束，未计费；换一张有头、身体、手臂和腿的图片的第二个请求成功。两次都入库为样本（`tripo-v3-smartsegment-image-failed`、`tripo-v3-smartsegment-image`）。第一次的失败是官方对该输入的观测，原因未证；报告里 `smartsegment-image-poll` 步骤的差异就是 fake 对同一请求返回成功。",
			"产物 CDN 对不同文件返回不同的 content-type：模型、渲染图和 multiview 图片是 `binary/octet-stream`，分割遮罩 `mask.png` 是 `image/png`，edit-multiview 的视图是 `image/jpeg`；fake 照此返回。CDN 是 CloudFront 签名 URL，无效或过期都是 403 `text/xml` 的 AccessDenied。",
			"这一段报告只含第二段官方运行的步骤：第一阶段的官方原始运行（余额、生成、绑骨、分割等）没有保留在工作树外，本次不能重新生成那份报告，它仍在 `alignment-report.md`。",
		},
		Notes: smartSegmentFailureNotes(),
	}
}

// smartSegmentFailureNotes explains the differences of the one official request
// that ended failed: the fake answers the same request with a success.
func smartSegmentFailureNotes() align.Notes {
	const why = "官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本"
	notes := align.Notes{}
	for _, aspectDetail := range []string{
		"field-extra-in-fake|$.data.credits_consumed", "field-extra-in-fake|$.data.output.mask_url",
		"field-extra-in-fake|$.data.output.model_task_id", "field-extra-in-fake|$.data.output.prompt",
		"field-extra-in-fake|$.data.output.seg_model_url", "field-extra-in-fake|$.data.output.seg_task_id",
		"field-extra-in-fake|$.data.progress", "field-missing-in-fake|$.data.error_code",
		"field-missing-in-fake|$.data.error_message", "task-status-flow|official running>failed, fake queued>running>success",
	} {
		notes["smartsegment-image-poll|"+aspectDetail] = why
	}
	return notes
}
