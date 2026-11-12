package tripov3

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/splat"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

type fixture struct {
	t      *testing.T
	server *httptest.Server
	fake   *fakeupstream.Server
}

func newFixture(t *testing.T, config fakeupstream.Config) *fixture {
	t.Helper()
	fake, err := fakeupstream.New(config, Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &fixture{t: t, server: server, fake: fake}
}

func (f *fixture) base() string { return f.server.URL + Prefix }

// do sends one request. key "" omits the authorization header.
func (f *fixture) do(method, path, key string, body any, headers ...string) (int, http.Header, map[string]any) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, f.base()+path, reader)
	if err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return response.StatusCode, response.Header, decoded
}

func field(t *testing.T, body map[string]any, path ...any) any {
	t.Helper()
	value, ok := fakeupstream.Dig(body, path...)
	if !ok {
		t.Fatalf("response has no %v: %v", path, body)
	}
	return value
}

// doRaw sends one request whose body is sent verbatim, for malformed JSON.
func (f *fixture) doRaw(method, path, key, raw string, headers ...string) (int, http.Header, map[string]any) {
	f.t.Helper()
	request, err := http.NewRequest(method, f.base()+path, strings.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	rawResponse, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	_ = json.Unmarshal(rawResponse, &decoded)
	return response.StatusCode, response.Header, decoded
}

// upload posts one multipart file to /files.
func (f *fixture) upload(filename, mediaType string, data []byte, key string, headers ...string) (int, http.Header, map[string]any) {
	f.t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		f.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		f.t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, f.base()+"/files", &buffer)
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return response.StatusCode, response.Header, decoded
}

func (f *fixture) create(path string, body any, key string, headers ...string) string {
	f.t.Helper()
	status, _, response := f.do(http.MethodPost, path, key, body, headers...)
	if status != http.StatusOK {
		f.t.Fatalf("POST %s = %d %v", path, status, response)
	}
	return field(f.t, response, "data", "task_id").(string)
}

// finish polls until the task leaves queued/running and returns the statuses
// seen and the last body.
func (f *fixture) finish(id string, key string) ([]string, map[string]any) {
	f.t.Helper()
	var seen []string
	for i := 0; i < 8; i++ {
		status, _, body := f.do(http.MethodGet, "/tasks/"+id, key, nil)
		if status != http.StatusOK {
			f.t.Fatalf("poll %s = %d %v", id, status, body)
		}
		state := field(f.t, body, "data", "status").(string)
		seen = append(seen, state)
		if state == statusSuccess || state == statusFailed {
			return seen, body
		}
	}
	f.t.Fatalf("task %s never finished: %v", id, seen)
	return nil, nil
}

func (f *fixture) download(url string) (int, string, []byte) {
	f.t.Helper()
	response, err := http.Get(url)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header.Get("Content-Type"), raw
}

func (f *fixture) outputURL(body map[string]any, name string) string {
	f.t.Helper()
	return field(f.t, body, "data", "output", name).(string)
}

const key = "test-key"

func textBody() map[string]any {
	return map[string]any{"model": "v3.1-20260211", "prompt": "a robot"}
}

// deliveryErr runs Plinth's delivery check over fetched bytes.
func deliveryErr(t *testing.T, data []byte, role, variant, mediaType string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return local.ValidateDeliveredFile(file, role, variant, mediaType)
}

