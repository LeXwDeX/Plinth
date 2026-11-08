package meshyopenapiv1

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

const key = "test-key"

type fixture struct {
	*testkit.Fixture
}

func newFixture(t *testing.T, config fakeupstream.Config) *fixture {
	t.Helper()
	return &fixture{testkit.New(t, config, Version(), meshyopenapiv2.Version())}
}

func (f *fixture) v1(path string) string { return Prefix + path }

// create submits a task and returns its id.
func (f *fixture) create(path string, body map[string]any, headers ...string) string {
	f.T.Helper()
	status, _, response := f.Do("POST", f.v1(path), key, body, headers...)
	if status != http.StatusAccepted {
		f.T.Fatalf("POST %s = %d %v", path, status, response)
	}
	return testkit.Field(f.T, response, "result").(string)
}

// finish polls until the task ends and returns the statuses seen and the last
// body.
func (f *fixture) finish(path, id, apiKey string) ([]string, map[string]any) {
	f.T.Helper()
	var seen []string
	for i := 0; i < 8; i++ {
		status, _, body := f.Do("GET", f.v1(path+"/"+id), apiKey, nil)
		if status != http.StatusOK {
			f.T.Fatalf("poll %s = %d %v", id, status, body)
		}
		state := testkit.Field(f.T, body, "status").(string)
		seen = append(seen, state)
		if state == core.StatusSucceeded || state == core.StatusFailed || state == core.StatusCanceled {
			return seen, body
		}
	}
	f.T.Fatalf("task %s never finished: %v", id, seen)
	return nil, nil
}

// urls returns every artifact URL (field path to URL) of a task body.
func urls(value any, path string, out map[string]string) map[string]string {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "http") {
			out[path] = typed
		}
	case map[string]any:
		for k, child := range typed {
			urls(child, path+"."+k, out)
		}
	case []any:
		for i, child := range typed {
			urls(child, path+"."+string(rune('0'+i)), out)
		}
	}
	return out
}

// source makes a finished image-to-3d task, the input of the processing kinds.
func (f *fixture) source() string {
	id := f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "target_polycount": 10000})
	f.finish("/image-to-3d", id, key)
	return id
}

func TestEveryKindCompletesAndDeliversItsFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	rig := f.create("/rigging", map[string]any{"input_task_id": source})
	f.finish("/rigging", rig, key)
	requests := map[string]map[string]any{
		"/text-to-image":     {"prompt": "p"},
		"/image-to-image":    {"prompt": "p", "reference_image_urls": []string{"https://example.test/a.png"}},
		"/image-to-3d":       {"image_url": "https://example.test/a.png", "target_formats": []string{"glb", "obj", "stl", "usdz", "fbx", "3mf"}},
		"/multi-image-to-3d": {"image_urls": []string{"https://example.test/a.png"}},
		"/remesh":            {"input_task_id": source, "target_formats": []string{"glb", "fbx"}},
		"/convert":           {"input_task_id": source, "target_formats": []string{"glb", "stl"}},
		"/retexture":         {"input_task_id": source, "text_style_prompt": "p"},
		"/rigging":           {"model_url": "data:application/octet-stream;base64,AAAA"},
		"/animations":        {"rig_task_id": rig, "action_id": 0},
		"/uv-unwrap":         {"model_url": "https://example.test/a.glb"},
		"/resize":            {"input_task_id": source, "resize_height": 1.7},
		"/print/analyze":     {"input_task_id": source},
		"/print/multi-color": {"input_task_id": source},
		"/text-to-motion":    {"prompt": "p", "duration": 2},
	}
	if len(requests) != len(kinds) {
		t.Fatalf("test covers %d kinds, the version has %d", len(requests), len(kinds))
	}
	for _, kind := range kinds {
		t.Run(kind.Path, func(t *testing.T) {
			id := f.create(kind.Path, requests[kind.Path])
			statuses, body := f.finish(kind.Path, id, key)
			if strings.Join(statuses, ",") != "PENDING,IN_PROGRESS,SUCCEEDED" {
				t.Fatalf("flow = %v", statuses)
			}
			if body["type"] != kind.Type || body["id"] != id {
				t.Fatalf("type/id = %v %v", body["type"], body["id"])
			}
			for field, url := range urls(body, "", map[string]string{}) {
				status, contentType, data := f.Download(url)
				ext := url[strings.LastIndex(url, "."):]
				if status != http.StatusOK || contentType != ContentTypes[ext] || len(data) == 0 {
					t.Errorf("%s %s: %d %q %d bytes", field, url, status, contentType, len(data))
				}
				if ext == ".glb" {
					if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
						t.Errorf("%s: GLB rejected by the delivery check: %v", field, err)
					}
				}
			}
		})
	}
}

