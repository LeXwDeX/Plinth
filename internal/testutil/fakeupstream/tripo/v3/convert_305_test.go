package tripov3

import (
	"bytes"
	"encoding/json"
	"image/png"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #305: what the paid run of 2026-11-16 showed about converting the GLB of a rig
// (evidence-305-rig-convert.candidate.json) is in the fake: the credits of every
// task, the move of the origin and the texture, skin and clip the conversion
// writes, and the time the creation of a conversion from a URL takes.

func inspectModel(t *testing.T, data []byte) modelinspect.Report {
	t.Helper()
	model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("the GLB cannot be read: %v", err)
	}
	return modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
}

// baseColor is the base color texture slot of the model's only material.
func baseColor(t *testing.T, report modelinspect.Report) modelinspect.MaterialSlotReport {
	t.Helper()
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if slot.Slot == "base_color" && slot.Present {
				return slot
			}
		}
	}
	t.Fatal("the model has no base color texture")
	return modelinspect.MaterialSlotReport{}
}

// poll reads a task n times and returns the last poll's data object.
func (f *fixture) poll(id string, n int) map[string]any {
	f.t.Helper()
	var data map[string]any
	for i := 0; i < n; i++ {
		status, _, body := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		if status != http.StatusOK {
			f.t.Fatalf("poll %s = %d %v", id, status, body)
		}
		data = field(f.t, body, "data").(map[string]any)
	}
	return data
}

func credits(t *testing.T, data map[string]any) float64 {
	t.Helper()
	value, ok := data["credits_consumed"].(float64)
	if !ok {
		t.Fatalf("no credits_consumed in %v", data)
	}
	return value
}

// riggedModel creates a generation and a rig of it and returns the rig and the URL
// of the GLB the rig delivered, which is what Plinth converts.
func (f *fixture) riggedModel() (rig, modelURL string) {
	f.t.Helper()
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)
	rig = f.create("/animations/rig", map[string]any{"input": gen, "model": "v3.0-20260909", "rig_type": "biped", "spec": "tripo", "out_format": "glb"}, key)
	_, done := f.finish(rig, key)
	return rig, f.outputURL(done, "model_url")
}

// A rig costs 25 credits and a rig check nothing; every poll from the queued one on
// already shows the whole price (the queued conversions of 2026-11-24, #577), and a
// task that fails is not charged.
func TestRigAndRigCheckAreBilledLikeThePaidRun(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)
	check := f.create("/animations/rig-check", map[string]any{"input": gen}, key)
	if got := credits(t, f.poll(check, 3)); got != 0 {
		t.Fatalf("a rig check costs %v", got)
	}
	rig := f.create("/animations/rig", map[string]any{"input": gen, "model": "v3.0-20260909"}, key)
	if queued := f.poll(rig, 1); queued["status"] != statusQueued || credits(t, queued) != 25 {
		t.Fatalf("queued poll: %v, want the frozen 25 credits", queued)
	}
	if running := f.poll(rig, 1); running["status"] != statusRunning || credits(t, running) != 25 {
		t.Fatalf("running poll: %v, want the frozen 25 credits", running)
	}
	if done := f.poll(rig, 1); done["status"] != statusSuccess || credits(t, done) != 25 {
		t.Fatalf("success poll: %v", done)
	}
	// The failed poll is the real failed sample, which carries no credits at all.
	failed := f.create("/animations/rig", map[string]any{"input": gen, "model": "v3.0-20260909"}, key, "X-Fake-Scenario", "task-failed")
	f.poll(failed, 2)
	if data := f.poll(failed, 1); data["status"] != statusFailed || data["credits_consumed"] != nil && credits(t, data) != 0 {
		t.Fatalf("a failed rig is charged: %v", data)
	}
}

