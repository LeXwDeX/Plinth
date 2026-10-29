package meshyopenapiv2

import (
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase is the official base URL of the route version this fake mirrors.
const OfficialBase = "https://api.meshy.ai/openapi/v2"

// missingTask is a well-formed id that belongs to no task.
const missingTask = "01a18514-059e-73f7-a0ce-0f88a12c9300"

func taskPoll() *align.Poll {
	return &align.Poll{
		StatusPath: "status", ProgressPath: "progress",
		Terminal: []string{"SUCCEEDED", "FAILED", "CANCELED"}, Max: 360, Interval: 5 * time.Second,
	}
}

// Profile is the alignment script of the Meshy v2 fake: the text-to-3d preview
// and its refine, each sent once with the cheapest options.
func Profile() align.Profile {
	return align.Profile{
		Provider: "meshy", API: "openapi-v2", Official: OfficialBase,
		Steps: []align.Step{
			{Name: "task-not-found", Method: "GET", Path: "/text-to-3d/" + missingTask},
			{Name: "task-no-auth", Method: "GET", Path: "/text-to-3d/" + missingTask, NoAuth: true},
			{Name: "create-invalid", Method: "POST", Path: "/text-to-3d", Body: map[string]any{}},
			{Name: "preview", Method: "POST", Path: "/text-to-3d", Paid: true,
				Body: map[string]any{
					"mode": "preview", "ai_model": "meshy-7.1", "geometry_resolution": "standard",
					"prompt":        "a plain low poly humanoid robot standing upright in a T pose",
					"should_remesh": true, "topology": "triangle", "target_polycount": 10000,
					"target_formats": []string{"glb"}, "pose_mode": "t-pose",
				},
				Capture: map[string]string{"preview": "result"}},
			{Name: "preview-poll", Method: "GET", Path: "/text-to-3d/{preview}", RequireVar: "preview", Poll: taskPoll(),
				Fetch: []string{"model_urls.*", "thumbnail_url"}},
			{Name: "refine", Method: "POST", Path: "/text-to-3d", Paid: true, RequireVar: "preview",
				Body: map[string]any{
					"mode": "refine", "preview_task_id": "{preview}", "ai_model": "meshy-7.1",
					"enable_pbr": false, "texture_resolution": "2k", "target_formats": []string{"glb"},
				},
				Capture: map[string]string{"refine": "result"}},
			{Name: "refine-poll", Method: "GET", Path: "/text-to-3d/{refine}", RequireVar: "refine", Poll: taskPoll(),
				Fetch: []string{"model_urls.*", "thumbnail_url", "texture_urls.*"}},
			{Name: "delete-preview", Method: "DELETE", Path: "/text-to-3d/{preview}", RequireVar: "preview"},
			{Name: "delete-refine", Method: "DELETE", Path: "/text-to-3d/{refine}", RequireVar: "refine"},
		},
		Capture: align.CaptureConfig{
			Connection: "meshy", Scenario: "meshy-openapi-v2-alignment", APIVersion: "v2",
			PublicHosts: []string{"api.meshy.ai", "assets.meshy.ai", "cdn.meshy.ai"},
			Projection: map[string]string{
				"created_at": "timestamp", "started_at": "timestamp", "finished_at": "timestamp", "expires_at": "timestamp",
				"consumed_credits": "number", "seed": "number", "preceding_tasks": "number",
			},
		},
		RequestNotes: []string{
			"preview 与 refine 各发一次，取最便宜的参数（meshy-7.1、standard 几何、1 万面三角形、2k 无 PBR 贴图）；refine 引用 preview 任务。",
			"官方 API 的 v2 路由按 id 查询任一类型的任务（用 v2 路由读到了 rigging 任务），fake 只按路由所属的任务族服务，不模拟这种跨族读取。",
			"v1 的处理类端点可把 v2 的 preview/refine 任务当 `input_task_id`；fake 的 v1 与 v2 共用任务库，行为一致。",
		},
		Notes: align.Notes{},
	}
}