func TestTargetFormatsDecideTheModelURLs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/remesh", map[string]any{"input_task_id": f.source(), "target_formats": []string{"obj", "usdz"}})
	_, body := f.finish("/remesh", id, key)
	models, _ := body["model_urls"].(map[string]any)
	var got []string
	for format := range models {
		got = append(got, format)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "mtl,obj,usdz" {
		t.Fatalf("model_urls = %v", got)
	}
}

// The partial-formats scenario leaves STL and 3MF out of a retexture result: the
// starting point of the missing-format completion (D class). Other kinds keep all
// their formats.
func TestPartialFormatsScenarioOmitsOptionalFormatsOfARetexture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	formats := []string{"glb", "obj", "stl", "3mf"}
	id := f.create("/retexture", map[string]any{"input_task_id": f.source(), "target_formats": formats}, "X-Fake-Scenario", "partial-formats")
	_, body := f.finish("/retexture", id, key)
	models, _ := body["model_urls"].(map[string]any)
	if models["glb"] == nil || models["obj"] == nil || models["stl"] != nil || models["3mf"] != nil {
		t.Fatalf("retexture model_urls = %v, want no stl and no 3mf", models)
	}
	remesh := f.create("/remesh", map[string]any{"input_task_id": f.source(), "target_formats": formats}, "X-Fake-Scenario", "partial-formats")
	_, body = f.finish("/remesh", remesh, key)
	if models, _ = body["model_urls"].(map[string]any); models["stl"] == nil || models["3mf"] == nil {
		t.Fatalf("a remesh keeps every format: %v", models)
	}
}

// The official API accepts an oversized rigging input and fails the task when it
// runs (observed with a 300000-face quad remesh): there is no 400 at submit.
func TestRiggingOfAnOversizedModelFailsTheTask(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	big := f.create("/remesh", map[string]any{"input_task_id": source, "topology": "quad", "target_polycount": 300000})
	f.finish("/remesh", big, key)
	rig := f.create("/rigging", map[string]any{"input_task_id": big})
	statuses, body := f.finish("/rigging", rig, key)
	if statuses[len(statuses)-1] != core.StatusFailed {
		t.Fatalf("flow = %v", statuses)
	}
	if testkit.Field(t, body, "task_error", "type") != "invalid_input" || testkit.Field(t, body, "consumed_credits").(float64) != 0 {
		t.Fatalf("failed rigging = %v", body)
	}
	// The same model remeshed to 10000 triangles rigs.
	small := f.create("/remesh", map[string]any{"input_task_id": source, "topology": "triangle", "target_polycount": 10000})
	f.finish("/remesh", small, key)
	ok := f.create("/rigging", map[string]any{"input_task_id": small})
	if statuses, _ := f.finish("/rigging", ok, key); statuses[len(statuses)-1] != core.StatusSucceeded {
		t.Fatalf("small model flow = %v", statuses)
	}
}

// The documented terminal status the samples do not hold: a canceled task
// stops without artifacts and can be deleted.
func TestCanceledTaskStopsWithoutArtifacts(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, "X-Fake-Scenario", "task-canceled")
	statuses, body := f.finish("/image-to-3d", id, key)
	if got := strings.Join(statuses, ","); got != "PENDING,IN_PROGRESS,CANCELED" {
		t.Fatalf("flow = %s", got)
	}
	if body["status"] != core.StatusCanceled || body["progress"].(float64) != 100 {
		t.Fatalf("canceled body = %v", body)
	}
	if got := urls(body, "", map[string]string{}); len(got) != 0 {
		t.Fatalf("a canceled task offers artifacts: %v", got)
	}
	if _, has := body["task_error"].(map[string]any); has {
		t.Fatalf("a canceled task carries a task_error: %v", body["task_error"])
	}
	if status, _, _ := f.Do("DELETE", f.v1("/image-to-3d/"+id), key, nil); status != http.StatusOK {
		t.Fatalf("delete of a canceled task = %d", status)
	}
}