func TestWorkflowChainSucceedsAndDeliversValidFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	statuses, done := f.finish(gen, key)
	if strings.Join(statuses, ",") != "queued,running,success" {
		t.Fatalf("flow = %v", statuses)
	}
	for _, name := range []string{"model_url", "rendered_image_url", "generated_image_url"} {
		status, contentType, data := f.download(f.outputURL(done, name))
		if status != http.StatusOK || contentType != ArtifactContentType || len(data) == 0 {
			t.Fatalf("%s: %d %q %d bytes", name, status, contentType, len(data))
		}
	}
	_, _, glb := f.download(f.outputURL(done, "model_url"))
	if err := deliveryErr(t, glb, "model", "glb", "model/gltf-binary"); err != nil {
		t.Fatalf("generated GLB rejected: %v", err)
	}

	check := f.create("/animations/rig-check", map[string]any{"input": gen}, key)
	if _, body := f.finish(check, key); field(t, body, "data", "output", "riggable") != true {
		t.Fatalf("rig check: %v", body)
	}
	rig := f.create("/animations/rig", map[string]any{"input": gen, "model": "v3.0-20260909", "out_format": "glb", "rig_type": "biped", "spec": "tripo"}, key)
	_, rigDone := f.finish(rig, key)
	retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk", "out_format": "glb"}, key)
	if _, body := f.finish(retarget, key); field(t, body, "data", "status") != statusSuccess {
		t.Fatalf("retarget: %v", body)
	}
	_ = rigDone

	segment := f.create("/mesh/segment", map[string]any{"input": gen, "model": "v2.0-20260430"}, key)
	_, segDone := f.finish(segment, key)
	complete := f.create("/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506"}, key)
	f.finish(complete, key)

	formats := []struct{ format, variant, media string }{
		{"FBX", "fbx", "model/fbx"}, {"OBJ", "obj", "application/zip"}, {"STL", "stl", "model/stl"},
		{"USDZ", "usdz", "model/vnd.usdz+zip"}, {"3MF", "3mf", "model/3mf"}, {"GLTF", "gltf", "model/gltf-binary"},
	}
	for _, c := range formats {
		convert := f.create("/models/convert", map[string]any{"input": f.outputURL(segDone, "model_url"), "format": c.format}, key)
		_, body := f.finish(convert, key)
		status, contentType, data := f.download(f.outputURL(body, "model_url"))
		if status != http.StatusOK || contentType != ArtifactContentType {
			t.Fatalf("%s: %d %q", c.format, status, contentType)
		}
		if err := deliveryErr(t, data, "converted_model", c.variant, c.media); err != nil {
			t.Errorf("%s export rejected by the delivery check: %v", c.format, err)
		}
	}
}

// #293: Tripo returns the GLTF export as a binary GLB. The dev-test check of
// 2026-11-12 read the converted.gltf file and the task's model (which is that
// file) as the same bytes, and the model was a GLB. The fake used to deliver a glTF
// JSON here, which hid that the two slots of a Plinth task hold one file.
func TestGltfExportIsTheBinaryGlbTheGenerationDelivers(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	_, done := f.finish(gen, key)
	_, _, model := f.download(f.outputURL(done, "model_url"))

	convert := f.create("/models/convert", map[string]any{"input": f.outputURL(done, "model_url"), "format": "GLTF"}, key)
	_, body := f.finish(convert, key)
	exportURL := f.outputURL(body, "model_url")
	// Plinth keys the media type on the extension of the URL: a .glb is model/gltf-binary.
	if !strings.HasSuffix(strings.Split(exportURL, "?")[0], "/model.glb") {
		t.Fatalf("the GLTF export is delivered as %q, want a model.glb", exportURL)
	}
	status, contentType, data := f.download(exportURL)
	if status != http.StatusOK || contentType != ArtifactContentType {
		t.Fatalf("GLTF export: %d %q", status, contentType)
	}
	if err := deliveryErr(t, data, "converted_model", "converted.gltf", "model/gltf-binary"); err != nil {
		t.Fatalf("the GLTF export is not a GLB the delivery check accepts: %v", err)
	}
	if !bytes.Equal(data, model) {
		t.Fatalf("the GLTF export differs from the model GLB: %d and %d bytes", len(data), len(model))
	}
}

// The convert request is checked against the parameters of the official page
// (convert.go): a wrong type or value is the real 400/code 1004 answer, a valid
// body is accepted with every documented option.
func TestConvertRequestIsCheckedAgainstTheOfficialParameters(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	valid := func(changes map[string]any) map[string]any {
		body := map[string]any{"input": "https://example.test/x.glb", "format": "FBX"}
		for name, value := range changes {
			body[name] = value
		}
		return body
	}
	for name, body := range map[string]map[string]any{
		"GLB is not a target":           valid(map[string]any{"format": "GLB"}),
		"lower case format":             valid(map[string]any{"format": "fbx"}),
		"unknown texture format":        valid(map[string]any{"texture_format": "GIF"}),
		"texture size as text":          valid(map[string]any{"texture_size": "8192"}),
		"fractional texture size":       valid(map[string]any{"texture_size": 8192.5}),
		"pivot as text":                 valid(map[string]any{"pivot_to_center_bottom": "true"}),
		"unknown fbx preset":            valid(map[string]any{"fbx_preset": "unity"}),
		"unknown orientation":           valid(map[string]any{"export_orientation": "up"}),
		"part names as text":            valid(map[string]any{"part_names": "head"}),
		"packaging outside FBX":         valid(map[string]any{"format": "STL", "texture_packaging": "zip"}),
		"unknown packaging":             valid(map[string]any{"texture_packaging": "folder"}),
		"face limit as a boolean":       valid(map[string]any{"face_limit": true}),
		"scale factor as text":          valid(map[string]any{"scale_factor": "1"}),
		"quad as a number":              valid(map[string]any{"quad": 1}),
		"flatten threshold as a string": valid(map[string]any{"flatten_bottom_threshold": "0.01"}),
	} {
		t.Run(name, func(t *testing.T) {
			status, _, response := f.do("POST", "/models/convert", key, body)
			if status != http.StatusBadRequest || field(t, response, "code").(float64) != 1004 {
				t.Fatalf("status %d body %v", status, response)
			}
		})
	}
	every := valid(map[string]any{
		"quad": false, "force_symmetry": false, "face_limit": 1000, "flatten_bottom": false, "flatten_bottom_threshold": 0.01,
		"texture_size": 8192, "texture_format": "PNG", "bake": true, "pack_uv": false, "export_vertex_colors": false,
		"pivot_to_center_bottom": true, "scale_factor": 1.5, "with_animation": false, "animate_in_place": false,
		"part_names": []string{"a"}, "export_orientation": "+x", "fbx_preset": "mixamo", "texture_packaging": "embedded",
	})
	if status, _, response := f.do("POST", "/models/convert", key, every); status != http.StatusOK {
		t.Fatalf("every documented option = %d %v", status, response)
	}
}

