package hyper3dapiv2

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// OfficialBase is the official base URL the Catalog's hyper3d_base_url defaults
// to.
const OfficialBase = "https://api.hyper3d.com/api/v2"

// referencePNG is a shaded sphere on a white background: an object the image to
// 3D endpoint can find.
func referencePNG() []byte {
	const size = 512
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			dx, dy := float64(x-size/2), float64(y-size/2)
			if d2 := dx*dx + dy*dy; d2 < 180*180 {
				shade := uint8(220 - int(d2/180/180*140))
				img.Set(x, y, color.RGBA{R: shade, G: shade / 2, B: 60, A: 255})
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

func statusPoll() *align.Poll {
	return &align.Poll{
		StatusPath: "jobs.*.status", Terminal: []string{"Done", "Failed"}, Max: 240, Interval: 5 * time.Second,
	}
}

// cheapGeneration are the form fields of the cheapest Gen-2.5 request (0.5
// credits: the lowest tier and texture, no addon).
func cheapGeneration(extra ...align.Field) []align.Field {
	return append([]align.Field{
		{Name: "tier", Value: "Gen-2.5-Extreme-Low"}, {Name: "geometry_file_format", Value: "glb"},
		{Name: "material", Value: "PBR"}, {Name: "mesh_mode", Value: "Raw"}, {Name: "quality", Value: "medium"},
		{Name: "texture_mode", Value: "extreme-low"},
	}, extra...)
}

func submitSteps(name, form string, fields []align.Field, files []align.File, require string) []align.Step {
	return []align.Step{
		{Name: name, Method: "POST", Path: "/" + form, Paid: true, Form: fields, Files: files, RequireVar: require,
			Capture: map[string]string{name + "_uuid": "uuid", name + "_key": "jobs.subscription_key"}},
		{Name: name + "-status", Method: "POST", Path: "/status", RequireVar: name + "_key", Poll: statusPoll(),
			Body: map[string]any{"subscription_key": "{" + name + "_key}"}},
		{Name: name + "-download", Method: "POST", Path: "/download", RequireVar: name + "_uuid",
			Body: map[string]any{"task_uuid": "{" + name + "_uuid}"}, Fetch: []string{"list.*"},
			Capture: map[string]string{name + "_url": "list.0.url"}},
	}
}

// Profile is the alignment script of the Hyper3D fake. Every paid request is
// the cheapest option of its endpoint and is sent once.
func Profile() align.Profile {
	picture := align.File{Field: "images", Filename: "reference.png", MediaType: "image/png", Data: referencePNG()}
	var steps []align.Step
	steps = append(steps,
		align.Step{Name: "balance", Method: "GET", Path: "/check_balance"},
		align.Step{Name: "balance-no-auth", Method: "GET", Path: "/check_balance", NoAuth: true},
		align.Step{Name: "status-invalid", Method: "POST", Path: "/status", Body: map[string]any{}},
		align.Step{Name: "download-invalid", Method: "POST", Path: "/download", Body: map[string]any{}},
		align.Step{Name: "rodin-invalid", Method: "POST", Path: "/rodin", Form: []align.Field{{Name: "tier", Value: "Gen-2.5-Extreme-Low"}}},
		align.Step{Name: "rodin-no-auth", Method: "POST", Path: "/rodin", NoAuth: true, Form: cheapGeneration(align.Field{Name: "prompt", Value: "a cube"})},
	)
	steps = append(steps, submitSteps("rodin-text", "rodin",
		cheapGeneration(align.Field{Name: "prompt", Value: "a plain solid red cube with sharp edges, no text"}), nil, "")...)
	steps = append(steps, submitSteps("rodin-image", "rodin", cheapGeneration(), []align.File{picture}, "")...)
	steps = append(steps, submitSteps("bang", "bang", []align.Field{
		{Name: "asset_id", Value: "{rodin-text_uuid}"}, {Name: "geometry_file_format", Value: "glb"},
		{Name: "material", Value: "None"}, {Name: "resolution", Value: "Basic"}, {Name: "mesh_mode", Value: "Raw"},
		{Name: "strength", Value: "5"},
	}, nil, "rodin-text_uuid")...)
	steps = append(steps, submitSteps("texture", "rodin_texture_only", []align.Field{
		{Name: "geometry_file_format", Value: "glb"}, {Name: "material", Value: "PBR"}, {Name: "resolution", Value: "Basic"},
		{Name: "texture_mode", Value: "extreme-low"}, {Name: "prompt", Value: "plain matte red"},
	}, []align.File{
		{Field: "model", Filename: "model.glb", MediaType: "model/gltf-binary", FromVar: "bang_url"},
		{Field: "image", Filename: "reference.png", MediaType: "image/png", Data: referencePNG()},
	}, "bang_url")...)
	return align.Profile{
		Provider: "hyper3d", API: "api-v2", Official: OfficialBase, Steps: steps,
		Capture: align.CaptureConfig{
			Connection: "hyper3d", Scenario: "hyper3d-api-v2-alignment", APIVersion: "v2",
			PublicHosts: []string{"api.hyper3d.com", "file.hyper3d.com"},
			Projection: map[string]string{
				"submit_time": "timestamp", "consumed": "number", "balance": "number",
				"queue_length": "number",
			},
		},
		RequestNotes: []string{
			"为每个端点取最便宜的请求：Gen-2.5-Extreme-Low、texture_mode=extreme-low，0.5 credit；Plinth 实际使用 Gen-2.5-Extreme-High，同一端点、同一响应结构。",
			"官方运行分三段，报告把它们合并：第一段提交 rodin（文生、图生）、bang、texture，对 HTTP 201 的轮询未被工具识别为轮询（已修正），因此 status/download 留到第二段；第二段用同一批 uuid 与 subscription_key 补轮询和下载，并重发 bang；第三段重发 texture。",
			"第一段的 bang 在 Rodin 任务尚未完成时发出，官方以 HTTP 201 + `{\"error\":\"ASSET_NOT_AVAILABLE\"}` 拒绝（未扣费）；fake 复现该拒绝，第二段在任务完成后重发并成功。",
			"第一段的 texture 用了过小的占位 GLB，三个子作业以 Failed 结束（status 响应带顶层 message `Invalid input.`），已计费；第三段改用 bang 产出的 GLB 重发，是新的输入而非重放。该 Failed 响应未入库为样本，fake 的 Failed 状态按它的结构构造。",
			"官方在请求缺少提示词与图片时返回 HTTP 201 + `{\"error\":\"INVALID_REQUEST\"}`（不是 4xx）；fake 复现。",
		},
		Notes: align.Notes{},
	}
}