// The documented 403 refusal (moderation, plan or IP restriction).
func TestForbiddenScenarioAnswersTheDocumentedRefusal(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("POST", f.v1("/image-to-3d"), key, map[string]any{"image_url": "x"}, "X-Fake-Scenario", "forbidden")
	if status != http.StatusForbidden || len(body) != 1 || body["message"] == "" {
		t.Fatalf("forbidden = %d %v", status, body)
	}
}

// The rigging page's 422 pose-estimation refusal; other kinds ignore it.
func TestRiggingNonHumanoidAnswers422(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	status, _, body := f.Do("POST", f.v1("/rigging"), key, map[string]any{"input_task_id": source}, "X-Fake-Scenario", "rig-non-humanoid")
	if status != http.StatusUnprocessableEntity || len(body) != 1 || body["message"] == "" {
		t.Fatalf("rig-non-humanoid = %d %v", status, body)
	}
	f.create("/remesh", map[string]any{"input_task_id": source}, "X-Fake-Scenario", "rig-non-humanoid")
}

// Every documented task_error.type is reachable; a code, when present, brings
// its doc_url.
func TestTaskErrorTypesFollowTheDocumentation(t *testing.T) {
	for _, testCase := range []struct {
		typ, code string
	}{
		{"invalid_input", ""},
		{"timeout", "timeout"},
		{"service_unavailable", ""},
		{"server_error", ""},
	} {
		t.Run(testCase.typ, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, core.TaskErrorHeader, testCase.typ)
			_, body := f.finish("/image-to-3d", id, key)
			if body["status"] != core.StatusFailed {
				t.Fatalf("status = %v", body["status"])
			}
			failure, _ := body["task_error"].(map[string]any)
			if failure["type"] != testCase.typ || failure["message"] == "" {
				t.Fatalf("task_error = %v", failure)
			}
			code, hasCode := failure["code"]
			_, hasDoc := failure["doc_url"]
			if testCase.code == "" && (hasCode || hasDoc) {
				t.Fatalf("%s must carry no code/doc_url: %v", testCase.typ, failure)
			}
			if testCase.code != "" && (code != testCase.code || !hasDoc) {
				t.Fatalf("%s code/doc_url = %v %v", testCase.typ, code, hasDoc)
			}
		})
	}
}

// The PENDING body leaves the start/finish timestamps out, and a FAILED body
// derived from the running sample gains the terminal ones (finding #203).
func TestPollBodiesFollowTheDocumentedStateFields(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	pending := f.create("/text-to-image", map[string]any{"prompt": "p"})
	status, _, body := f.Do("GET", f.v1("/text-to-image/"+pending), key, nil)
	if status != http.StatusOK || body["status"] != core.StatusPending {
		t.Fatalf("first poll = %d %v", status, body)
	}
	if body["progress"].(float64) != 0 {
		t.Fatalf("PENDING progress = %v", body["progress"])
	}
	if ahead, _ := body["preceding_tasks"].(float64); ahead != 3 {
		t.Errorf("PENDING preceding_tasks = %v", body["preceding_tasks"])
	}
	for _, field := range []string{"started_at", "finished_at", "expires_at"} {
		if _, has := body[field]; has {
			t.Errorf("PENDING body carries %s: %v", field, body)
		}
	}
	failed := f.create("/text-to-image", map[string]any{"prompt": "p"}, core.TaskErrorHeader, "timeout")
	_, body = f.finish("/text-to-image", failed, key)
	if body["status"] != core.StatusFailed {
		t.Fatalf("status = %v", body["status"])
	}
	for _, field := range []string{"started_at", "finished_at", "expires_at"} {
		if _, has := body[field]; !has {
			t.Errorf("FAILED body lacks %s: %v", field, body)
		}
	}
}

