package mattingapiv1

import (
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase names the real upstream the fake is aligned with: the
// background-removal service the dev-test stack's MATTING_BASE_URL points at
// (a private host; its address lives only in the stack's runtime environment).
// Plinth appends api/v1/jobs, api/v1/batches/{id} and
// api/v1/tasks/{id}/download to that base.
const OfficialBase = "dev-test matting service /api/v1"

// pipeline are the declared defaults of the Catalog's background-removal
// Offering (birefnet and sdmatte settings), sent as one JSON form field.
const pipeline = `{"birefnet_process_res":1024,"birefnet_mask_blur":6,"expand":240,"tapered_corners":true,"blur_radius":30,"sdmatte_process_res":1024,"transparent_object":true,"mask_refine":true,"sensitivity":0.1}`

// Profile is the alignment script of the matting fake. The service runs on
// the operator's own hardware and has no per-request price, so no step is
// marked paid; the submission is still made exactly once.
func Profile() align.Profile {
	return align.Profile{
		Provider: "matting", API: "api-v1", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "jobs-no-auth", Method: "POST", Path: "/jobs", NoAuth: true,
				Form:  []align.Field{{Name: "pipeline_params_json", Value: pipeline}},
				Files: []align.File{{Field: "files", Filename: "input_1.png", MediaType: "image/png", Data: align.ReferencePNG(256)}}},
			{Name: "jobs-invalid", Method: "POST", Path: "/jobs", Form: []align.Field{{Name: "pipeline_params_json", Value: pipeline}}},
			{Name: "jobs", Method: "POST", Path: "/jobs",
				Form:    []align.Field{{Name: "pipeline_params_json", Value: pipeline}},
				Files:   []align.File{{Field: "files", Filename: "input_1.png", MediaType: "image/png", Data: align.ReferencePNG(256)}},
				Capture: map[string]string{"batch": "batch_id"}},
			{Name: "batch", Method: "GET", Path: "/batches/{batch}", RequireVar: "batch",
				Poll:    &align.Poll{StatusPath: "batch_status", Terminal: []string{"SUCCESS", "PARTIAL_SUCCESS", "FAILED", "CANCELED"}, Max: 120, Interval: 3 * time.Second},
				Capture: map[string]string{"task": "tasks.0.task_id"}},
			{Name: "batch-unknown", Method: "GET", Path: "/batches/00000000-0000-4000-8000-000000000000"},
			{Name: "download", Method: "GET", Path: "/tasks/{task}/download", RequireVar: "task"},
			{Name: "download-no-auth", Method: "GET", Path: "/tasks/{task}/download", RequireVar: "task", NoAuth: true},
		},
		Capture: align.CaptureConfig{
			Connection: "matting", Scenario: "matting-api-v1-alignment", APIVersion: "v1", AliasBaseHost: true,
			Projection: map[string]string{
				"batch_id": "id", "task_id": "id", "created_at": "timestamp", "updated_at": "timestamp",
				"started_at": "timestamp", "finished_at": "timestamp", "source_name": "text", "download_path": "text",
			},
		},
		RequestNotes: []string{
			"官方服务是运营方自己的 GPU 服务，通过明文 HTTP 访问；wirefixture 只接受 https，提交的样本把请求 URL 规范为 https 并把主机别名成 example.test，响应字节不变。",
			"服务没有按次计价，所有步骤都不计为付费；提交只发一次，轮询约 30 次直到批次 SUCCESS。",
			"批次与任务 id（batch_xxx、task_xxx）与 download_path 在样本里是占位，fake 自行生成同形态的 id 和 `/api/v1/tasks/<id>/download` 路径。",
			"没有观测到 FAILED 批次、未知任务的下载 404：fake 按已观测的字段结构构造（端点表标明）。",
		},
		Notes: align.Notes{},
	}
}