// A conversion costs 5 credits and 10 when an option leaves the page's default;
// fbx_preset and bake do not (the pricing page). 10 is what the paid run was
// charged for PNG textures of 2048 pixels.
func TestConversionIsBasicOrAdvancedLikeThePricingPage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, modelURL := f.riggedModel()
	for name, tc := range map[string]struct {
		changes map[string]any
		want    float64
	}{
		"nothing":                       {nil, 5},
		"the defaults named":            {map[string]any{"texture_format": "JPEG", "texture_size": 4096, "bake": true, "fbx_preset": "mixamo", "with_animation": true}, 5},
		"PNG textures":                  {map[string]any{"texture_format": "PNG"}, 10},
		"a smaller size":                {map[string]any{"texture_size": 2048}, 10},
		"PNG at 2048 (the paid run)":    {map[string]any{"texture_format": "PNG", "texture_size": 2048, "with_animation": true, "bake": true, "fbx_preset": "blender"}, 10},
		"the pivot":                     {map[string]any{"pivot_to_center_bottom": true}, 10},
		"a face limit":                  {map[string]any{"face_limit": 1000}, 10},
		"quad":                          {map[string]any{"quad": true}, 10},
		"the pivot named off":           {map[string]any{"pivot_to_center_bottom": false}, 5},
		"a scale":                       {map[string]any{"scale_factor": 2}, 10},
		"the unit scale":                {map[string]any{"scale_factor": 1}, 5},
		"a flattened bottom":            {map[string]any{"flatten_bottom": true}, 10},
		"a flatten threshold":           {map[string]any{"flatten_bottom_threshold": 0.01}, 10},
		"the larger size is advanced":   {map[string]any{"texture_size": 8192}, 10},
		"the default size is not":       {map[string]any{"texture_size": 4096}, 5},
		"PNG with the default size too": {map[string]any{"texture_format": "PNG", "texture_size": 4096}, 10},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"input": modelURL, "format": "GLTF"}
			for option, value := range tc.changes {
				body[option] = value
			}
			id := f.create("/models/convert", body, key)
			f.poll(id, 1)
			if running := f.poll(id, 1); running["status"] != statusRunning || credits(t, running) != tc.want {
				t.Fatalf("running poll: %v, want the frozen %v credits", running, tc.want)
			}
			if done := f.poll(id, 1); credits(t, done) != tc.want {
				t.Fatalf("success poll: %v, want %v credits", done, tc.want)
			}
		})
	}
}

// A decimation is priced by model (the decimate and pricing pages, re-read
// 2026-11-25, #579): v3.0 35 credits for a triangle mesh (observed 2026-11-24,
// #577) and 40 for a quad mesh, v2.0 30 (also the omitted model, the page's
// default) and v1.0 10. Before #579 the fake charged 35 for every model.
func TestDecimationIsBilledLikeThePricingPage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)
	for name, tc := range map[string]struct {
		body map[string]any
		want float64
	}{
		"triangles":     {map[string]any{"input": gen, "model": "v3.0"}, 35},
		"quad on v3.0":  {map[string]any{"input": gen, "model": "v3.0", "quad": true}, 40},
		"quad on v2.0":  {map[string]any{"input": gen, "model": "v2.0", "quad": true}, 30},
		"v2.0":          {map[string]any{"input": gen, "model": "v2.0"}, 30},
		"no model":      {map[string]any{"input": gen}, 30},
		"v1.0":          {map[string]any{"input": gen, "model": "v1.0", "face_limit": 1000}, 10},
		"a face limit":  {map[string]any{"input": gen, "model": "v3.0", "face_limit": 5000}, 35},
		"baked and uvs": {map[string]any{"input": gen, "model": "v3.0", "bake": true, "smart_uv": true}, 35},
	} {
		id := f.create("/mesh/decimate", tc.body, key)
		f.poll(id, 1)
		if running := f.poll(id, 1); credits(t, running) != tc.want {
			t.Errorf("%s: %v credits, want %v", name, credits(t, running), tc.want)
		}
	}
}

