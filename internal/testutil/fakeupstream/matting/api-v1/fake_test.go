package mattingapiv1

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
)

const key = "test-key"

func newFixture(t *testing.T, config fakeupstream.Config) *testkit.Fixture {
	t.Helper()
	return testkit.New(t, config, Version())
}

func jobForm(t *testing.T, withFile bool) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("pipeline_params_json", pipeline)
	if withFile {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="files"; filename="input_1.png"`)
		header.Set("Content-Type", "image/png")
		part, _ := writer.CreatePart(header)
		_, _ = part.Write([]byte("png bytes"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func submit(f *testkit.Fixture, t *testing.T, headers ...string) (int, map[string]any) {
	t.Helper()
	status, _, body := f.Do("POST", Prefix+"/jobs", key, jobForm(t, true), headers...)
	return status, body
}

func poll(f *testkit.Fixture, t *testing.T, batchID string) map[string]any {
	t.Helper()
	status, _, body := f.Do("GET", Prefix+"/batches/"+batchID, key, nil)
	if status != http.StatusOK {
		t.Fatalf("poll: %d %v", status, body)
	}
	return body
}

func TestBatchRunsReadyRunningSuccessAndDownloadsAValidPicture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, created := submit(f, t)
	if status != http.StatusOK || created["batch_status"] != "READY" {
		t.Fatalf("create: %d %v", status, created)
	}
	batchID := created["batch_id"].(string)
	var seen []string
	var final map[string]any
	for len(seen) < 6 {
		final = poll(f, t, batchID)
		seen = append(seen, final["batch_status"].(string))
		if final["batch_status"] == "SUCCESS" {
			break
		}
	}
	if strings.Join(seen, ">") != "READY>RUNNING>SUCCESS" {
		t.Fatalf("batch states = %v", seen)
	}
	task := testkit.Field(t, final, "tasks", 0).(map[string]any)
	if task["status"] != "SUCCEEDED" || task["downloadable"] != true || task["source_name"] != "input_1.png" && task["source_name"] != "ok" {
		t.Fatalf("task = %v", task)
	}
	path, _ := task["download_path"].(string)
	if path != "/api/v1/tasks/"+task["task_id"].(string)+"/download" {
		t.Fatalf("download_path = %q", path)
	}
	status, header, data := f.Raw("GET", Prefix+"/tasks/"+task["task_id"].(string)+"/download", key, nil)
	if status != http.StatusOK || header.Get("Content-Type") != "image/png" {
		t.Fatalf("download: %d %s", status, header.Get("Content-Type"))
	}
	if err := testkit.DeliveryErr(t, data, "image", "", "image/png"); err != nil {
		t.Fatalf("downloaded picture invalid: %v", err)
	}
}

func TestRequestValidationAndAuthentication(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	if status, _, body := f.Do("POST", Prefix+"/jobs", "", jobForm(t, true)); status != http.StatusUnauthorized || body["detail"] != "Not authenticated" {
		t.Fatalf("no key: %d %v", status, body)
	}
	if status, _, body := f.Do("POST", Prefix+"/jobs", key, jobForm(t, false)); status != http.StatusUnprocessableEntity || testkit.Field(t, body, "detail", 0, "type") != "missing" {
		t.Fatalf("no file: %d %v", status, body)
	}
	if status, _, body := f.Do("GET", Prefix+"/batches/batch_unknown", key, nil); status != http.StatusNotFound || body["detail"] != "Batch not found" {
		t.Fatalf("unknown batch: %d %v", status, body)
	}
	_, created := submit(f, t)
	task := testkit.Field(t, created, "tasks", 0, "task_id").(string)
	if status, _, _ := f.Raw("GET", Prefix+"/tasks/"+task+"/download", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("download without a key = %d", status)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	t.Run("rate limit has Retry-After; balance faults answer a server error", func(t *testing.T) {
		f := newFixture(t, short)
		status, header, _ := f.Raw("POST", Prefix+"/jobs", key, jobForm(t, true), "X-Fake-Scenario", "rate-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" {
			t.Fatalf("%d %s", status, header.Get("Retry-After"))
		}
		for _, scenario := range []string{"insufficient-balance", "key-quota", "server-error"} {
			if status, _, _ := f.Raw("POST", Prefix+"/jobs", key, jobForm(t, true), "X-Fake-Scenario", scenario); status != http.StatusInternalServerError {
				t.Fatalf("%s = %d", scenario, status)
			}
		}
	})
	t.Run("a failed task ends the batch without a download", func(t *testing.T) {
		f := newFixture(t, short)
		_, created := submit(f, t, "X-Fake-Scenario", "task-failed")
		batchID := created["batch_id"].(string)
		var final map[string]any
		for i := 0; i < 6; i++ {
			if final = poll(f, t, batchID); final["batch_status"] == "FAILED" {
				break
			}
		}
		task := testkit.Field(t, final, "tasks", 0).(map[string]any)
		if final["batch_status"] != "FAILED" || task["status"] != "FAILED" || task["downloadable"] != false || task["error_message"] == nil {
			t.Fatalf("failed batch = %v", final)
		}
	})
	t.Run("a missing or corrupt result", func(t *testing.T) {
		f := newFixture(t, short)
		for _, tc := range []struct {
			scenario string
			want     int
		}{{"artifact-404", http.StatusNotFound}, {"artifact-corrupt", http.StatusOK}} {
			_, created := submit(f, t, "X-Fake-Scenario", tc.scenario)
			task := testkit.Field(t, created, "tasks", 0, "task_id").(string)
			status, _, data := f.Raw("GET", Prefix+"/tasks/"+task+"/download", key, nil)
			if status != tc.want {
				t.Fatalf("%s: %d", tc.scenario, status)
			}
			if tc.scenario == "artifact-corrupt" && testkit.DeliveryErr(t, data, "image", "", "image/png") == nil {
				t.Fatal("a corrupt picture passed the delivery check")
			}
		}
	})
	t.Run("lost response keeps the batch", func(t *testing.T) {
		f := newFixture(t, short)
		form := jobForm(t, true)
		req, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/jobs", bytes.NewReader(form.Data))
		req.Header.Set("Content-Type", form.ContentType)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(req); err == nil {
			response.Body.Close()
			t.Fatalf("the response should be lost, got %d", response.StatusCode)
		}
		if status, _, _ := f.Do("GET", Prefix+"/batches/batch_fake0001", key, nil); status != http.StatusOK {
			t.Fatalf("the batch must exist: %d", status)
		}
	})
}

func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})
	checked := map[string]bool{}
	_, created := submit(f, t)
	batchID := created["batch_id"].(string)
	states := map[string][]byte{}
	for i := 0; i < 4; i++ {
		_, _, raw := f.Raw("GET", Prefix+"/batches/"+batchID, key, nil)
		var body map[string]any
		_ = bytesToJSON(raw, &body)
		states[body["batch_status"].(string)] = raw
	}
	for _, exchange := range samples.Find("POST "+samplePrefix+"/jobs", "") {
		switch exchange.Response.Status {
		case http.StatusOK:
			_, _, raw := f.Raw("POST", Prefix+"/jobs", key, jobForm(t, true))
			testkit.AssertSameShape(t, "create", exchange.Response.Body.JSON, raw)
			checked["create"] = true
		case http.StatusUnauthorized:
			_, _, raw := f.Raw("POST", Prefix+"/jobs", "", jobForm(t, true))
			testkit.AssertSameShape(t, "401", exchange.Response.Body.JSON, raw)
			checked["401"] = true
		case http.StatusUnprocessableEntity:
			_, _, raw := f.Raw("POST", Prefix+"/jobs", key, jobForm(t, false))
			testkit.AssertSameShape(t, "422", exchange.Response.Body.JSON, raw)
			checked["422"] = true
		}
	}
	for _, exchange := range samples.Find("GET "+samplePrefix+"/batches/{id}", "") {
		if exchange.Response.Status == http.StatusNotFound {
			_, _, raw := f.Raw("GET", Prefix+"/batches/batch_unknown", key, nil)
			testkit.AssertSameShape(t, "404", exchange.Response.Body.JSON, raw)
			checked["404"] = true
			continue
		}
		var sample map[string]any
		_ = bytesToJSON(exchange.Response.Body.JSON, &sample)
		status, _ := sample["batch_status"].(string)
		raw, ok := states[status]
		if !ok {
			continue // the first batch poll of a sample is READY, which the create response covers
		}
		testkit.AssertSameShape(t, "batch "+status, exchange.Response.Body.JSON, raw)
		checked["batch "+status] = true
	}
	for _, want := range []string{"create", "401", "422", "404", "batch RUNNING", "batch SUCCESS"} {
		if !checked[want] {
			t.Errorf("the sample has no %s exchange to compare", want)
		}
	}
}

func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})
	testkit.AssertEndpointTable(t, endpoints, samples, func(endpoint fakeupstream.Endpoint) int {
		path := strings.NewReplacer("{batch_id}", "batch_x", "{task_id}", "task_x").Replace(endpoint.Path)
		status, _, _ := f.Raw(endpoint.Method, Prefix+path, "", nil)
		return status // without a key every routed path answers 401
	})
}

func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	testkit.AssertAlignmentReport(t, "testdata/alignment-report.json", OfficialBase, false)
}

func bytesToJSON(raw []byte, into any) error { return json.Unmarshal(raw, into) }
