package meshyopenapiv1

import (
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase is the official base URL of the route version this fake mirrors.
const OfficialBase = "https://api.meshy.ai/openapi/v1"

// OmittedKeys are response keys the committed samples cannot hold (a key with a
// leading underscore is not an identifier the sanitizer accepts); the fake adds
// them back and the drift test accounts for them. The official print/analyze
// report is the only carrier: printability._version.
var OmittedKeys = []string{"_version"}

// missingTask is a well-formed id that belongs to no task.
const missingTask = "01a18514-059e-73f7-a0ce-0f88a12c9300"

func taskPoll() *align.Poll {
	return &align.Poll{
		StatusPath: "status", ProgressPath: "progress",
		Terminal: []string{"SUCCEEDED", "FAILED", "CANCELED"}, Max: 360, Interval: 5 * time.Second,
	}
}

// pollStep is the poll of a created task; urls are the output fields fetched.
func pollStep(name, path, variable string, urls ...string) align.Step {
	return align.Step{Name: name, Method: "GET", Path: path + "/{" + variable + "}", RequireVar: variable,
		Poll: taskPoll(), Fetch: urls}
}

func create(name, path string, variable string, body map[string]any, require string) align.Step {
	return align.Step{Name: name, Method: "POST", Path: path, Paid: true, Body: body, RequireVar: require,
		Capture: map[string]string{variable: "result"}}
}

// Profile is the alignment script of the Meshy v1 fake. Every paid request is
// the cheapest option of its endpoint and is sent once. The chain mirrors what
// Plinth drives: a reference picture, a textured humanoid from it, then the
// processing endpoints that take that model, and the independent ones.
func Profile() align.Profile {
	humanoid := "a plain low poly humanoid robot standing upright in a T pose, front view, white background"
	return align.Profile{
		Provider: "meshy", API: "openapi-v1", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "balance", Method: "GET", Path: "/balance"},
			{Name: "balance-no-auth", Method: "GET", Path: "/balance", NoAuth: true},
			{Name: "task-not-found", Method: "GET", Path: "/image-to-3d/" + missingTask},
			{Name: "task-bad-id", Method: "GET", Path: "/image-to-3d/not-an-id"},
			{Name: "create-invalid", Method: "POST", Path: "/image-to-3d", Body: map[string]any{}},
			{Name: "create-invalid-remesh", Method: "POST", Path: "/remesh", Body: map[string]any{}},

			create("text-to-image", "/text-to-image", "t2i", map[string]any{
				"ai_model": "nano-banana-2", "prompt": humanoid, "aspect_ratio": "1:1", "pose_mode": "t-pose",
			}, ""),
			pollStep("text-to-image-poll", "/text-to-image", "t2i", "image_urls.*"),
			{Name: "text-to-image-url", Method: "GET", Path: "/text-to-image/{t2i}", RequireVar: "t2i",
				Capture: map[string]string{"img": "image_urls.0"}},
			create("image-to-image", "/image-to-image", "i2i", map[string]any{
				"ai_model": "nano-banana-2", "prompt": "the same robot in plain grey", "aspect_ratio": "1:1",
				"reference_image_urls": []string{"{img}"},
			}, "img"),
			pollStep("image-to-image-poll", "/image-to-image", "i2i", "image_urls.*"),

			create("image-to-3d", "/image-to-3d", "i23d", map[string]any{
				"image_url": "{img}", "ai_model": "meshy-7.1", "geometry_resolution": "standard",
				"should_texture": true, "enable_pbr": false, "texture_resolution": "2k",
				"should_remesh": true, "topology": "triangle", "target_polycount": 10000,
				"target_formats": []string{"glb"}, "pose_mode": "t-pose",
			}, "img"),
			pollStep("image-to-3d-poll", "/image-to-3d", "i23d", "model_urls.*", "thumbnail_url", "texture_urls.*"),
			{Name: "image-to-3d-model", Method: "GET", Path: "/image-to-3d/{i23d}", RequireVar: "i23d",
				Capture: map[string]string{"glb": "model_urls.glb"}},
			create("multi-image-to-3d", "/multi-image-to-3d", "mi23d", map[string]any{
				"image_urls": []string{"{img}"}, "ai_model": "meshy-7.1", "geometry_resolution": "standard",
				"should_texture": false, "should_remesh": true, "topology": "triangle", "target_polycount": 10000,
				"target_formats": []string{"glb"},
			}, "img"),
			pollStep("multi-image-to-3d-poll", "/multi-image-to-3d", "mi23d", "model_urls.*", "thumbnail_url"),

			create("remesh", "/remesh", "remesh", map[string]any{
				"input_task_id": "{i23d}", "target_formats": []string{"glb"}, "topology": "triangle", "target_polycount": 10000,
			}, "i23d"),
			pollStep("remesh-poll", "/remesh", "remesh", "model_urls.*", "thumbnail_url"),
			create("convert", "/convert", "convert", map[string]any{
				"input_task_id": "{remesh}", "target_formats": []string{"glb", "stl"},
			}, "remesh"),
			pollStep("convert-poll", "/convert", "convert", "model_urls.*"),
			create("retexture", "/retexture", "retexture", map[string]any{
				"input_task_id": "{i23d}", "ai_model": "meshy-7", "text_style_prompt": "plain matte grey metal",
				"enable_pbr": false, "texture_resolution": "2k", "target_formats": []string{"glb"},
			}, "i23d"),
			pollStep("retexture-poll", "/retexture", "retexture", "model_urls.*", "thumbnail_url", "texture_urls.*"),

			create("rigging", "/rigging", "rig", map[string]any{
				"input_task_id": "{i23d}", "height_meters": 1.7,
			}, "i23d"),
			pollStep("rigging-poll", "/rigging", "rig", "result.*"),
			create("animation", "/animations", "anim", map[string]any{
				"rig_task_id": "{rig}", "action_id": 0,
			}, "rig"),
			pollStep("animation-poll", "/animations", "anim", "result.*"),

			// The #103 shape: a 300000-face quad remesh is about 600000 triangles, over
			// the 320000-face limit rigging documents. Whether the official API refuses at
			// submit (400) or fails the task is recorded as observed.
			create("remesh-quad", "/remesh", "remeshquad", map[string]any{
				"input_task_id": "{i23d}", "target_formats": []string{"glb"}, "topology": "quad", "target_polycount": 300000,
			}, "i23d"),
			pollStep("remesh-quad-poll", "/remesh", "remeshquad", "model_urls.*"),
			create("rigging-oversized", "/rigging", "rigbig", map[string]any{
				"input_task_id": "{remeshquad}", "height_meters": 1.7,
			}, "remeshquad"),
			pollStep("rigging-oversized-poll", "/rigging", "rigbig", "result.*"),

			create("uv-unwrap", "/uv-unwrap", "uv", map[string]any{"model_url": "{glb}"}, "glb"),
			pollStep("uv-unwrap-poll", "/uv-unwrap", "uv", "model_urls.*", "thumbnail_url"),
			create("resize", "/resize", "resize", map[string]any{"model_url": "{glb}", "resize_height": 1.7, "origin_at": "bottom"}, "glb"),
			pollStep("resize-poll", "/resize", "resize", "model_urls.*"),
			create("print-analyze", "/print/analyze", "analyze", map[string]any{"model_url": "{glb}", "printer_type": "fdm"}, "glb"),
			pollStep("print-analyze-poll", "/print/analyze", "analyze", "result.*"),
			create("print-multi-color", "/print/multi-color", "multicolor", map[string]any{
				"model_url": "{glb}", "style": "realistic", "max_colors": 4, "max_depth": 3, "printer_brand": "bambu",
			}, "glb"),
			pollStep("print-multi-color-poll", "/print/multi-color", "multicolor", "model_urls.*"),
			create("text-to-motion", "/text-to-motion", "motion", map[string]any{
				"prompt": "a person waves one hand", "mode": "prime", "duration": 2,
			}, ""),
			pollStep("text-to-motion-poll", "/text-to-motion", "motion", "result.*"),

			{Name: "delete-text-to-image", Method: "DELETE", Path: "/text-to-image/{t2i}", RequireVar: "t2i"},
			{Name: "delete-image-to-image", Method: "DELETE", Path: "/image-to-image/{i2i}", RequireVar: "i2i"},
			{Name: "delete-image-to-3d", Method: "DELETE", Path: "/image-to-3d/{i23d}", RequireVar: "i23d"},
			{Name: "delete-multi-image-to-3d", Method: "DELETE", Path: "/multi-image-to-3d/{mi23d}", RequireVar: "mi23d"},
			{Name: "delete-remesh", Method: "DELETE", Path: "/remesh/{remesh}", RequireVar: "remesh"},
			{Name: "delete-remesh-quad", Method: "DELETE", Path: "/remesh/{remeshquad}", RequireVar: "remeshquad"},
			{Name: "delete-convert", Method: "DELETE", Path: "/convert/{convert}", RequireVar: "convert"},
			{Name: "delete-retexture", Method: "DELETE", Path: "/retexture/{retexture}", RequireVar: "retexture"},
			{Name: "delete-rigging", Method: "DELETE", Path: "/rigging/{rig}", RequireVar: "rig"},
			{Name: "delete-rigging-oversized", Method: "DELETE", Path: "/rigging/{rigbig}", RequireVar: "rigbig"},
			{Name: "delete-animation", Method: "DELETE", Path: "/animations/{anim}", RequireVar: "anim"},
			{Name: "delete-uv-unwrap", Method: "DELETE", Path: "/uv-unwrap/{uv}", RequireVar: "uv"},
			{Name: "delete-resize", Method: "DELETE", Path: "/resize/{resize}", RequireVar: "resize"},
			{Name: "delete-print-analyze", Method: "DELETE", Path: "/print/analyze/{analyze}", RequireVar: "analyze"},
			{Name: "delete-print-multi-color", Method: "DELETE", Path: "/print/multi-color/{multicolor}", RequireVar: "multicolor"},
			{Name: "delete-text-to-motion", Method: "DELETE", Path: "/text-to-motion/{motion}", RequireVar: "motion"},
		},
		Capture: align.CaptureConfig{
			Connection: "meshy", Scenario: "meshy-openapi-v1-alignment", APIVersion: "v1",
			PublicHosts: []string{"api.meshy.ai", "assets.meshy.ai", "cdn.meshy.ai"},
			OmitKeys:    OmittedKeys,
			Projection: map[string]string{
				"created_at": "timestamp", "started_at": "timestamp", "finished_at": "timestamp", "expires_at": "timestamp",
				"consumed_credits": "number", "seed": "number", "preceding_tasks": "number",
				"balance": "number", "monthly_credits": "number", "permanent_credits": "number", "monthly_resets_at": "timestamp",
			},
		},
		RequestNotes: []string{
			"每个付费请求都取该端点最便宜的参数（nano-banana-2、meshy-7.1、standard 几何、2k 贴图、1 万面三角形、无 PBR），每个端点只发一次，没有重放已消费的请求。",
			"处理类端点（remesh、convert、retexture、rigging、animations）经 `input_task_id` 引用前面步骤的任务；uv-unwrap、resize、print/analyze、print/multi-color 与 image-to-3d 的图片经 `model_url`/`image_url` 引用前面步骤产物的公开 URL。Plinth 对本地来源改发 data URI（`inline_model_input`），端点与响应结构相同，只有该字段的值不同。",
			"官方 API 对 `rigging` 的输入面数上限（32 万）在提交时不返回 400：300000 面 quad 的 Remesh 来源（约 60 万三角面）被接受（202），任务在运行 10% 处以 FAILED、`task_error.type=invalid_input` 结束。fake 按此复现（见 `rigging-oversized` 两步）。",
			"16 个删除步骤中，4 个随主运行发出，其余 12 个在主运行结束后用同一批任务 id 补发（两段官方请求按步骤合并到报告）；删除不收费。",
			"官方 DELETE 任务返回 200 且无响应体；任务在 IN_PROGRESS 时删除按官方文档返回 409，实测运行只删除了已结束任务，该情形按文档构造。",
		},
		Notes: align.Notes{},
	}
}