// A convert task shows the parameters it ran (the committed sample), and the
// exported FBX carries the texture in the requested format; without a request
// the official default, JPEG, applies. quad forces the FBX output.
func TestConvertEchoesItsRequestAndDeliversTheRequestedTexture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	run := func(changes map[string]any) (map[string]any, []byte, string) {
		body := map[string]any{"input": "https://example.test/x.glb", "format": "FBX"}
		for name, value := range changes {
			body[name] = value
		}
		_, done := f.finish(f.create("/models/convert", body, key), key)
		url := f.outputURL(done, "model_url")
		status, _, data := f.download(url)
		if status != http.StatusOK {
			t.Fatalf("download %s = %d", url, status)
		}
		return field(t, done, "data", "input").(map[string]any), data, url
	}

	input, data, _ := run(map[string]any{
		"texture_format": "PNG", "texture_size": 8192, "pivot_to_center_bottom": true, "fbx_preset": "3dsmax",
	})
	for name, want := range map[string]any{
		"format": "FBX", "texture_format": "PNG", "texture_size": float64(8192), "pivot_to_center_bottom": true, "fbx_preset": "3dsmax",
	} {
		if input[name] != want {
			t.Errorf("input.%s = %v, want %v (input %v)", name, input[name], want, input)
		}
	}
	if _, echoedSource := input["input"]; echoedSource {
		t.Errorf("the source URL is shown under original_model_id, not input: %v", input)
	}
	if !bytes.Contains(data, []byte("\x89PNG\r\n\x1a\n")) || bytes.Contains(data, []byte("\xff\xd8\xff")) {
		t.Fatal("a PNG texture request did not deliver an FBX with a PNG texture")
	}
	if err := deliveryErr(t, data, "converted_model", "fbx", "model/fbx"); err != nil {
		t.Fatalf("textured FBX rejected by the delivery check: %v", err)
	}

	_, data, _ = run(nil)
	if !bytes.Contains(data, []byte("\xff\xd8\xff")) || bytes.Contains(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("a request without texture_format must deliver the official default, a JPEG texture")
	}

	_, data, url := run(map[string]any{"format": "STL", "quad": true})
	if !strings.HasSuffix(strings.Split(url, "?")[0], ".fbx") {
		t.Fatalf("quad must force the FBX output, got %s", url)
	}
	if err := deliveryErr(t, data, "converted_model", "fbx", "model/fbx"); err != nil {
		t.Fatalf("quad FBX export rejected: %v", err)
	}
}

func TestRetargetOfAV2RigFailsLikeTheOfficialAPI(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.create("/generation/text-to-model", textBody(), key)
	rig := f.create("/animations/rig", map[string]any{"input": gen, "model": "v2.5-20260210"}, key)
	f.finish(rig, key)
	retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, key)
	statuses, body := f.finish(retarget, key)
	if statuses[len(statuses)-1] != statusFailed || field(t, body, "data", "error_code").(float64) != 1004 {
		t.Fatalf("flow %v body %v", statuses, body)
	}
}

func TestRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		method, path, key string
		body              any
		status            int
		code              float64
	}{
		"no key balance":    {"GET", "/account/balance", "", nil, http.StatusUnauthorized, 2},
		"no key create":     {"POST", "/generation/text-to-model", "", textBody(), http.StatusUnauthorized, 2},
		"empty body":        {"POST", "/generation/text-to-model", key, map[string]any{}, http.StatusBadRequest, 1004},
		"missing prompt":    {"POST", "/generation/text-to-model", key, map[string]any{"model": "m"}, http.StatusBadRequest, 1004},
		"unknown task":      {"GET", "/tasks/00000000-0000-0000-0000-000000000000", key, nil, http.StatusNotFound, 2001},
		"unknown source":    {"POST", "/animations/rig-check", key, map[string]any{"input": "deadbeef-0000-4000-8000-000000000042"}, http.StatusNotFound, 2001},
		"rig of a rig":      {"POST", "/animations/rig", key, nil, http.StatusBadRequest, 1004},
		"convert no format": {"POST", "/models/convert", key, map[string]any{"input": "https://example.test/x.glb"}, http.StatusBadRequest, 1004},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.do(tc.method, tc.path, tc.key, tc.body)
			if status != tc.status || field(t, body, "code").(float64) != tc.code {
				t.Fatalf("status %d body %v", status, body)
			}
			if field(t, body, "status") != "error" {
				t.Fatalf("error envelope lacks status=error: %v", body)
			}
		})
	}
	status, _, body := f.do("GET", "/account/balance", key, nil)
	if status != http.StatusOK || field(t, body, "data", "balance").(float64) <= 0 {
		t.Fatalf("balance = %d %v", status, body)
	}
	// Retargeting a model that is not a rig is refused.
	gen := f.create("/generation/text-to-model", textBody(), key)
	if status, _, _ := f.do("POST", "/animations/retarget", key, map[string]any{"input": gen, "animation": "preset:walk"}); status != http.StatusBadRequest {
		t.Fatalf("retarget of a plain model = %d", status)
	}
}

func TestUploadReturnsAFileToken(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.upload("a.png", "image/png", []byte("png"), key)
	if status != http.StatusOK || !strings.HasPrefix(field(t, body, "data", "file_token").(string), "file_") {
		t.Fatalf("upload = %d %v", status, body)
	}
	if status, _, _ := f.do("POST", "/files", key, map[string]any{}); status != http.StatusBadRequest {
		t.Fatalf("JSON upload = %d", status)
	}
}

// The upload endpoint takes the documented picture and model types and refuses
// an empty part (2003) or an undocumented type (2004); both refusals are
// constructed from the files and error-handling pages because the samples only
// hold a success.
func TestUploadValidatesEmptyAndUnsupportedFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		filename, mediaType string
		data                []byte
		status              int
		code                float64
	}{
		"empty picture":  {"a.png", "image/png", nil, http.StatusBadRequest, 2003},
		"empty model":    {"a.glb", "model/gltf-binary", nil, http.StatusBadRequest, 2003},
		"text file":      {"notes.txt", "text/plain", []byte("x"), http.StatusBadRequest, 2004},
		"unknown binary": {"a.exe", "application/octet-stream", []byte("x"), http.StatusBadRequest, 2004},
		"png picture":    {"a.png", "image/png", []byte("png"), http.StatusOK, 0},
		"webp picture":   {"a.webp", "image/webp", []byte("webp"), http.StatusOK, 0},
		"glb model":      {"a.glb", "model/gltf-binary", []byte("glb"), http.StatusOK, 0},
		"obj model":      {"a.obj", "application/octet-stream", []byte("obj"), http.StatusOK, 0},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.upload(tc.filename, tc.mediaType, tc.data, key)
			if status != tc.status {
				t.Fatalf("upload = %d %v", status, body)
			}
			if tc.status != http.StatusOK {
				if field(t, body, "code").(float64) != tc.code || field(t, body, "status") != "error" {
					t.Fatalf("refusal = %v", body)
				}
			}
		})
	}
}

// An upload consumes no credits: the credit scenarios answer task creation but
// leave the upload on the same key untouched, while request-level failures
// still apply.
func TestUploadIgnoresCreditScenarios(t *testing.T) {
	for _, scenarioKey := range []string{"fake-insufficient-balance", "fake-key-quota"} {
		t.Run(scenarioKey, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			status, _, body := f.upload("a.png", "image/png", []byte("png"), scenarioKey)
			if status != http.StatusOK || !strings.HasPrefix(field(t, body, "data", "file_token").(string), "file_") {
				t.Fatalf("upload = %d %v", status, body)
			}
			status, _, createBody := f.do("POST", "/generation/text-to-model", scenarioKey, textBody())
			if status != http.StatusForbidden || field(t, createBody, "code").(float64) != 2010 {
				t.Fatalf("create = %d %v", status, createBody)
			}
		})
	}
	f := newFixture(t, fakeupstream.Config{})
	if status, _, body := f.upload("a.png", "image/png", []byte("png"), "fake-rate-limit"); status != http.StatusTooManyRequests || field(t, body, "code").(float64) != 1007 {
		t.Fatalf("rate-limited upload = %d %v", status, body)
	}
}