// textureMaps returns the first texture_urls object after checking every URL
// it carries is a downloadable PNG.
func textureMaps(t *testing.T, f *fixture, body map[string]any) map[string]any {
	t.Helper()
	entries, _ := body["texture_urls"].([]any)
	if len(entries) == 0 {
		t.Fatalf("texture_urls = %v", body["texture_urls"])
	}
	entry, _ := entries[0].(map[string]any)
	for field, value := range entry {
		url, _ := value.(string)
		status, contentType, data := f.Download(url)
		if status != http.StatusOK || contentType != "image/png" || len(data) == 0 {
			t.Errorf("texture %s = %d %q %d bytes", field, status, contentType, len(data))
		}
	}
	return entry
}

// The committed samples were captured with the cheapest texture options; a
// default Catalog request (should_texture/enable_pbr true) must still receive
// the documented maps (finding #203).
func TestTextureOptionsShapeTheSuccessBody(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	textured := f.create("/image-to-3d", map[string]any{
		"image_url": "https://example.test/a.png", "ai_model": "meshy-7.1",
		"should_texture": true, "enable_pbr": true, "texture_resolution": "8k",
		"target_formats": []string{"glb"},
	})
	_, body := f.finish("/image-to-3d", textured, key)
	entry := textureMaps(t, f, body)
	for _, field := range []string{"base_color", "metallic", "normal", "roughness"} {
		if _, has := entry[field]; !has {
			t.Errorf("image-to-3d texture lacks %s: %v", field, entry)
		}
	}
	// meshy-7.1 at 8k documents no emission map.
	if _, has := entry["emission"]; has {
		t.Errorf("image-to-3d texture carries an emission map: %v", entry)
	}

	plain := f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "should_texture": false})
	_, plainBody := f.finish("/image-to-3d", plain, key)
	if entries, _ := plainBody["texture_urls"].([]any); len(entries) != 0 {
		t.Errorf("an untextured task returns texture_urls: %v", entries)
	}

	multi := f.create("/multi-image-to-3d", map[string]any{
		"image_urls": []string{"https://example.test/a.png"}, "should_texture": true, "enable_pbr": true,
	})
	_, multiBody := f.finish("/multi-image-to-3d", multi, key)
	multiEntry := textureMaps(t, f, multiBody)
	for _, field := range []string{"base_color", "metallic", "normal", "roughness"} {
		if _, has := multiEntry[field]; !has {
			t.Errorf("multi-image-to-3d texture lacks %s: %v", field, multiEntry)
		}
	}

	retextured := f.create("/retexture", map[string]any{"input_task_id": f.source(), "enable_pbr": true})
	_, retextureBody := f.finish("/retexture", retextured, key)
	retextureEntry := textureMaps(t, f, retextureBody)
	for _, field := range []string{"base_color", "metallic", "normal", "roughness"} {
		if _, has := retextureEntry[field]; !has {
			t.Errorf("retexture texture lacks %s: %v", field, retextureEntry)
		}
	}
}

// The recorded DocURLs must name pages that document the route (finding #203).
func TestEndpointDocURLsNameTheRoutePages(t *testing.T) {
	want := map[string]string{
		"/rigging":           docsRoot + "rigging",
		"/animations":        docsRoot + "animation",
		"/print/analyze":     docsRoot + "analyze-printability",
		"/print/multi-color": docsRoot + "multi-color-print",
	}
	for _, endpoint := range endpoints {
		for path, url := range want {
			if endpoint.Path != path && !strings.HasPrefix(endpoint.Path, path+"/") {
				continue
			}
			if endpoint.DocURL != url || endpoint.CheckedAt != docsChecked {
				t.Errorf("%s: DocURL %q checked %q, want %q at %s", endpoint.Key(), endpoint.DocURL, endpoint.CheckedAt, url, docsChecked)
			}
		}
	}
}

func TestRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	const missing = "01a18514-059e-73f7-a0ce-0f88a12c9300"
	for name, tc := range map[string]struct {
		method, path, key string
		body              any
		status            int
	}{
		"no key balance":          {"GET", "/balance", "", nil, http.StatusUnauthorized},
		"no key create":           {"POST", "/remesh", "", map[string]any{"model_url": "x"}, http.StatusUnauthorized},
		"empty create":            {"POST", "/image-to-3d", key, map[string]any{}, http.StatusBadRequest},
		"empty remesh":            {"POST", "/remesh", key, map[string]any{}, http.StatusBadRequest},
		"convert without formats": {"POST", "/convert", key, map[string]any{"model_url": "x"}, http.StatusBadRequest},
		"unknown task":            {"GET", "/image-to-3d/" + missing, key, nil, http.StatusNotFound},
		"malformed id":            {"GET", "/image-to-3d/not-an-id", key, nil, http.StatusBadRequest},
		"unknown source":          {"POST", "/remesh", key, map[string]any{"input_task_id": missing}, http.StatusNotFound},
		"malformed source":        {"POST", "/remesh", key, map[string]any{"input_task_id": "nope"}, http.StatusBadRequest},
		"delete unknown":          {"DELETE", "/remesh/" + missing, key, nil, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do(tc.method, f.v1(tc.path), tc.key, tc.body)
			if status != tc.status {
				t.Fatalf("status %d body %v", status, body)
			}
			if _, ok := body["message"].(string); !ok || len(body) != 1 {
				t.Fatalf("error body must be {message}: %v", body)
			}
		})
	}
	status, _, body := f.Do("GET", f.v1("/balance"), key, nil)
	if status != http.StatusOK || testkit.Field(t, body, "balance").(float64) <= 0 {
		t.Fatalf("balance = %d %v", status, body)
	}
	// A source that has not finished is refused.
	pending := f.create("/image-to-3d", map[string]any{"image_url": "x"})
	if status, _, _ := f.Do("POST", f.v1("/remesh"), key, map[string]any{"input_task_id": pending}); status != http.StatusBadRequest {
		t.Fatalf("remesh of an unfinished task = %d", status)
	}
	// A deleted task is gone; a v2 task is a valid source for a v1 task.
	done := f.source()
	if status, _, _ := f.Do("DELETE", f.v1("/image-to-3d/"+done), key, nil); status != http.StatusOK {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := f.Do("GET", f.v1("/image-to-3d/"+done), key, nil); status != http.StatusNotFound {
		t.Fatalf("deleted task = %d", status)
	}
	preview := f.createV2(map[string]any{"mode": "preview", "prompt": "p"})
	f.finishV2(preview)
	if status, _, _ := f.Do("POST", f.v1("/remesh"), key, map[string]any{"input_task_id": preview}); status != http.StatusAccepted {
		t.Fatalf("remesh of a v2 task = %d", status)
	}
}

func (f *fixture) createV2(body map[string]any) string {
	status, _, response := f.Do("POST", meshyopenapiv2.Prefix+"/text-to-3d", key, body)
	if status != http.StatusAccepted {
		f.T.Fatalf("v2 create = %d %v", status, response)
	}
	return response["result"].(string)
}

func (f *fixture) finishV2(id string) {
	for i := 0; i < 8; i++ {
		_, _, body := f.Do("GET", meshyopenapiv2.Prefix+"/text-to-3d/"+id, key, nil)
		if body["status"] == core.StatusSucceeded {
			return
		}
	}
	f.T.Fatalf("v2 task %s never finished", id)
}

func TestPollCountsAreConfigurable(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{QueuedPolls: 2, RunningPolls: 3})
	id := f.create("/text-to-image", map[string]any{"prompt": "p"})
	statuses, _ := f.finish("/text-to-image", id, key)
	if got := strings.Join(statuses, ","); got != "PENDING,PENDING,IN_PROGRESS,IN_PROGRESS,IN_PROGRESS,SUCCEEDED" {
		t.Fatalf("flow = %s", got)
	}
}

