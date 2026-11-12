package tripov3

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #276: the fake takes a decimation the way the official page
// (https://developers.tripo3d.ai/en/docs/mesh-decimate, read 2026-11-12) lets it
// be asked for. These tests pin what decimate.go promises.

func decimateMultiviewBody() map[string]any {
	return map[string]any{
		"model":  "v3.1-20260211",
		"inputs": []map[string]any{{"front": "file_front"}, {"left": "file_left"}, {"back": "file_back"}},
	}
}

func TestDecimateTakesTheDocumentedSourceTasks(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	text := f.create("/generation/text-to-model", textBody(), key)
	image := f.create("/generation/image-to-model", map[string]any{"model": "v3.1-20260211", "input": "file_x"}, key)
	multiview := f.create("/generation/multiview-to-model", decimateMultiviewBody(), key)
	for _, source := range []string{text, image, multiview} {
		f.finish(source, key)
	}
	for name, source := range map[string]string{
		"text_to_model":      text,
		"image_to_model":     image,
		"multiview_to_model": multiview, // the page lists it next to the other two
		"file token":         "file_abc123",
		"public URL":         "https://example.test/model.glb",
	} {
		t.Run(name, func(t *testing.T) {
			id := f.create("/mesh/decimate", decimateSampleRequest(source), key)
			if statuses, _ := f.finish(id, key); statuses[len(statuses)-1] != statusSuccess {
				t.Fatalf("decimation of %s ended %v", name, statuses)
			}
		})
	}

	// A task of any other type is not a "3D generation task" and is refused with the
	// create envelope of the samples.
	rig := f.create("/animations/rig", map[string]any{"input": text, "model": "v3.0-20260909"}, key)
	segment := f.create("/mesh/segment", map[string]any{"input": text, "model": "v2.0-20260430"}, key)
	convert := f.create("/models/convert", map[string]any{"input": "https://example.test/model.glb", "format": "STL"}, key)
	decimated := f.create("/mesh/decimate", decimateSampleRequest(text), key)
	for name, source := range map[string]string{"a rig": rig, "a segmentation": segment, "a conversion": convert, "another decimation": decimated} {
		t.Run("refuses "+name, func(t *testing.T) {
			status, _, body := f.do(http.MethodPost, "/mesh/decimate", key, decimateSampleRequest(source))
			if status != http.StatusBadRequest || field(t, body, "code").(float64) != 1004 {
				t.Fatalf("decimation of %s = %d %v", name, status, body)
			}
		})
	}
	// A task the fake does not know is the real 404/code 2001.
	status, _, body := f.do(http.MethodPost, "/mesh/decimate", key, decimateSampleRequest("00000000-0000-4000-8000-0000000000aa"))
	if status != http.StatusNotFound || field(t, body, "code").(float64) != 2001 {
		t.Fatalf("decimation of an unknown task = %d %v", status, body)
	}
}

func TestDecimateRequestFollowsTheOfficialPage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/text-to-model", textBody(), key)
	f.finish(source, key)
	with := func(changes map[string]any) map[string]any {
		body := map[string]any{"input": source, "model": "v3.0"}
		for name, value := range changes {
			if value == nil {
				delete(body, name)
				continue
			}
			body[name] = value
		}
		return body
	}
	for name, tc := range map[string]struct {
		body    map[string]any
		refused bool
	}{
		"the request Plinth sends":           {with(map[string]any{"bake": true, "quad": false, "smart_uv": true}), false},
		"model omitted is the v2.0 default":  {with(map[string]any{"model": nil}), false},
		"unknown model":                      {with(map[string]any{"model": "v4.0"}), true},
		"smart_uv with v3.0":                 {with(map[string]any{"smart_uv": true}), false},
		"smart_uv with v2.0":                 {with(map[string]any{"model": "v2.0", "smart_uv": true}), true},
		"smart_uv with v1.0":                 {with(map[string]any{"model": "v1.0", "smart_uv": true, "face_limit": 3000}), true},
		"smart_uv without a model (v2.0)":    {with(map[string]any{"model": nil, "smart_uv": true}), true},
		"smart_uv must be a boolean":         {with(map[string]any{"smart_uv": "yes"}), true},
		"bake must be a boolean":             {with(map[string]any{"bake": 1}), true},
		"quad must be a boolean":             {with(map[string]any{"quad": "true"}), true},
		"triangle face_limit at the minimum": {with(map[string]any{"face_limit": 500}), false},
		"triangle face_limit at the maximum": {with(map[string]any{"face_limit": 20000}), false},
		"triangle face_limit below 500":      {with(map[string]any{"face_limit": 499}), true},
		"triangle face_limit above 20000":    {with(map[string]any{"face_limit": 20001}), true},
		"quad face_limit at the maximum":     {with(map[string]any{"quad": true, "face_limit": 10000}), false},
		"quad face_limit above 10000":        {with(map[string]any{"quad": true, "face_limit": 10001}), true},
		"face_limit must be an integer":      {with(map[string]any{"face_limit": 5000.5}), true},
		"face_limit must be a number":        {with(map[string]any{"face_limit": "5000"}), true},
		"v1.0 needs a face_limit":            {with(map[string]any{"model": "v1.0"}), true},
		"v1.0 triangle face_limit up to 2M":  {with(map[string]any{"model": "v1.0", "face_limit": 2000000}), false},
		"v1.0 triangle face_limit past 2M":   {with(map[string]any{"model": "v1.0", "face_limit": 2000001}), true},
		"v1.0 quad face_limit past 150000":   {with(map[string]any{"model": "v1.0", "quad": true, "face_limit": 150001}), true},
		"part_names are strings":             {with(map[string]any{"part_names": []string{"head"}}), false},
		"part_names must be strings":         {with(map[string]any{"part_names": []any{1}}), true},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.do(http.MethodPost, "/mesh/decimate", key, tc.body)
			if tc.refused {
				if status != http.StatusBadRequest || field(t, body, "code").(float64) != 1004 {
					t.Fatalf("%v = %d %v, want 400/code 1004", tc.body, status, body)
				}
				return
			}
			if status != http.StatusOK {
				t.Fatalf("%v = %d %v", tc.body, status, body)
			}
		})
	}
}

