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
		{"USDZ", "usdz", "model/vnd.usdz+zip"}, {"3MF", "3mf", "model/3mf"}, {"GLTF", "gltf", "model/gltf+json"},
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
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="a.png"`)
	header.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(header)
	_, _ = part.Write([]byte("png"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, f.base()+"/files", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(field(t, body, "data", "file_token").(string), "file_") {
		t.Fatalf("upload = %d %v", response.StatusCode, body)
	}
	if status, _, _ := f.do("POST", "/files", key, map[string]any{}); status != http.StatusBadRequest {
		t.Fatalf("JSON upload = %d", status)
	}
}

func TestConstructedEndpointsCreateTasks(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, k := range kinds {
		if k.Real {
			continue
		}
		body := map[string]any{"model": "m", "input": "file_x", "prompt": "p"}
		id := f.create(k.Path, body, key)
		statuses, done := f.finish(id, key)
		if statuses[len(statuses)-1] != statusSuccess || field(t, done, "data", "type") != k.Type {
			t.Errorf("%s: %v %v", k.Path, statuses, done)
		}
	}
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
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		status, header, body := post(newFixture(t, short), "rate-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" || field(t, body, "code").(float64) != 2000 {
			t.Fatalf("%d %v %v", status, header, body)
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
	t.Run("key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, _ := f.do("POST", "/generation/text-to-model", "fake-insufficient-balance-x", textBody()); status != http.StatusForbidden {
			t.Fatalf("status = %d", status)
		}
	})
}