// Every kind answers with its own task type and delivers its output files: the
// seven endpoints added in #89 included. Nested multiview URLs, the splat and the
// Smart Segmentation outputs are fetched and checked by Plinth's delivery check.
func TestEveryKindCreatesATaskOfItsTypeAndDeliversItsFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/text-to-model", textBody(), key)
	f.finish(source, key)
	for _, tc := range []struct {
		path, typ string
		body      map[string]any
		// fetch lists output field paths with the delivery check each must pass.
		fetch []delivery
	}{
		{"/generation/image-to-model", "image_to_model", map[string]any{"model": "v3.1-20260211", "input": "file_x"},
			[]delivery{{[]any{"model_url"}, "model", "glb", "model/gltf-binary"}, {[]any{"rendered_image_url"}, "preview", "", "image/png"}}},
		{"/generation/image-to-splat", "image_to_splat", map[string]any{"input": "file_x", "model_seed": 42},
			[]delivery{{[]any{"model_url"}, "gaussian-splat", "splat", "application/vnd.art-ai.gaussian-splat"}}},
		{"/generation/image-to-multiview", "image_to_multiview", map[string]any{"input": "file_x"}, multiviewChecks("front", "left", "back", "right")},
		{"/generation/edit-multiview", "edit_multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": "green"}}},
			multiviewChecks("front")},
		{"/models/texture", "texture_model", map[string]any{"input": source, "model": "v3.5-20260815"},
			[]delivery{{[]any{"model_url"}, "model", "glb", "model/gltf-binary"}}},
		{"/mesh/decimate", "highpoly_to_lowpoly", map[string]any{"input": source, "model": "v3.0"},
			[]delivery{{[]any{"model_url"}, "model", "glb", "model/gltf-binary"}}},
		{"/mesh/smartsegment", "smartsegment_image", map[string]any{"input": "file_x", "seg_type": "image"},
			[]delivery{{[]any{"seg_model_url"}, "model", "glb", "model/gltf-binary"}, {[]any{"mask_url"}, "image", "", "image/png"}}},
		{"/mesh/smartsegment", "smartsegment_model", map[string]any{"input": "file_x", "seg_type": "model"},
			[]delivery{{[]any{"seg_model_url"}, "model", "glb", "model/gltf-binary"}}},
	} {
		id := f.create(tc.path, tc.body, key)
		statuses, done := f.finish(id, key)
		if statuses[len(statuses)-1] != statusSuccess || field(t, done, "data", "type") != tc.typ {
			t.Errorf("%s: %v %v", tc.path, statuses, done)
			continue
		}
		for _, check := range tc.fetch {
			url, _ := field(t, done, append([]any{"data", "output"}, check.path...)...).(string)
			status, _, data := f.download(url)
			if status != http.StatusOK {
				t.Errorf("%s %v: download %d", tc.path, check.path, status)
				continue
			}
			if check.role == "preview" {
				continue // the render preview is a picture; its bytes are checked by the image tests
			}
			var err error
			if check.role == "gaussian-splat" {
				err = splat.ValidateReader(bytes.NewReader(data), int64(len(data))) // the splat profile has its own validator
			} else {
				err = deliveryErr(t, data, check.role, check.variant, check.mediaType)
			}
			if err != nil {
				t.Errorf("%s %v fails the delivery check: %v", tc.path, check.path, err)
			}
		}
	}
	// A multiview task offers exactly the views its sample shows, nested in generate_multiview_image.
	id := f.create("/generation/image-to-multiview", map[string]any{"input": "file_x"}, key)
	_, done := f.finish(id, key)
	views, _ := field(t, done, "data", "output", "generate_multiview_image").(map[string]any)
	if len(views) != 4 {
		t.Errorf("multiview output = %v, want the four view URLs", views)
	}
	// Smart Segmentation refuses a seg_type it does not know.
	if status, _, _ := f.do("POST", "/mesh/smartsegment", key, map[string]any{"input": "file_x", "seg_type": "video"}); status != http.StatusBadRequest {
		t.Errorf("unknown seg_type = %d", status)
	}
}

