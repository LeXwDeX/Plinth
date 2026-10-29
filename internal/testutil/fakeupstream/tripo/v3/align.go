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
		},
		Capture: align.CaptureConfig{
			Connection: "tripo", Scenario: "tripo-v3-alignment", APIVersion: "v3",
			PublicHosts: []string{"openapi.tripo3d.ai"},
			Projection:  map[string]string{"balance": "number", "frozen": "number", "create_time": "timestamp"},
		},
		Notes: align.Notes{},
	}
}
