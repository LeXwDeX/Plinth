package hyper3dapiv2

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const key = "test-key"

func newFixture(t *testing.T, config fakeupstream.Config) *testkit.Fixture {
	t.Helper()
	return testkit.New(t, config, Version())
}

// form builds a multipart body: text fields plus empty files under the names
// listed in files.
func form(fields map[string]string, files ...string) testkit.Body {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, value := range fields {
		_ = writer.WriteField(name, value)
	}
	for _, name := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+name+`.bin"`)
		header.Set("Content-Type", "application/octet-stream")
		part, _ := writer.CreatePart(header)
		_, _ = part.Write([]byte("data"))
	}
	_ = writer.Close()
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func textForm() testkit.Body {
	return form(map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"})
}

type submitted struct {
	uuid, key string
	jobs      int
}

func submit(t *testing.T, f *testkit.Fixture, route string, body testkit.Body, headers ...string) submitted {
	t.Helper()
	status, _, response := f.Do("POST", Prefix+route, key, body, headers...)
	if status != http.StatusCreated {
		t.Fatalf("POST %s = %d %v", route, status, response)
	}
	jobs, _ := testkit.Field(t, response, "jobs", "uuids").([]any)
	return submitted{uuid: testkit.Field(t, response, "uuid").(string), key: testkit.Field(t, response, "jobs", "subscription_key").(string), jobs: len(jobs)}
}

// poll returns the aggregated status flow (the first job that is not Done, else
// Done or Failed) until every job ended.
func poll(t *testing.T, f *testkit.Fixture, s submitted) []string {
	t.Helper()
	var flow []string
	for i := 0; i < 60; i++ {
		status, _, body := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
		if status != http.StatusCreated {
			t.Fatalf("status = %d %v", status, body)
		}
		state, ended := statusOf(t, body)
		if len(flow) == 0 || flow[len(flow)-1] != state {
			flow = append(flow, state)
		}
		if ended {
			return flow
		}
	}
	t.Fatalf("jobs never ended: %v", flow)
	return nil
}

func statusOf(t *testing.T, body map[string]any) (state string, ended bool) {
	t.Helper()
	jobs, _ := body["jobs"].([]any)
	state, ended = statusDone, true
	for _, job := range jobs {
		word, _ := job.(map[string]any)["status"].(string)
		switch word {
		case statusFailed:
			return statusFailed, true
		case statusDone:
		default:
			if ended {
				state = word
			}
			ended = false
		}
	}
	return state, ended
}

func download(t *testing.T, f *testkit.Fixture, s submitted) []any {
	t.Helper()
	status, _, body := f.Do("POST", Prefix+"/download", key, map[string]any{"task_uuid": s.uuid})
	if status != http.StatusCreated {
		t.Fatalf("download = %d %v", status, body)
	}
	return testkit.Field(t, body, "list").([]any)
}

func TestGenerationCompletesAndDeliversValidFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	s := submit(t, f, "/rodin", textForm())
	// Asked early, the official API answers an empty list.
	if list := download(t, f, s); len(list) != 0 {
		t.Fatalf("download before the jobs ended = %v", list)
	}
	flow := poll(t, f, s)
	if got := strings.Join(flow, ">"); !strings.HasPrefix(got, "Waiting>Generating") || !strings.HasSuffix(got, "Done") {
		t.Fatalf("flow = %s", got)
	}
	list := download(t, f, s)
	if len(list) != 2 {
		t.Fatalf("list = %v", list)
	}
	names := map[string]string{}
	for _, item := range list {
		entry := item.(map[string]any)
		names[entry["name"].(string)] = entry["url"].(string)
	}
	for name, wantType := range map[string]string{"base_basic_pbr.glb": "model/gltf-binary", "preview.webp": "image/webp"} {
		status, contentType, data := f.Download(names[name])
		if status != http.StatusOK || contentType != wantType || len(data) == 0 {
			t.Fatalf("%s = %d %q %d bytes", name, status, contentType, len(data))
		}
		if strings.HasSuffix(name, ".glb") {
			if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Fatalf("GLB rejected by the delivery check: %v", err)
			}
		}
	}
}

func TestBangAndTextureNeedTheirInputs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": "00000000-0000-4000-8000-000000000001"}))
	if status != http.StatusCreated || body["error"] != "ASSET_NOT_AVAILABLE" {
		t.Fatalf("bang of an unknown asset = %d %v", status, body)
	}
	gen := submit(t, f, "/rodin", textForm())
	if _, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": gen.uuid})); body["error"] != "ASSET_NOT_AVAILABLE" {
		t.Fatalf("bang of an unfinished task = %v", body)
	}
	poll(t, f, gen)
	bang := submit(t, f, "/bang", form(map[string]string{"asset_id": gen.uuid, "material": "PBR", "resolution": "High"}))
	poll(t, f, bang)
	list := download(t, f, bang)
	if name := list[0].(map[string]any)["name"].(string); !strings.HasSuffix(name, "_pbr.glb") {
		t.Fatalf("a PBR bang must offer a _pbr.glb model, got %q", name)
	}
	if _, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, form(map[string]string{"prompt": "p"}, "model")); body["error"] != "INVALID_REQUEST" {
		t.Fatalf("texture without an image = %v", body)
	}
	texture := submit(t, f, "/rodin_texture_only", form(map[string]string{"material": "PBR"}, "model", "image"))
	poll(t, f, texture)
	if len(download(t, f, texture)) != 1 {
		t.Fatal("texture must offer one model")
	}
}

func TestRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		method, path, key string
		body              any
		status            int
		field, value      string
	}{
		"no key balance":     {"GET", "/check_balance", "", nil, http.StatusUnauthorized, "error", "UNAUTHORIZED"},
		"no key rodin":       {"POST", "/rodin", "", textForm(), http.StatusUnauthorized, "error", "UNAUTHORIZED"},
		"no prompt or image": {"POST", "/rodin", key, form(map[string]string{"tier": "Gen-2.5-Extreme-Low"}), http.StatusCreated, "error", "INVALID_REQUEST"},
		"not multipart":      {"POST", "/rodin", key, map[string]any{"prompt": "p"}, http.StatusBadRequest, "error", "Bad Request"},
		"status no key":      {"POST", "/status", key, map[string]any{}, http.StatusBadRequest, "error", "Bad Request"},
		"status bad key":     {"POST", "/status", key, map[string]any{"subscription_key": "nope"}, http.StatusBadRequest, "error", "Bad Request"},
		"download no uuid":   {"POST", "/download", key, map[string]any{}, http.StatusBadRequest, "error", "Bad Request"},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do(tc.method, Prefix+tc.path, tc.key, tc.body)
			if status != tc.status || body[tc.field] != tc.value {
				t.Fatalf("status %d body %v", status, body)
			}
		})
	}
	status, _, body := f.Do("GET", Prefix+"/check_balance", key, nil)
	if status != http.StatusOK || testkit.Field(t, body, "balance").(float64) <= 0 {
		t.Fatalf("balance = %d %v", status, body)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	post := func(f *testkit.Fixture, scenario string) (int, http.Header, map[string]any) {
		return f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", scenario)
	}
	t.Run("insufficient balance", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "insufficient-balance")
		if status != http.StatusCreated || body["error"] != "API_INSUFFICIENT_FUNDS" {
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
	t.Run("timeout drops the connection", func(t *testing.T) {
		f := newFixture(t, short)
		start := time.Now()
		body := textForm()
		request, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/rodin", bytes.NewReader(body.Data))
		request.Header.Set("Content-Type", body.ContentType)
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
	t.Run("lost response keeps the work", func(t *testing.T) {
		f := newFixture(t, short)
		body := textForm()
		request, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/rodin", bytes.NewReader(body.Data))
		request.Header.Set("Content-Type", body.ContentType)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		// The work exists although the caller never saw its ids: the first task
		// took the first uuid and its jobs the next ones, so a later submission
		// counts on from them, and the lost task still answers its status.
		next := submit(t, f, "/rodin", textForm())
		lost := next.uuid[:len(next.uuid)-12] + "000000000001"
		status, _, polled := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": "fake-subscription-" + lost})
		if status != http.StatusCreated || len(testkit.Field(t, polled, "jobs").([]any)) != next.jobs {
			t.Fatalf("lost task %s = %d %v", lost, status, polled)
		}
	})
	t.Run("task failed", func(t *testing.T) {
		f := newFixture(t, short)
		s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", "task-failed")
		flow := poll(t, f, s)
		if flow[len(flow)-1] != statusFailed {
			t.Fatalf("flow = %v", flow)
		}
		if list := download(t, f, s); len(list) != 0 {
			t.Fatalf("a failed task offers files: %v", list)
		}
	})
	t.Run("artifact 404 and corrupt", func(t *testing.T) {
		for scenario, ok := range map[string]bool{"artifact-404": false, "artifact-corrupt": true} {
			f := newFixture(t, short)
			s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", scenario)
			poll(t, f, s)
			url := download(t, f, s)[0].(map[string]any)["url"].(string)
			status, _, data := f.Download(url)
			switch {
			case !ok && status != http.StatusNotFound:
				t.Fatalf("%s = %d", scenario, status)
			case ok && (status != http.StatusOK || testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary") == nil):
				t.Fatalf("%s: %d, delivery check passed", scenario, status)
			}
		}
	})
}

// The fake keeps the structure of the real samples for every route.
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	shapeOf := func(raw []byte) fakeupstream.Shape {
		shape, err := fakeupstream.ShapeOf(raw)
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	for _, file := range sampleFiles {
		samples, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			t.Fatal(err)
		}
		route := ""
		for _, exchange := range samples.Exchanges {
			body, _ := fakeupstream.JSONBody(exchange)
			if body != nil && body["uuid"] != nil && body["jobs"] != nil {
				route = strings.TrimPrefix(exchange.Request.Path, samplePrefix)
				break
			}
		}
		fields := map[string]string{"prompt": "p", "asset_id": "", "material": "PBR"}
		var gen submitted
		files := []string{}
		switch route {
		case "/rodin":
			files = nil
		case "/bang":
			gen = submit(t, f, "/rodin", textForm())
			poll(t, f, gen)
			fields["asset_id"] = gen.uuid
		case "/rodin_texture_only":
			files = []string{"model", "image"}
		}
		for _, exchange := range samples.Exchanges {
			body, _ := fakeupstream.JSONBody(exchange)
			apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			var raw []byte
			var status int
			switch {
			case apiPath == "/check_balance" && exchange.Response.Status == 200:
				status, _, raw = f.Raw("GET", Prefix+apiPath, key, nil)
			case apiPath == "/check_balance":
				status, _, raw = f.Raw("GET", Prefix+apiPath, "", nil)
			case exchange.Response.Status == 401:
				status, _, raw = f.Raw("POST", Prefix+route, "", textForm())
			case apiPath == route && body["error"] != nil:
				status, _, raw = f.Raw("POST", Prefix+route, key, form(map[string]string{"tier": "x"}))
			case apiPath == "/status" && exchange.Response.Status == 400, apiPath == "/download" && exchange.Response.Status == 400:
				status, _, raw = f.Raw("POST", Prefix+apiPath, key, map[string]any{})
			default:
				continue // submit, status and download of a task are compared below
			}
			if status != exchange.Response.Status {
				t.Errorf("%s %s: status %d, sample %d", file, apiPath, status, exchange.Response.Status)
			}
			if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(raw)); !diff.Empty() {
				t.Errorf("%s %s drifted from the real sample: %s", file, apiPath, diff)
			}
		}
		// A real task: submit, every sampled status, then the download list.
		s := submit(t, f, route, form(fields, files...))
		_, _, submitRaw := f.Raw("POST", Prefix+route, key, form(fields, files...))
		compared := 0
		for _, exchange := range samples.Exchanges {
			apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			body, _ := fakeupstream.JSONBody(exchange)
			if apiPath == route && exchange.Response.Status == 201 && body["jobs"] != nil {
				if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(submitRaw)); !diff.Empty() {
					t.Errorf("%s submit drifted: %s", route, diff)
				}
				compared++
			}
		}
		for i := 0; i < 60; i++ {
			_, _, polled := f.Raw("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
			var decoded map[string]any
			_ = json.Unmarshal(polled, &decoded)
			if _, ended := statusOf(t, decoded); ended {
				for _, exchange := range samples.Exchanges {
					if strings.HasSuffix(exchange.Request.Path, "/status") && exchange.Response.Status == 201 {
						if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(polled)); !diff.Empty() && allDone(exchange) {
							t.Errorf("%s done poll drifted: %s", route, diff)
						}
					}
				}
				break
			}
		}
		_, _, listed := f.Raw("POST", Prefix+"/download", key, map[string]any{"task_uuid": s.uuid})
		for _, exchange := range samples.Exchanges {
			if strings.HasSuffix(exchange.Request.Path, "/download") && exchange.Response.Status == 201 {
				if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(listed)); !diff.Empty() {
					t.Errorf("%s download drifted: %s", route, diff)
				}
			}
		}
		if compared == 0 {
			t.Errorf("%s has no submit sample", file)
		}
	}
}

