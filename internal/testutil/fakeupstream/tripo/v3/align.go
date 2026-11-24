package tripov3

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
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

// generationPoll is taskPoll with the budget of the multiview Offering: its
// timeout_seconds is 3600 and it polls every 5 seconds. A poll is free, and a
// run that gave up on a paid generation would leave the model unfetched.
func generationPoll() *align.Poll {
	return &align.Poll{
		StatusPath: "data.status", ProgressPath: "data.progress",
		Terminal: taskTerminal, Max: 720, Interval: 5 * time.Second,
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

// viewPNG is the block figure of figurePNG seen from one side, 512x512 on white:
// front and back are wide with both arms (the front has a face band, the back a
// darker body), the left view is narrow with one arm. Three different pictures of
// one object for a multiview upload; what they show decides nothing about the
// protocol under test. They are drawn once per process, because every Profile()
// call (and each test that makes one) builds the script again.
func viewPNG(view string) []byte { return viewPictures()[view] }

var viewPictures = sync.OnceValue(func() map[string][]byte {
	return map[string][]byte{"front": drawView("front"), "left": drawView("left"), "back": drawView("back")}
})

func drawView(view string) []byte {
	const size = 512
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	fill(0, 0, size, size, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	body := color.RGBA{R: 200, G: 40, B: 40, A: 255}
	arm := color.RGBA{R: 40, G: 90, B: 200, A: 255}
	leg := color.RGBA{R: 40, G: 150, B: 60, A: 255}
	if view == "left" {
		fill(221, 170, 291, 330, body) // body, seen from the side
		fill(236, 180, 276, 300, arm)  // the near arm
		fill(226, 330, 286, 460, leg)  // the legs, one behind the other
	} else {
		if view == "back" {
			body = color.RGBA{R: 140, G: 30, B: 30, A: 255}
		}
		fill(196, 170, 316, 330, body)
		fill(80, 180, 196, 220, arm)
		fill(316, 180, 432, 220, arm)
		fill(206, 330, 250, 460, leg)
		fill(262, 330, 306, 460, leg)
	}
	for y := 60; y < 170; y++ { // head
		for x := 201; x < 311; x++ {
			if dx, dy := x-256, y-115; dx*dx+dy*dy < 55*55 {
				img.SetRGBA(x, y, color.RGBA{R: 240, G: 190, B: 40, A: 255})
			}
		}
	}
	if view == "front" {
		fill(231, 105, 281, 117, color.RGBA{R: 30, G: 30, B: 30, A: 255}) // the face band
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// Probe is one free negative probe of the alignment script (#577): a request the
// official API refuses before it creates anything, and the answer it gave. The
// official run of 2026-11-25 sent all eleven, none was billed and none made a task;
// its answers are the Want fields, and the fake gives the same ones (the nine JSON
// probes are also the sample evidence-577-negative-probes; the recorder keeps no
// empty or text/plain file part, so the two upload refusals are only here).
type Probe struct {
	Step align.Step
	// WantStatus, WantCode, WantMessage and WantSuggestion are the real answer: the
	// HTTP status and the envelope's code, message and suggestion.
	WantStatus     int
	WantCode       int
	WantMessage    string
	WantSuggestion string
	// WantRequestID says the envelope carries a request_id (the 400 and 404 answers
	// do, the two 401 answers do not).
	WantRequestID bool
	// Risk says what the official API would bill if it accepted the request after
	// all, "" when acceptance costs nothing either.
	Risk string
}

// The suggestions of the real answers.
const (
	suggestParameters = "Refer to the API documentation for parameter requirements"
	probeModels       = "P1-20260311, P2-20260801, v2.5-20250123, v3.0-20250812, v3.1-20260211"
)

// probeText is the prompt of the generation probes: short and plain, so an
// unexpected acceptance builds the cheapest model.
const probeText = "a plain grey cube"

// probeWrongKey is the dummy key of the wrong-key probe: unknown to the official API,
// and the fake's invalid-key scenario word.
const probeWrongKey = "fake-invalid-key"

// Probes are the free negative probes, each OptIn (a routine refresh does not send
// them; -steps names them). Those whose input is a file token run after the free
// "upload" step, which captures file_token:
//
//	-steps upload,probe-upload-empty,probe-upload-text,probe-model-invalid,probe-model-retired,probe-generate-parts-texture-omitted,probe-fast-texture-v25,probe-seg-type-invalid,probe-convert-format-glb,probe-create-no-auth,probe-wrong-key,probe-input-task-not-found
//
// None is Paid: each is refused by the documented rule it probes (the official run of
// 2026-11-25 confirmed it: twelve requests with the upload, no credit spent). Risk
// names what a request would cost if the official API accepted it instead.
func Probes() []Probe {
	optIn := func(step align.Step) align.Step { step.OptIn = true; return step }
	text := func(extra map[string]any) map[string]any {
		body := map[string]any{"model": "v3.1-20260211", "prompt": probeText, "texture": false, "pbr": false, "face_limit": 1000}
		for name, value := range extra {
			if value == nil {
				delete(body, name)
				continue
			}
			body[name] = value
		}
		return body
	}
	return []Probe{
		{Step: optIn(align.Step{Name: "probe-upload-empty", Method: "POST", Path: "/files",
			Multipart: &align.File{Field: "file", Filename: "empty.png", MediaType: "image/png", Data: []byte{}}}),
			WantStatus: 400, WantCode: 2003, WantMessage: "The image file is empty",
			WantSuggestion: "Check if the file is corrupted", WantRequestID: true},
		{Step: optIn(align.Step{Name: "probe-upload-text", Method: "POST", Path: "/files",
			Multipart: &align.File{Field: "file", Filename: "notes.txt", MediaType: "text/plain", Data: []byte("not a picture or a model\n")}}),
			WantStatus: 400, WantCode: 2004, WantMessage: "This image file type is not supported",
			WantSuggestion: "Refer to API documentation for supported image file types", WantRequestID: true},
		{Step: optIn(align.Step{Name: "probe-model-invalid", Method: "POST", Path: "/generation/text-to-model",
			Body: text(map[string]any{"model": "v9.9-20990101"})}),
			WantStatus: 400, WantCode: 1004,
			WantMessage:    "invalid model 'v9.9-20990101', allowed values: " + probeModels,
			WantSuggestion: suggestParameters, WantRequestID: true},
		// A retired name is an unknown name: 1004, not the 2015 of the error page.
		{Step: optIn(align.Step{Name: "probe-model-retired", Method: "POST", Path: "/generation/text-to-model",
			Body: text(map[string]any{"model": "v2.0-20240919"})}),
			WantStatus: 400, WantCode: 1004,
			WantMessage:    "invalid model 'v2.0-20240919', allowed values: " + probeModels,
			WantSuggestion: suggestParameters, WantRequestID: true,
			Risk: "a geometry-only text model of a retired version, about 10 credits, if the name is still served"},
		{Step: optIn(align.Step{Name: "probe-generate-parts-texture-omitted", Method: "POST", Path: "/generation/text-to-model",
			Body: text(map[string]any{"generate_parts": true, "texture": nil})}),
			WantStatus: 400, WantCode: 1004,
			WantMessage:    "generate_parts is incompatible with texture; set texture=false to generate parts",
			WantSuggestion: suggestParameters, WantRequestID: true,
			Risk: "a textured text model with parts, about 40 credits (10 + texture 10 + parts 20), if an omitted texture is not taken as true"},
		{Step: optIn(align.Step{Name: "probe-fast-texture-v25", Method: "POST", Path: "/generation/text-to-model",
			Body: text(map[string]any{"texture": true, "texture_quality": "fast", "texture_version": "v2.5-20250123"})}),
			WantStatus: 400, WantCode: 1004,
			WantMessage:    "texture_quality 'fast' requires texture v3.5-20260815; got v2.5-20250123",
			WantSuggestion: suggestParameters, WantRequestID: true,
			Risk: "a fast-textured text model, about 20 credits, if fast is served with the v2.5 texture"},
		{Step: optIn(align.Step{Name: "probe-seg-type-invalid", Method: "POST", Path: "/mesh/smartsegment", RequireVar: "file_token",
			Body: map[string]any{"input": "{file_token}", "seg_type": "mesh", "granularity": "coarse"}}),
			WantStatus: 400, WantCode: 1004, WantMessage: "invalid seg_type; allowed: image, model",
			WantSuggestion: suggestParameters, WantRequestID: true,
			Risk: "a Smart Segmentation of the uploaded picture, 85 credits, if an unknown seg_type falls back to image"},
		{Step: optIn(align.Step{Name: "probe-convert-format-glb", Method: "POST", Path: "/models/convert", RequireVar: "file_token",
			Body: map[string]any{"input": "{file_token}", "format": "GLB"}}),
			WantStatus: 400, WantCode: 1004, WantMessage: "invalid format 'GLB', allowed values: 3MF, FBX, GLTF, OBJ, STL, USDZ",
			WantSuggestion: suggestParameters, WantRequestID: true,
			Risk: "a conversion, 5 credits, if GLB were a target (its input is a picture, so the task would fail and a failed task is not billed)"},
		{Step: optIn(align.Step{Name: "probe-create-no-auth", Method: "POST", Path: "/generation/text-to-model", NoAuth: true,
			Body: text(nil)}),
			WantStatus: 401, WantCode: 2, WantMessage: "Authentication required", WantSuggestion: "Check if your credentials is valid"},
		{Step: optIn(align.Step{Name: "probe-wrong-key", Method: "GET", Path: "/account/balance", WrongKey: probeWrongKey}),
			WantStatus: 401, WantCode: 2, WantMessage: invalidKeyMessage, WantSuggestion: invalidKeySuggestion},
		{Step: optIn(align.Step{Name: "probe-input-task-not-found", Method: "POST", Path: "/animations/rig-check",
			Body: map[string]any{"input": "00000000-0000-4000-8000-000000000000"}}),
			WantStatus: 404, WantCode: 2001, WantMessage: "The task is not found",
			WantSuggestion: "Check if the task is committed by you or whether the format is correct", WantRequestID: true},
	}
}

// Profile is the alignment script of the Tripo v3 fake. Every paid request,
// except the two of the multiview run at the end (#386), is the cheapest option
// of its endpoint and is sent once. The chain mirrors what Plinth drives:
// generation, rig check, rigging, animation, segmentation, a model export and
// part completion.
func Profile() align.Profile {
	profile := align.Profile{
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
			// A third partial run (#386): the multiview model Plinth requests with its
			// production defaults and its 3MF export, run with -artifacts to keep the real
			// bytes of the 3MF: -steps mv-upload-front,mv-upload-left,mv-upload-back,
			// multiview-to-model,multiview-to-model-poll,convert-3mf,convert-3mf-poll.
			// Unlike the cheapest-option steps above these are the most expensive options
			// of the endpoint (about 75 credits: the generation at the Offering's defaults,
			// 70, and one export, 5), and two requests are paid. The uploads are chained
			// through RequireVar, so a missing token skips everything after it. The bodies
			// are what the adapter sends for the Offering
			// tripo/tripo-v3.1-multiview/image-to-3d at its defaults with three views
			// (TestAlignmentScriptSendsWhatTheAdapterSends in internal/providers/tripo).
			{Name: "mv-upload-front", Method: "POST", Path: "/files",
				Multipart: &align.File{Field: "file", Filename: "input.png", MediaType: "image/png", Data: viewPNG("front")},
				Capture:   map[string]string{"mv_front": "data.file_token"}},
			{Name: "mv-upload-left", Method: "POST", Path: "/files", RequireVar: "mv_front",
				Multipart: &align.File{Field: "file", Filename: "input.png", MediaType: "image/png", Data: viewPNG("left")},
				Capture:   map[string]string{"mv_left": "data.file_token"}},
			{Name: "mv-upload-back", Method: "POST", Path: "/files", RequireVar: "mv_left",
				Multipart: &align.File{Field: "file", Filename: "input.png", MediaType: "image/png", Data: viewPNG("back")},
				Capture:   map[string]string{"mv_back": "data.file_token"}},
			{Name: "multiview-to-model", Method: "POST", Path: "/generation/multiview-to-model", Paid: true, RequireVar: "mv_back",
				Body: map[string]any{
					"model": "v3.1-20260211",
					"inputs": []map[string]string{
						{"front": "{mv_front}"}, {"left": "{mv_left}"}, {"back": "{mv_back}"},
					},
					"texture": true, "pbr": true, "texture_quality": "extreme", "texture_version": "v3.5-20260815",
					"texture_alignment": "original_image", "geometry_quality": "detailed", "orientation": "default",
					"face_limit": 2000000, "quad": false, "smart_low_poly": false, "auto_size": false,
					"export_uv": true, "generate_parts": false,
				},
				Capture: map[string]string{"m2m": "data.task_id"}},
			{Name: "multiview-to-model-poll", Method: "GET", Path: "/tasks/{m2m}", RequireVar: "m2m", Poll: generationPoll(),
				Fetch: []string{"data.output.*"}},
			{Name: "convert-3mf", Method: "POST", Path: "/models/convert", Paid: true, RequireVar: "m2m",
				Body:    map[string]any{"input": "{m2m}", "format": "3MF"},
				Capture: map[string]string{"m2m_convert": "data.task_id"}},
			{Name: "convert-3mf-poll", Method: "GET", Path: "/tasks/{m2m_convert}", RequireVar: "m2m_convert", Poll: taskPoll(),
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
			"第三段官方运行（#386，2026-11-19，标签 mv386、conv386，经用户同意）：三次视角图上传、按 Plinth 生产默认参数的 multiview-to-model（三个视角，纹理与 PBR 开、extreme 质量、detailed 几何、200 万面）及其轮询，再对该任务做一次 3MF 转换（请求只有 input 与 format）及其轮询，立即取回产物。两次付费共 75 积分：多视图生成 70、3MF 转换 5；两者在 queued 轮询里就已报出全价，fake 在 queued 轮询里仍沿用样本的值（值层面，本报告不比较值）。",
			"这次运行补出了 multiview-to-model 原先只按文档构造的结构：任务轮询的 `input` 是请求自己的参数（texture、pbr、texture_quality、texture_version、texture_alignment、geometry_quality、orientation、face_limit、quad、smart_low_poly、auto_size、export_uv、generate_parts）、请求的 model 以 `model_version` 出现，另有 `files` 数组（front、left、back、right 四项，发送过的视角是 `{object:{bucket,key}}`，没发的是 null）；渲染图是 WebP，不是其他生成任务（2026-10-29）的 PNG；真实运行里渲染图在 progress 99 时先于模型出现。运行前 fake 的这一轮询取自绑骨任务的模板（`input` 里是 original_model_id、out_format、spec、topology），渲染图是 PNG：24 项未说明差异，已按证据改正为 0 项（脱敏证据 `evidence-386-multiview-model.candidate.json`、`evidence-386-convert-3mf.candidate.json`，由 `evidence_multiview_3mf_test.go` 钉住）。",
			"3MF 转换（task type `convert_model`，`input` 为 `{format, original_model_id}`，output 只有 `model_url`）的轮询与 fake 没有结构差异。取回的 3MF 是 35,952,441 字节的 ZIP64 包：三个成员（`3D/3dmodel.model`、`[Content_Types].xml`、`_rels/.rels`），每个成员都按 ZIP64 成帧，模型根元素声明 Tripo 的厂商命名空间 `https://www.tripo3d.ai/ns/aigc/1.0` 并带一条 AIGC 标签。Plinth 的归档隔离原先把这个命名空间当外部地址拒收，这就是 #386 的根因；名单加入该标识符后真实文件通过。fake 现在交付同结构的小包（`internal/testutil/fakeupstream/tripo3mf.go`），不再是通用的 3MF 夹具。字节本身（真实产物含约 96 万个顶点）不入库。",
			"产物 CDN 对不同文件返回不同的 content-type：模型、渲染图和 multiview 图片是 `binary/octet-stream`，分割遮罩 `mask.png` 是 `image/png`，edit-multiview 的视图是 `image/jpeg`；fake 照此返回。CDN 是 CloudFront 签名 URL，无效或过期都是 403 `text/xml` 的 AccessDenied。",
			"这一段报告只含第三段官方运行的步骤；第一段（余额、生成、绑骨、分割等）与第二段（#89，七个补采端点）的报告仍在 `alignment-report.md` 与 `alignment-report-89.md`。",
		},
		Notes: smartSegmentFailureNotes(),
	}
	for _, probe := range Probes() {
		profile.Steps = append(profile.Steps, probe.Step)
	}
	return profile
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