// An edit returns exactly the views it was asked to edit, whichever they are (the
// captured sample asked for the front view only).
func TestEditMultiviewReturnsTheRequestedViews(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.create("/generation/image-to-multiview", map[string]any{"input": "file_x"}, key)
	f.finish(source, key)
	for _, views := range [][]string{{"front"}, {"left"}, {"back", "right"}, {"right", "front", "left", "back"}} {
		var prompts []map[string]any
		for _, view := range views {
			prompts = append(prompts, map[string]any{"view": view, "prompt": "make it green"})
		}
		id := f.create("/generation/edit-multiview", map[string]any{"input": source, "prompts": prompts}, key)
		_, done := f.finish(id, key)
		got, _ := field(t, done, "data", "output", "generate_multiview_image").(map[string]any)
		if len(got) != len(views) {
			t.Errorf("views %v: output = %v", views, got)
		}
		for _, view := range views {
			url, _ := got[view+"_view_url"].(string)
			if status, contentType, data := f.download(url); status != http.StatusOK || contentType != "image/jpeg" || deliveryErr(t, data, "image", view, "image/jpeg") != nil {
				t.Errorf("view %s: download %d %s", view, status, contentType)
			}
		}
	}
	if status, _, _ := f.do("POST", "/generation/edit-multiview", key, map[string]any{"input": source, "prompts": []map[string]any{{"view": "top", "prompt": "x"}}}); status != http.StatusBadRequest {
		t.Errorf("an unknown view = %d", status)
	}
}

type delivery struct {
	path                     []any
	role, variant, mediaType string
}

func multiviewChecks(views ...string) []delivery {
	var checks []delivery
	for _, view := range views {
		checks = append(checks, delivery{[]any{"generate_multiview_image", view + "_view_url"}, "image", view, "image/jpeg"})
	}
	return checks
}

func TestPollCountsAreConfigurable(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{QueuedPolls: 2, RunningPolls: 3})
	id := f.create("/generation/text-to-model", textBody(), key)
	statuses, _ := f.finish(id, key)
	if got := strings.Join(statuses, ","); got != "queued,queued,running,running,running,success" {
		t.Fatalf("flow = %s", got)
	}
}