// The finished conversion of an imported model (here a URL) reports the move it
// made, the one the paid run showed: normalize_scale and normalize_translation.
// Without the pivot the translation lifts the middle of the box onto the origin;
// with it, the middle of the bottom face. Every format reports it, as every
// conversion of an imported model did on 2026-11-24 (#577; the STL of 2026-10-29
// reported neither, and the newer round is followed), and no running poll shows
// it. The conversion of a task named by its id reports none
// (TestRealRoundPollsKeepTheStructureOfTheRealTasks).
func TestConversionReportsTheMoveOfTheOrigin(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, modelURL := f.riggedModel()
	_, maximum := fakeupstream.ModelBounds()
	for name, tc := range map[string]struct {
		format string
		pivot  bool
		want   float64
	}{
		"GLTF at the middle":     {"GLTF", false, -maximum[1] / 2},
		"FBX at the middle":      {"FBX", false, -maximum[1] / 2},
		"GLTF at the bottom":     {"GLTF", true, 0},
		"FBX at the bottom":      {"FBX", true, 0},
		"STL at the middle":      {"STL", false, -maximum[1] / 2},
		"USDZ at the bottom":     {"USDZ", true, 0},
		"OBJ at the middle":      {"OBJ", false, -maximum[1] / 2},
		"3MF at the bottom":      {"3MF", true, 0},
		"the pivot named as off": {"GLTF", false, -maximum[1] / 2},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"input": modelURL, "format": tc.format, "texture_format": "PNG", "texture_size": 2048}
			if tc.pivot || name == "the pivot named as off" {
				body["pivot_to_center_bottom"] = tc.pivot
			}
			id := f.create("/models/convert", body, key)
			f.poll(id, 1)
			if running := f.poll(id, 1); running["status"] != statusRunning {
				t.Fatalf("running poll: %v", running)
			} else if output, _ := running["output"].(map[string]any); output["normalize_scale"] != nil || output["normalize_translation"] != nil {
				t.Fatalf("a running poll reports the move: %v", output)
			}
			output, _ := f.poll(id, 1)["output"].(map[string]any)
			translation, _ := output["normalize_translation"].([]any)
			if output["normalize_scale"] != float64(1) || len(translation) != 3 {
				t.Fatalf("output = %v", output)
			}
			if got := translation[1].(float64); math.Abs(got-tc.want) > 1e-4 || math.Abs(translation[0].(float64)) > 1e-4 || math.Abs(translation[2].(float64)) > 1e-4 {
				t.Fatalf("normalize_translation = %v, want [0 %v 0]", translation, tc.want)
			}
		})
	}
}

// The GLB a GLTF conversion of a rig delivers is what the paid run read back: the
// skin of the rig, textures in the requested format at the requested size, and the
// origin where the request put it (the middle of the box without
// pivot_to_center_bottom, the feet on the floor with it).
func TestConvertedGLBOfARigKeepsTheSkinAndFollowsTheRequest(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	rig, modelURL := f.riggedModel()
	_, maximum := fakeupstream.ModelBounds()

	// The rig's own GLB is the baseline: one skin, the fixture's texture, the feet
	// on the floor.
	_, _, native := f.download(modelURL)
	nativeReport := inspectModel(t, native)
	if len(nativeReport.Skins) != 1 || nativeReport.BBox.BottomCenterOffset[1] != 0 {
		t.Fatalf("the rig's GLB: skins %v, bottom centre %v", nativeReport.Skins, nativeReport.BBox.BottomCenterOffset)
	}

	for name, tc := range map[string]struct {
		changes    map[string]any
		mime       string
		edge       int
		bottomAt   float64
		wantFormat string
	}{
		"the game request":  {map[string]any{"texture_format": "PNG", "texture_size": 2048, "pivot_to_center_bottom": true, "with_animation": true, "bake": true}, "image/png", 2048, 0, "PNG"},
		"without the pivot": {map[string]any{"texture_format": "PNG", "texture_size": 1024, "with_animation": true}, "image/png", 1024, -maximum[1] / 2, "PNG"},
		"JPEG":              {map[string]any{"texture_format": "JPEG", "texture_size": 512, "pivot_to_center_bottom": true}, "image/jpeg", 512, 0, "JPEG"},
		"the size default":  {map[string]any{"texture_format": "PNG", "pivot_to_center_bottom": true}, "image/png", 4096, 0, "PNG"},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"input": modelURL, "format": "GLTF"}
			for option, value := range tc.changes {
				body[option] = value
			}
			_, done := f.finish(f.create("/models/convert", body, key), key)
			status, contentType, data := f.download(f.outputURL(done, "model_url"))
			if status != http.StatusOK || contentType != ArtifactContentType || !strings.HasSuffix(strings.Split(f.outputURL(done, "model_url"), "?")[0], "/model.glb") {
				t.Fatalf("download: %d %q", status, contentType)
			}
			if err := deliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Fatalf("the converted GLB is refused by the delivery check: %v", err)
			}
			report := inspectModel(t, data)
			slot := baseColor(t, report)
			if slot.MIME != tc.mime || slot.Width != tc.edge || slot.Height != tc.edge {
				t.Fatalf("base color %s %dx%d, want %s %d", slot.MIME, slot.Width, slot.Height, tc.mime, tc.edge)
			}
			if len(report.Skins) != len(nativeReport.Skins) || report.Skins[0].Joints != nativeReport.Skins[0].Joints {
				t.Fatalf("skins %v, the rig has %v", report.Skins, nativeReport.Skins)
			}
			if len(report.Animations) != 0 {
				t.Fatalf("a rig carries no clip, the conversion has %v", report.Animations)
			}
			if report.Triangles != nativeReport.Triangles {
				t.Fatalf("triangles %d, the rig has %d", report.Triangles, nativeReport.Triangles)
			}
			if got := report.BBox.BottomCenterOffset[1]; math.Abs(got-tc.bottomAt) > 1e-4 {
				t.Fatalf("the bottom centre is at y = %v, want %v", got, tc.bottomAt)
			}
		})
	}
	_ = rig
}