// Every scenario has a regression: the exact status, message, header or
// behavior the Plinth Adapter keys on.
func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	post := func(f *fixture, scenario string) (int, http.Header, map[string]any) {
		return f.Do("POST", f.v1("/image-to-3d"), key, map[string]any{"image_url": "x"}, "X-Fake-Scenario", scenario)
	}
	t.Run("insufficient balance", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "insufficient-balance")
		if status != http.StatusPaymentRequired || body["message"] == "" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("key quota is the official credit limit message", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "key-quota")
		if status != http.StatusPaymentRequired || body["message"] != "API key credit limit reached" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		status, header, _ := post(newFixture(t, short), "rate-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" {
			t.Fatalf("%d %v", status, header)
		}
	})
	t.Run("server error", func(t *testing.T) {
		if status, _, _ := post(newFixture(t, short), "server-error"); status != http.StatusInternalServerError {
			t.Fatalf("status %d", status)
		}
	})
	t.Run("balance is not charged for a credit scenario", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, _ := f.Do("GET", f.v1("/balance"), key, nil, "X-Fake-Scenario", "insufficient-balance"); status != http.StatusOK {
			t.Fatalf("balance = %d", status)
		}
	})
	t.Run("timeout drops the connection", func(t *testing.T) {
		f := newFixture(t, short)
		start := time.Now()
		request, _ := http.NewRequest("POST", f.Server.URL+f.v1("/image-to-3d"), strings.NewReader(`{"image_url":"x"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "timeout")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		if time.Since(start) < 40*time.Millisecond {
			t.Fatalf("dropped after %v, before the hold", time.Since(start))
		}
	})
	t.Run("lost response keeps the task", func(t *testing.T) {
		f := newFixture(t, short)
		request, _ := http.NewRequest("POST", f.Server.URL+f.v1("/image-to-3d"), strings.NewReader(`{"image_url":"x"}`))
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		next := f.create("/image-to-3d", map[string]any{"image_url": "x"})
		lost := next[:len(next)-1] + string(rune(next[len(next)-1]-1))
		if status, _, body := f.Do("GET", f.v1("/image-to-3d/"+lost), key, nil); status != http.StatusOK {
			t.Fatalf("lost task %s = %d %v", lost, status, body)
		}
	})
	t.Run("task failed", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, "X-Fake-Scenario", "task-failed")
		statuses, body := f.finish("/image-to-3d", id, key)
		if statuses[len(statuses)-1] != core.StatusFailed || testkit.Field(t, body, "task_error", "message") == "" {
			t.Fatalf("%v %v", statuses, body)
		}
	})
	t.Run("artifact 404", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, "X-Fake-Scenario", "artifact-404")
		_, body := f.finish("/image-to-3d", id, key)
		if status, _, _ := f.Download(testkit.Field(t, body, "model_urls", "glb").(string)); status != http.StatusNotFound {
			t.Fatalf("artifact = %d", status)
		}
	})
	t.Run("artifact corrupt", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, "X-Fake-Scenario", "artifact-corrupt")
		_, body := f.finish("/image-to-3d", id, key)
		status, _, data := f.Download(testkit.Field(t, body, "model_urls", "glb").(string))
		if status != http.StatusOK || testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary") == nil {
			t.Fatalf("corrupt artifact: %d, delivery check passed", status)
		}
	})
	t.Run("artifact URL expired, a later read hands out working URLs", func(t *testing.T) {
		f := newFixture(t, short)
		id := f.create("/image-to-3d", map[string]any{"image_url": "x"}, "X-Fake-Scenario", "artifact-expired")
		_, body := f.finish("/image-to-3d", id, key)
		status, contentType, raw := f.Download(testkit.Field(t, body, "model_urls", "glb").(string))
		if status != http.StatusForbidden || !strings.Contains(contentType, "xml") || !strings.Contains(string(raw), "<Code>AccessDenied</Code>") {
			t.Fatalf("expired URL = %d %s %s", status, contentType, raw)
		}
		_, _, again := f.Do("GET", f.v1("/image-to-3d/"+id), key, nil)
		status, _, data := f.Download(testkit.Field(t, again, "model_urls", "glb").(string))
		if status != http.StatusOK || testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary") != nil {
			t.Fatalf("refetched URL = %d", status)
		}
	})
	t.Run("key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		status, _, _ := f.Do("POST", f.v1("/image-to-3d"), "fake-key-quota-x", map[string]any{"image_url": "x"})
		if status != http.StatusPaymentRequired {
			t.Fatalf("status = %d", status)
		}
	})
}

// Engines share no mutable state: servers built and used at the same time (the
// race detector is the assertion) each serve their own content types.
func TestConcurrentServersShareNoGlobalState(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := newFixture(t, fakeupstream.Config{})
			id := f.create("/image-to-3d", map[string]any{"image_url": "x"})
			_, body := f.finish("/image-to-3d", id, key)
			status, contentType, _ := f.Download(testkit.Field(t, body, "model_urls", "glb").(string))
			if status != http.StatusOK || contentType != "model/gltf-binary" {
				t.Errorf("glb = %d %q", status, contentType)
			}
		}()
	}
	wg.Wait()
}