func TestDecimateTaskObjectShowsTheDocumentedDefaults(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/text-to-model", textBody(), key)
	f.finish(source, key)
	for name, tc := range map[string]struct {
		changes             map[string]any
		bake, quad, smartUV bool
	}{
		// The page's defaults: bake true, quad false, smart_uv false.
		"nothing but the model":    {map[string]any{"model": "v3.0"}, true, false, false},
		"the request Plinth sends": {map[string]any{"model": "v3.0", "bake": true, "quad": false, "smart_uv": true}, true, false, true},
		"bake off, quad on":        {map[string]any{"model": "v3.0", "bake": false, "quad": true, "face_limit": 5000}, false, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"input": source}
			for field, value := range tc.changes {
				body[field] = value
			}
			id := f.create("/mesh/decimate", body, key)
			_, done := f.finish(id, key)
			input, _ := field(t, done, "data", "input").(map[string]any)
			if input["bake"] != tc.bake || input["quad"] != tc.quad || input["smart_uv"] != tc.smartUV || input["original_model_id"] != source {
				t.Fatalf("input = %v, want bake %v quad %v smart_uv %v of %s", input, tc.bake, tc.quad, tc.smartUV, source)
			}
		})
	}
}

// inspectGLB reads a delivered GLB the way the model tools do.
func inspectGLB(t *testing.T, data []byte) modelinspect.Report {
	t.Helper()
	model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("the delivered GLB cannot be read: %v", err)
	}
	return modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
}

func TestDecimateDeliversATexturedModelAtTheRequestedSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/text-to-model", textBody(), key)
	f.finish(source, key)
	for name, tc := range map[string]struct {
		changes map[string]any
		target  int
	}{
		"face_limit 10000":                 {map[string]any{"face_limit": 10000}, 10000},
		"face_limit 20000":                 {map[string]any{"face_limit": 20000}, 20000},
		"face_limit 500":                   {map[string]any{"face_limit": 500}, 500},
		"adaptive when face_limit is left": {nil, decimateAdaptiveTriangles},
	} {
		t.Run(name, func(t *testing.T) {
			body := decimateSampleRequest(source)
			for field, value := range tc.changes {
				body[field] = value
			}
			id := f.create("/mesh/decimate", body, key)
			_, done := f.finish(id, key)
			status, _, data := f.download(f.outputURL(done, "model_url"))
			if status != http.StatusOK || !strings.HasPrefix(f.outputURL(done, "model_url"), f.server.URL) {
				t.Fatalf("model_url answered %d", status)
			}
			if err := deliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Fatalf("the decimated GLB fails the delivery check: %v", err)
			}
			report := inspectGLB(t, data)
			want := int64(fakeupstream.LowPolyTriangles(tc.target))
			if report.Triangles != want || report.Triangles > int64(tc.target) {
				t.Fatalf("triangles = %d, want %d (never above the %d asked for)", report.Triangles, want, tc.target)
			}
			if report.TextureCount == 0 || !report.UV.AllHaveUV {
				t.Fatalf("a baked, unwrapped result lost its texture or UVs: textures %d, all UV %v", report.TextureCount, report.UV.AllHaveUV)
			}
		})
	}
}

// The page's only format line is "outputs GLB; FBX when quad=true" (under v2.0),
// and the one real v3.0 quad task delivered an FBX (#185), so quad=true delivers one.
func TestDecimateQuadDeliversAnFBX(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/text-to-model", textBody(), key)
	f.finish(source, key)
	body := decimateSampleRequest(source)
	body["quad"], body["face_limit"] = true, 5000
	id := f.create("/mesh/decimate", body, key)
	_, done := f.finish(id, key)
	url := f.outputURL(done, "model_url")
	if !strings.HasSuffix(strings.SplitN(url, "?", 2)[0], ".fbx") {
		t.Fatalf("a quad decimation names %s, want an .fbx", url)
	}
	status, _, data := f.download(url)
	if status != http.StatusOK {
		t.Fatalf("download = %d", status)
	}
	if err := deliveryErr(t, data, "converted_model", "fbx", "model/fbx"); err != nil {
		t.Fatalf("the quad FBX fails the delivery check: %v", err)
	}
}