// Every scenario has a regression: the exact status, error code, header or
// behavior the Plinth Adapter keys on.
func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	post := func(f *fixture, scenario string) (int, http.Header, map[string]any) {
		return f.do("POST", "/generation/text-to-model", key, textBody(), "X-Fake-Scenario", scenario)
	}
	t.Run("insufficient balance", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "insufficient-balance")
		if status != http.StatusForbidden || field(t, body, "code").(float64) != 2010 {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("key quota", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "key-quota")
		if status != http.StatusForbidden || field(t, body, "code").(float64) != 2010 || !strings.Contains(field(t, body, "message").(string), "credit limit") {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("rate limit is code 1007 with the rate headers", func(t *testing.T) {
		status, header, body := post(newFixture(t, short), "rate-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" || field(t, body, "code").(float64) != 1007 {
			t.Fatalf("%d %v %v", status, header, body)
		}
		if header.Get("X-RateLimit-Limit") == "" || header.Get("X-RateLimit-Remaining") == "" || header.Get("X-RateLimit-Reset") == "" {
			t.Fatalf("rate headers = %v", header)
		}
	})
	t.Run("concurrency limit is code 2000 with Retry-After and creation only", func(t *testing.T) {
		f := newFixture(t, short)
		status, header, body := post(f, "concurrency-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" || field(t, body, "code").(float64) != 2000 ||
			!strings.Contains(field(t, body, "message").(string), "limit of generation") {
			t.Fatalf("%d %v %v", status, header, body)
		}
		// The pool is full for task creation; an upload is not a task.
		if status, _, uploadBody := f.upload("a.png", "image/png", []byte("png"), "fake-concurrency-limit"); status != http.StatusOK {
			t.Fatalf("upload under a full pool = %d %v", status, uploadBody)
		}
		// Polling and the balance read are not task creation either.
		if status, _, _ := f.do("GET", "/account/balance", "fake-concurrency-limit", nil); status != http.StatusOK {
			t.Fatalf("balance under a full pool = %d", status)
		}
	})
	t.Run("server error", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "server-error")
		if status != http.StatusInternalServerError || field(t, body, "code").(float64) != 1000 {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("rate limit then recovery", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, _ := f.do("POST", "/generation/text-to-model", key, textBody(), "X-Fake-Scenario", "rate-limit", "X-Fake-Fail-Times", "1"); status != http.StatusTooManyRequests {
			t.Fatalf("first = %d", status)
		}
		if status, _, _ := f.do("POST", "/generation/text-to-model", key, textBody(), "X-Fake-Scenario", "rate-limit", "X-Fake-Fail-Times", "1"); status != http.StatusOK {
			t.Fatalf("second = %d", status)
		}
	})
	t.Run("timeout drops the connection", func(t *testing.T) {
		f := newFixture(t, short)
		request, _ := http.NewRequest(http.MethodPost, f.base()+"/generation/text-to-model", strings.NewReader(`{"model":"m","prompt":"p"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "timeout")
		start := time.Now()
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		if time.Since(start) < 40*time.Millisecond {
			t.Fatalf("connection dropped after %v, before the hold", time.Since(start))
		}
	})
	t.Run("lost response keeps the task", func(t *testing.T) {
		f := newFixture(t, short)
		request, _ := http.NewRequest(http.MethodPost, f.base()+"/generation/text-to-model", strings.NewReader(`{"model":"m","prompt":"p"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		// The work exists upstream although the caller never saw its id: the
		// next created task has the following id.
		next := f.create("/generation/text-to-model", textBody(), key)
		lostID := strings.TrimSuffix(next, next[len(next)-1:]) + "1"
		if status, _, body := f.do("GET", "/tasks/"+lostID, key, nil); status != http.StatusOK {
			t.Fatalf("lost task %s = %d %v", lostID, status, body)
		}
	})
	t.Run("task failed", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/generation/text-to-model", textBody(), key, "X-Fake-Scenario", "task-failed")
		statuses, body := f.finish(id, key)
		if statuses[len(statuses)-1] != statusFailed || field(t, body, "data", "error_code").(float64) == 0 || field(t, body, "data", "error_message") == "" {
			t.Fatalf("%v %v", statuses, body)
		}
	})
	t.Run("artifact 404", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/generation/text-to-model", textBody(), "fake-artifact-404-key")
		_, body := f.finish(id, "fake-artifact-404-key")
		if status, _, _ := f.download(f.outputURL(body, "model_url")); status != http.StatusNotFound {
			t.Fatalf("artifact = %d", status)
		}
	})
	t.Run("artifact corrupt", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/generation/text-to-model", textBody(), "fake-artifact-corrupt")
		_, body := f.finish(id, "fake-artifact-corrupt")
		status, _, data := f.download(f.outputURL(body, "model_url"))
		if status != http.StatusOK {
			t.Fatalf("artifact = %d", status)
		}
		if deliveryErr(t, data, "model", "glb", "model/gltf-binary") == nil {
			t.Fatal("corrupt bytes passed the delivery check")
		}
	})
	t.Run("artifact URL expired, a later read hands out working URLs", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/generation/text-to-model", textBody(), "fake-artifact-expired")
		_, body := f.finish(id, "fake-artifact-expired")
		status, contentType, raw := f.download(f.outputURL(body, "model_url"))
		if status != http.StatusForbidden || !strings.Contains(contentType, "xml") || !strings.Contains(string(raw), "<Code>AccessDenied</Code>") {
			t.Fatalf("expired URL = %d %s %s", status, contentType, raw)
		}
		// Reading the task again (Plinth's free result refetch) issues fresh URLs.
		_, _, again := f.do("GET", "/tasks/"+id, "fake-artifact-expired", nil)
		status, _, data := f.download(f.outputURL(again, "model_url"))
		if status != http.StatusOK || deliveryErr(t, data, "model", "glb", "model/gltf-binary") != nil {
			t.Fatalf("refetched URL = %d", status)
		}
		// Tasks created without the scenario never carry the issue parameter.
		plain := f.create("/generation/text-to-model", textBody(), key)
		_, plainBody := f.finish(plain, key)
		if strings.Contains(f.outputURL(plainBody, "model_url"), fakeupstream.IssueParam) {
			t.Fatal("a normal task's URL carries the expiry parameter")
		}
	})
	t.Run("key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, _ := f.do("POST", "/generation/text-to-model", "fake-insufficient-balance-x", textBody()); status != http.StatusForbidden {
			t.Fatalf("status = %d", status)
		}
	})
}