// The conversion keeps the triangles of the model it was asked to convert (a
// decimation) and the clip of a retargeted rig when the request keeps animation.
func TestConvertedGLBKeepsTheTrianglesAndTheClipOfItsSource(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)
	decimate := f.create("/mesh/decimate", map[string]any{"input": gen, "model": "v3.0", "face_limit": 5000, "bake": true}, key)
	_, decimated := f.finish(decimate, key)
	convert := func(source string, changes map[string]any) []byte {
		body := map[string]any{"input": source, "format": "GLTF", "texture_format": "PNG", "texture_size": 256}
		for option, value := range changes {
			body[option] = value
		}
		_, done := f.finish(f.create("/models/convert", body, key), key)
		_, _, data := f.download(f.outputURL(done, "model_url"))
		return data
	}
	_, _, native := f.download(f.outputURL(decimated, "model_url"))
	nativeTriangles := inspectModel(t, native).Triangles
	if kept := inspectModel(t, convert(f.outputURL(decimated, "model_url"), nil)).Triangles; kept != nativeTriangles || nativeTriangles != int64(fakeupstream.LowPolyTriangles(5000)) {
		t.Fatalf("the decimation has %d triangles, the conversion %d (want %d)", nativeTriangles, kept, fakeupstream.LowPolyTriangles(5000))
	}
	if report := inspectModel(t, convert(f.outputURL(decimated, "model_url"), nil)); len(report.Skins) != 0 {
		t.Fatalf("a decimation has no skin: %v", report.Skins)
	}

	rig := f.create("/animations/rig", map[string]any{"input": gen, "model": "v3.0-20260909"}, key)
	f.finish(rig, key)
	retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, key)
	_, walking := f.finish(retarget, key)
	source := f.outputURL(walking, "model_url")
	if clip := inspectModel(t, convert(source, map[string]any{"with_animation": true})); len(clip.Skins) != 1 || len(clip.Animations) != 1 {
		t.Fatalf("a conversion that keeps animation: skins %v animations %v", clip.Skins, clip.Animations)
	}
	if still := inspectModel(t, convert(source, map[string]any{"with_animation": false})); len(still.Skins) != 1 || len(still.Animations) != 0 {
		t.Fatalf("a conversion that drops animation: skins %v animations %v", still.Skins, still.Animations)
	}
}

// The FBX of a conversion embeds the picture in the requested format and at the
// requested size (the paid run: PNG of 2048 pixels), and keeps the skin of a rig.
func TestConvertedFBXEmbedsTheRequestedTextureAtTheRequestedSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, modelURL := f.riggedModel()
	_, done := f.finish(f.create("/models/convert", map[string]any{
		"input": modelURL, "format": "FBX", "texture_format": "PNG", "texture_size": 2048, "with_animation": true, "bake": true, "fbx_preset": "blender",
	}, key), key)
	_, _, data := f.download(f.outputURL(done, "model_url"))
	if err := deliveryErr(t, data, "model", "fbx", "model/fbx"); err != nil {
		t.Fatalf("the FBX is refused by the delivery check: %v", err)
	}
	start := bytes.Index(data, []byte("\x89PNG"))
	if start < 0 {
		t.Fatal("no PNG is embedded")
	}
	size, err := png.DecodeConfig(bytes.NewReader(data[start:]))
	if err != nil || size.Width != 2048 || size.Height != 2048 {
		t.Fatalf("the embedded picture is %dx%d (%v), want 2048", size.Width, size.Height, err)
	}
	if !bytes.Contains(data, []byte("MockRoot")) {
		t.Fatal("the FBX of a rig lost its skin")
	}
}