// allDone reports whether a status sample has every job Done (the final poll).
func allDone(exchange wirefixture.Exchange) bool {
	body, _ := fakeupstream.JSONBody(exchange)
	jobs, _ := body["jobs"].([]any)
	for _, job := range jobs {
		if job.(map[string]any)["status"] != statusDone {
			return false
		}
	}
	return len(jobs) > 0
}

func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples := map[string]bool{}
	for _, file := range sampleFiles {
		loaded, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, exchange := range loaded.Exchanges {
			samples[fakeupstream.SampleKey(exchange.Request.Method, exchange.Request.Path)] = true
		}
	}
	f := newFixture(t, fakeupstream.Config{})
	for _, endpoint := range endpoints {
		switch endpoint.Evidence {
		case fakeupstream.EvidenceReal:
			if !samples[endpoint.Sample] {
				t.Errorf("%s names sample %q, which is not committed", endpoint.Key(), endpoint.Sample)
			}
		case fakeupstream.EvidenceConstructed:
			if !strings.HasPrefix(endpoint.DocURL, "https://") || len(endpoint.CheckedAt) != len("2026-10-29") {
				t.Errorf("%s: constructed endpoints need a documentation URL and a check date", endpoint.Key())
			}
		default:
			t.Errorf("%s has no evidence class", endpoint.Key())
		}
		if strings.Contains(endpoint.Path, "{") {
			continue
		}
		if status, _, _ := f.Do(endpoint.Method, Prefix+endpoint.Path, "", nil); status != http.StatusUnauthorized {
			t.Errorf("%s answers %d without a key, want 401", endpoint.Key(), status)
		}
	}
}