// The official task-query page lists cancelled and says an unknown value may
// occur; neither was captured. The fake injects both from the real failed poll
// structure, without the failure fields (doc.go), through the shared
// task-canceled scenario and the Tripo-local task-unknown word.
func TestTaskTerminalStatusScenarios(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		key     string
		headers []string
		want    string
	}{
		"cancelled by header": {key, []string{"X-Fake-Scenario", "task-canceled"}, statusCancelled},
		"cancelled by key":    {"fake-task-canceled", nil, statusCancelled},
		"unknown by header":   {key, []string{"X-Fake-Scenario", "task-unknown"}, statusUnknown},
		"unknown by key":      {"fake-task-unknown", nil, statusUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			id := f.create("/generation/text-to-model", textBody(), tc.key, tc.headers...)
			var data map[string]any
			for i := 0; i < 3; i++ {
				status, _, body := f.do("GET", "/tasks/"+id, tc.key, nil)
				if status != http.StatusOK {
					t.Fatalf("poll = %d %v", status, body)
				}
				data, _ = fakeupstream.DigMap(body, "data")
			}
			if data["status"] != tc.want || data["type"] != "text_to_model" {
				t.Fatalf("terminal poll = %v", data)
			}
			if _, has := data["error_code"]; has {
				t.Fatalf("a %s task carries error_code: %v", tc.want, data)
			}
			if _, has := data["error_message"]; has {
				t.Fatalf("a %s task carries error_message: %v", tc.want, data)
			}
			// unknown is not a signal and cancelled is terminal: reading again
			// answers the same status instead of moving to success.
			_, _, again := f.do("GET", "/tasks/"+id, tc.key, nil)
			if got := field(t, again, "data", "status"); got != tc.want {
				t.Fatalf("second read = %v, want %s", got, tc.want)
			}
		})
	}
}

// A body that is not JSON is 1003; an empty body or a missing parameter keeps
// 1004. An input that names an unknown task is 2001; a file token or a public
// URL passes through.
func TestCreateReportsMalformedBodiesUnknownTasksAndPassThroughInputs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		raw  string
		code float64
	}{
		"broken JSON":    {`{"model":`, 1003},
		"array body":     {`[]`, 1003},
		"null body":      {`null`, 1003},
		"empty body":     {"", 1004},
		"missing params": {`{}`, 1004},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.doRaw("POST", "/generation/text-to-model", key, tc.raw)
			if status != http.StatusBadRequest || field(t, body, "code").(float64) != tc.code || field(t, body, "status") != "error" {
				t.Fatalf("status %d body %v", status, body)
			}
			if tc.code == 1003 && field(t, body, "message") != "The request body is malformed" {
				t.Fatalf("message = %v", field(t, body, "message"))
			}
		})
	}
	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/animations/rig-check", map[string]any{"input": "123e4567-e89b-42d3-a456-426614174000"}},
		{"/models/texture", map[string]any{"input": "not-a-fake-task"}},
	} {
		status, _, body := f.do("POST", tc.path, key, tc.body)
		if status != http.StatusNotFound || field(t, body, "code").(float64) != 2001 {
			t.Fatalf("%s unknown input = %d %v", tc.path, status, body)
		}
	}
	for name, tc := range map[string]struct {
		path string
		body map[string]any
	}{
		"file token": {"/generation/image-to-model", map[string]any{"model": "v3.1-20260211", "input": "file_x"}},
		"public URL": {"/models/texture", map[string]any{"input": "https://example.test/model.glb"}},
	} {
		if status, _, body := f.do("POST", tc.path, key, tc.body); status != http.StatusOK {
			t.Fatalf("%s %s = %d %v", name, tc.path, status, body)
		}
	}
}

// multiview-to-model is constructed from the documentation: inputs is a list of
// single-key view objects with a required front, and the legacy input is refused.
func TestMultiviewToModelValidatesInputsAndFinishes(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, body := range map[string]map[string]any{
		"legacy input":  {"model": "v3.1-20260211", "input": "file_x"},
		"one view":      {"model": "v3.1-20260211", "inputs": []any{map[string]any{"front": "t1"}}},
		"missing front": {"model": "v3.1-20260211", "inputs": []any{map[string]any{"left": "t1"}, map[string]any{"back": "t2"}}},
		"duplicate":     {"model": "v3.1-20260211", "inputs": []any{map[string]any{"front": "t1"}, map[string]any{"front": "t2"}}},
		"two keys":      {"model": "v3.1-20260211", "inputs": []any{map[string]any{"front": "t1", "left": "t2"}, map[string]any{"back": "t3"}}},
	} {
		if status, _, _ := f.do(http.MethodPost, "/generation/multiview-to-model", key, body); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, status)
		}
	}
	id := f.create("/generation/multiview-to-model", map[string]any{
		"model":  "v3.1-20260211",
		"inputs": []any{map[string]any{"front": "t1"}, map[string]any{"right": "t2"}},
	}, key)
	_, body := f.finish(id, key)
	if got := field(t, body, "data", "type"); got != "multiview_to_model" {
		t.Fatalf("type = %v", got)
	}
}