// A conversion that asks for nothing about its output is the plain fixture it
// has always been, so the Offerings that never named a texture or an origin keep
// their bytes; so is one that names a texture the fixture cannot draw.
func TestConversionWithoutOutputOptionsIsThePlainFixture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	_, done := f.finish(gen, key)
	_, _, model := f.download(f.outputURL(done, "model_url"))
	for name, changes := range map[string]map[string]any{
		"nothing":                 {},
		"only bake and a preset":  {"bake": true, "fbx_preset": "mixamo", "with_animation": false},
		"a format it cannot draw": {"texture_format": "WEBP", "texture_size": 2048},
	} {
		body := map[string]any{"input": f.outputURL(done, "model_url"), "format": "GLTF"}
		for option, value := range changes {
			body[option] = value
		}
		_, converted := f.finish(f.create("/models/convert", body, key), key)
		if _, _, data := f.download(f.outputURL(converted, "model_url")); !bytes.Equal(data, model) {
			t.Errorf("%s: the conversion differs from the fixture (%d and %d bytes)", name, len(data), len(model))
		}
	}
}

// The model a conversion fetched from a URL is a new model: the poll names it
// original_model_id and it is neither the source task nor another conversion's.
func TestConversionOfAURLNamesANewModel(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	rig, modelURL := f.riggedModel()
	first := f.create("/models/convert", map[string]any{"input": modelURL, "format": "GLTF"}, key)
	second := f.create("/models/convert", map[string]any{"input": modelURL, "format": "GLTF"}, key)
	imported := func(id string) string {
		input, _ := f.poll(id, 1)["input"].(map[string]any)
		model, _ := input["original_model_id"].(string)
		return model
	}
	one, two := imported(first), imported(second)
	if one == "" || two == "" || one == two || one == rig || two == rig {
		t.Fatalf("original_model_id %q and %q, want two new models other than %s", one, two, rig)
	}
}

// Creating a conversion from a URL takes a configurable time (the paid run: 47 to
// 48 seconds), so a test can prove that a client's timeout is long enough. The
// default is short; a negative value answers at once; a conversion from a task
// id never waits.
func TestConversionOfAURLTakesTheConfiguredTimeToCreate(t *testing.T) {
	took := func(config fakeupstream.Config, body map[string]any) time.Duration {
		f := newFixture(t, config)
		start := time.Now()
		f.create("/models/convert", body, key)
		return time.Since(start)
	}
	url := map[string]any{"input": "https://example.test/x.glb", "format": "GLTF"}
	if elapsed := took(fakeupstream.Config{ConvertURLDelay: 250 * time.Millisecond}, url); elapsed < 250*time.Millisecond {
		t.Errorf("a slow creation took %v, want at least 250ms", elapsed)
	}
	if elapsed := took(fakeupstream.Config{ConvertURLDelay: -1}, url); elapsed > 200*time.Millisecond {
		t.Errorf("a creation without delay took %v", elapsed)
	}
	if elapsed := took(fakeupstream.Config{}, url); elapsed > time.Second {
		t.Errorf("the default creation took %v, want a short wait", elapsed)
	}
}

// The delay is part of the creation request: a client whose timeout is shorter
// than it gives up, which is what a request timeout under the real API's 47 to 48
// seconds would do to every conversion of a URL.
func TestACreationSlowerThanTheClientTimeoutTimesOut(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{ConvertURLDelay: 2 * time.Second})
	raw, _ := json.Marshal(map[string]any{"input": "https://example.test/x.glb", "format": "GLTF"})
	request, _ := http.NewRequest(http.MethodPost, f.base()+"/models/convert", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	if response, err := (&http.Client{Timeout: 100 * time.Millisecond}).Do(request); err == nil {
		response.Body.Close()
		t.Fatal("the creation answered inside the client's timeout")
	}
}
