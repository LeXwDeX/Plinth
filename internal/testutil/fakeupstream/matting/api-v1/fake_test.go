package mattingapiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
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
	if !withFile {
		return jobFormWithFiles(t)
	}
	return jobFormWithFiles(t, "input_1.png")
}

// jobFormWithFiles sends one files part per name.
func jobFormWithFiles(t *testing.T, names ...string) testkit.Body {
	t.Helper()
	return jobFormWithPipeline(t, pipeline, names...)
}

// jobFormWithPipeline sends the given pipeline_params_json and one files part
// per name.
func jobFormWithPipeline(t *testing.T, pipelineJSON string, names ...string) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("pipeline_params_json", pipelineJSON)
	for _, name := range names {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="files"; filename="`+name+`"`)
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
	return submitFiles(f, t, []string{"input_1.png"}, headers...)
}

// submitFiles creates a batch carrying one file per name.
func submitFiles(f *testkit.Fixture, t *testing.T, names []string, headers ...string) (int, map[string]any) {
	t.Helper()
	status, _, body := f.Do("POST", Prefix+"/jobs", key, jobFormWithFiles(t, names...), headers...)
	return status, body
}

// count reads a numeric field of a decoded response.
func count(t *testing.T, body map[string]any, name string) float64 {
	t.Helper()
	value, ok := body[name].(float64)
	if !ok {
		t.Fatalf("%s is not a number: %v", name, body[name])
	}
	return value
}

func poll(f *testkit.Fixture, t *testing.T, batchID string) map[string]any {
	t.Helper()
	status, _, body := f.Do("GET", Prefix+"/batches/"+batchID, key, nil)
	if status != http.StatusOK {
		t.Fatalf("poll: %d %v", status, body)
	}
	return body
}

func pollUntil(f *testkit.Fixture, t *testing.T, batchID, want string) map[string]any {
	t.Helper()
	for i := 0; i < 6; i++ {
		if final := poll(f, t, batchID); final["batch_status"] == want {
			return final
		}
	}
	t.Fatalf("batch %s never reached %s", batchID, want)
	return nil
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

// #284: the service validates the process resolution of its PipelineParams
// (birefnet_process_res 512..4096 with null meaning auto, sdmatte_process_res
// 512..2048) and answers 422 outside it, so a Schema range that drifts past the
// service is caught by the fake before a real batch is spent.
func TestProcessResolutionIsHeldToTheServiceRange(t *testing.T) {
	for _, test := range []struct {
		name     string
		pipeline string
		refused  []string // the fields the 422 names, in order; none means accepted
	}{
		{"declared defaults", pipeline, nil},
		{"both at the lower edge", `{"birefnet_process_res":512,"sdmatte_process_res":512}`, nil},
		{"both at the upper edge", `{"birefnet_process_res":4096,"sdmatte_process_res":2048}`, nil},
		{"birefnet null is auto", `{"birefnet_process_res":null,"sdmatte_process_res":1024}`, nil},
		{"a field left out takes the service default", `{"expand":240}`, nil},
		{"an empty pipeline", ``, nil},
		{"birefnet above its range", `{"birefnet_process_res":4097,"sdmatte_process_res":1024}`, []string{"birefnet_process_res"}},
		{"birefnet below its range", `{"birefnet_process_res":511}`, []string{"birefnet_process_res"}},
		{"sdmatte above its range", `{"birefnet_process_res":1024,"sdmatte_process_res":2049}`, []string{"sdmatte_process_res"}},
		{"sdmatte below its range", `{"sdmatte_process_res":511}`, []string{"sdmatte_process_res"}},
		{"sdmatte has no auto", `{"sdmatte_process_res":null}`, []string{"sdmatte_process_res"}},
		{"both out of range are both named", `{"birefnet_process_res":9000,"sdmatte_process_res":100}`,
			[]string{"birefnet_process_res", "sdmatte_process_res"}},
		{"a fraction is not an integer", `{"birefnet_process_res":1024.5}`, []string{"birefnet_process_res"}},
		{"a string is not an integer", `{"birefnet_process_res":"big"}`, []string{"birefnet_process_res"}},
		{"a whole number written as a float is accepted", `{"birefnet_process_res":2048.0}`, nil},
		{"a pipeline that is not an object is not judged", `[1,2]`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			status, _, body := f.Do("POST", Prefix+"/jobs", key, jobFormWithPipeline(t, test.pipeline, "input_1.png"))
			if len(test.refused) == 0 {
				if status != http.StatusOK || body["batch_status"] != "READY" {
					t.Fatalf("%s: %d %v", test.pipeline, status, body)
				}
				return
			}
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("%s: %d %v", test.pipeline, status, body)
			}
			detail, _ := body["detail"].([]any)
			if len(detail) != len(test.refused) {
				t.Fatalf("%s: detail = %v", test.pipeline, body["detail"])
			}
			for index, field := range test.refused {
				if got := testkit.Field(t, body, "detail", index, "loc", 2); got != field {
					t.Fatalf("detail %d names %v, want %s", index, got, field)
				}
			}
			// The refused create made no batch.
			if status, _, _ := f.Do("GET", Prefix+"/batches/batch_fake0001", key, nil); status != http.StatusNotFound {
				t.Fatalf("a refused create left a batch behind: %d", status)
			}
		})
	}
}

// The refusal is the FastAPI validation list the committed 422 sample shows
// (type, loc, msg, input), with pydantic's ctx bound for a range error.
func TestProcessResolutionRefusalIsAFastAPIValidationList(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		t.Fatal(err)
	}
	var sampleEntry map[string]any
	for _, exchange := range samples.Find("POST "+samplePrefix+"/jobs", "") {
		if exchange.Response.Status != http.StatusUnprocessableEntity {
			continue
		}
		var sample struct {
			Detail []map[string]any `json:"detail"`
		}
		if err := json.Unmarshal(exchange.Response.Body.JSON, &sample); err != nil || len(sample.Detail) == 0 {
			t.Fatalf("422 sample = %s: %v", exchange.Response.Body.JSON, err)
		}
		sampleEntry = sample.Detail[0]
	}
	if sampleEntry == nil {
		t.Fatal("the sample has no 422 exchange to compare")
	}
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("POST", Prefix+"/jobs", key,
		jobFormWithPipeline(t, `{"birefnet_process_res":8192,"sdmatte_process_res":256}`, "input_1.png"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %v", status, body)
	}
	if len(body) != 1 {
		t.Fatalf("body = %v, want only detail", body)
	}
	for index, want := range []struct {
		field, kind, msg, bound string
		input, limit            float64
	}{
		{"birefnet_process_res", "less_than_equal", "Input should be less than or equal to 4096", "le", 8192, 4096},
		{"sdmatte_process_res", "greater_than_equal", "Input should be greater than or equal to 512", "ge", 256, 512},
	} {
		entry, _ := testkit.Field(t, body, "detail", index).(map[string]any)
		for name := range sampleEntry {
			if _, ok := entry[name]; !ok {
				t.Fatalf("entry %d lacks %q that the real 422 carries: %v", index, name, entry)
			}
		}
		if entry["type"] != want.kind || entry["msg"] != want.msg || entry["input"] != want.input {
			t.Fatalf("entry %d = %v", index, entry)
		}
		if got := testkit.Field(t, entry, "loc", 0); got != "body" {
			t.Fatalf("entry %d loc = %v", index, entry["loc"])
		}
		if got := testkit.Field(t, entry, "loc", 1); got != "pipeline_params_json" {
			t.Fatalf("entry %d loc = %v", index, entry["loc"])
		}
		if got := testkit.Field(t, entry, "loc", 2); got != want.field {
			t.Fatalf("entry %d loc = %v", index, entry["loc"])
		}
		if got := testkit.Field(t, entry, "ctx", want.bound); got != want.limit {
			t.Fatalf("entry %d ctx = %v", index, entry["ctx"])
		}
	}
}

// The exported limits are what the create route enforces, so a test elsewhere
// can hold the product Schema's declared ranges against them.
func TestResolutionLimitsAreTheEnforcedOnes(t *testing.T) {
	limits := ResolutionLimits()
	if len(limits) != 2 || limits[0].Field != "birefnet_process_res" || limits[1].Field != "sdmatte_process_res" {
		t.Fatalf("limits = %+v", limits)
	}
	f := newFixture(t, fakeupstream.Config{})
	for _, limit := range limits {
		for _, value := range []int64{limit.Min, limit.Max} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%d}`, limit.Field, value), "input_1.png")
			if status, _, body := f.Do("POST", Prefix+"/jobs", key, form); status != http.StatusOK {
				t.Fatalf("%s=%d: %d %v", limit.Field, value, status, body)
			}
		}
		for _, value := range []int64{limit.Min - 1, limit.Max + 1} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%d}`, limit.Field, value), "input_1.png")
			if status, _, body := f.Do("POST", Prefix+"/jobs", key, form); status != http.StatusUnprocessableEntity {
				t.Fatalf("%s=%d: %d %v", limit.Field, value, status, body)
			}
		}
	}
}

func TestABatchCarriesEveryUploadedFile(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		t.Fatal(err)
	}
	var sampleCreate []byte
	for _, exchange := range samples.Find("POST "+samplePrefix+"/jobs", "") {
		if exchange.Response.Status == http.StatusOK {
			sampleCreate = exchange.Response.Body.JSON
		}
	}
	f := newFixture(t, fakeupstream.Config{})
	names := []string{"first.png", "second.jpg", "third.webp"}
	status, _, raw := f.Raw("POST", Prefix+"/jobs", key, jobFormWithFiles(t, names...))
	if status != http.StatusOK {
		t.Fatalf("create with three files: %d %s", status, raw)
	}
	testkit.AssertSameShape(t, "multi-file create", sampleCreate, raw)
	var created map[string]any
	_ = bytesToJSON(raw, &created)
	if count(t, created, "total_count") != 3 || count(t, created, "accepted_count") != 3 || count(t, created, "rejected_count") != 0 {
		t.Fatalf("counts = %v", created)
	}
	ids := map[string]bool{}
	for index, name := range names {
		task := testkit.Field(t, created, "tasks", index).(map[string]any)
		if task["status"] != "READY" || task["source_index"] != float64(index) || task["source_name"] != name {
			t.Fatalf("task %d = %v", index, task)
		}
		id := task["task_id"].(string)
		if ids[id] {
			t.Fatalf("duplicate task id %q", id)
		}
		ids[id] = true
	}
	final := pollUntil(f, t, created["batch_id"].(string), "SUCCESS")
	for index := range names {
		task := testkit.Field(t, final, "tasks", index).(map[string]any)
		if task["status"] != "SUCCEEDED" || task["downloadable"] != true {
			t.Fatalf("final task %d = %v", index, task)
		}
		id := task["task_id"].(string)
		status, header, data := f.Raw("GET", Prefix+"/tasks/"+id+"/download", key, nil)
		if status != http.StatusOK || header.Get("Content-Type") != "image/png" {
			t.Fatalf("download %s: %d %s", id, status, header.Get("Content-Type"))
		}
		if err := testkit.DeliveryErr(t, data, "image", "", "image/png"); err != nil {
			t.Fatalf("downloaded picture invalid: %v", err)
		}
	}
}

func TestPartialImagesRejectsTheExtraFilesAndEndsPartialSuccess(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, created := submitFiles(f, t, []string{"accepted.png", "rejected.png"}, "X-Fake-Scenario", "partial-images")
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	if count(t, created, "total_count") != 2 || count(t, created, "accepted_count") != 1 || count(t, created, "rejected_count") != 1 {
		t.Fatalf("counts = %v", created)
	}
	accepted := testkit.Field(t, created, "tasks", 0).(map[string]any)
	if accepted["status"] != "READY" || accepted["source_name"] != "accepted.png" || accepted["source_index"] != float64(0) {
		t.Fatalf("accepted task = %v", accepted)
	}
	rejected := testkit.Field(t, created, "tasks", 1).(map[string]any)
	if rejected["status"] != "REJECTED" || rejected["downloadable"] != false ||
		rejected["invalid_reason"] == nil || rejected["source_name"] != "rejected.png" || rejected["source_index"] != float64(1) {
		t.Fatalf("rejected task = %v", rejected)
	}
	final := pollUntil(f, t, created["batch_id"].(string), "PARTIAL_SUCCESS")
	if count(t, final, "accepted_count") != 1 || count(t, final, "rejected_count") != 1 {
		t.Fatalf("final counts = %v", final)
	}
	accepted = testkit.Field(t, final, "tasks", 0).(map[string]any)
	if accepted["status"] != "SUCCEEDED" || accepted["downloadable"] != true {
		t.Fatalf("final accepted task = %v", accepted)
	}
	rejected = testkit.Field(t, final, "tasks", 1).(map[string]any)
	if rejected["status"] != "REJECTED" || rejected["downloadable"] != false {
		t.Fatalf("final rejected task = %v", rejected)
	}
	acceptedID := accepted["task_id"].(string)
	if status, _, _ := f.Raw("GET", Prefix+"/tasks/"+acceptedID+"/download", key, nil); status != http.StatusOK {
		t.Fatalf("download of the accepted task = %d", status)
	}
	rejectedID := rejected["task_id"].(string)
	if status, _, _ := f.Raw("GET", Prefix+"/tasks/"+rejectedID+"/download", key, nil); status != http.StatusNotFound {
		t.Fatalf("download of a rejected task = %d", status)
	}
}

func TestPartialImagesWithOneFileEndsCompletedWithoutArtifacts(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, created := submitFiles(f, t, []string{"only.png"}, "X-Fake-Scenario", "partial-images")
	if count(t, created, "total_count") != 1 || count(t, created, "accepted_count") != 0 || count(t, created, "rejected_count") != 1 {
		t.Fatalf("counts = %v", created)
	}
	final := pollUntil(f, t, created["batch_id"].(string), "PARTIAL_SUCCESS")
	task := testkit.Field(t, final, "tasks", 0).(map[string]any)
	if task["status"] != "REJECTED" || task["downloadable"] != false {
		t.Fatalf("task = %v", task)
	}
	// A completed batch that claims no artifact is the Adapter's no_artifacts branch.
}

// taskStatuses are the statuses of a batch document's tasks, in upload order.
func taskStatuses(t *testing.T, body map[string]any) []string {
	t.Helper()
	tasks, _ := body["tasks"].([]any)
	statuses := make([]string, 0, len(tasks))
	for index := range tasks {
		statuses = append(statuses, testkit.Field(t, body, "tasks", index, "status").(string))
	}
	return statuses
}

// #284: X-Fake-File-Faults chooses what becomes of each file by its 1-based
// position, so a batch can mix files that succeed with files that are rejected
// or fail in any positions (partial-images rejects every file but the first).
// A rejected file is refused in the create response already, a file that fails
// looks like the others until the batch finishes, and neither has an artifact.
func TestFileFaultsChooseWhatBecomesOfEachFile(t *testing.T) {
	for _, test := range []struct {
		name, faults string
		files        int
		created      []string // the task statuses of the create response
		final        []string // the task statuses of the finished batch
		batch        string
	}{
		{"a rejected file and a file that fails among files that succeed", "2:rejected,4:failed", 4,
			[]string{"READY", "REJECTED", "READY", "READY"}, []string{"SUCCEEDED", "REJECTED", "SUCCEEDED", "FAILED"}, "PARTIAL_SUCCESS"},
		{"the first file fails", "1:failed", 2,
			[]string{"READY", "READY"}, []string{"FAILED", "SUCCEEDED"}, "PARTIAL_SUCCESS"},
		{"every file fails", "1:failed,2:failed", 2,
			[]string{"READY", "READY"}, []string{"FAILED", "FAILED"}, "PARTIAL_SUCCESS"},
		{"every file is rejected", "1:rejected,2:rejected", 2,
			[]string{"REJECTED", "REJECTED"}, []string{"REJECTED", "REJECTED"}, "PARTIAL_SUCCESS"},
		{"a file that is rejected and would fail stays rejected", "1:rejected,1:failed", 2,
			[]string{"REJECTED", "READY"}, []string{"REJECTED", "SUCCEEDED"}, "PARTIAL_SUCCESS"},
		{"spaces around the entries are tolerated", " 2 : rejected , 1 : failed ", 2,
			[]string{"READY", "REJECTED"}, []string{"FAILED", "REJECTED"}, "PARTIAL_SUCCESS"},
		{"a renamed file is not a fault of its result", "2:renamed", 2,
			[]string{"READY", "READY"}, []string{"SUCCEEDED", "SUCCEEDED"}, "SUCCESS"},
		{"entries it does not understand are ignored", "9:rejected,0:failed,x:failed,1:exploded,1,:failed", 2,
			[]string{"READY", "READY"}, []string{"SUCCEEDED", "SUCCEEDED"}, "SUCCESS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			names := make([]string, test.files)
			for index := range names {
				names[index] = fmt.Sprintf("input_%d.png", index+1)
			}
			status, created := submitFiles(f, t, names, FileFaultsHeader, test.faults)
			if status != http.StatusOK {
				t.Fatalf("create: %d %v", status, created)
			}
			if got := taskStatuses(t, created); !slices.Equal(got, test.created) {
				t.Fatalf("created statuses = %v, want %v", got, test.created)
			}
			batchID := created["batch_id"].(string)
			// The file that fails shows the state of the others until the batch ends.
			if queued := poll(f, t, batchID); queued["batch_status"] != "READY" {
				t.Fatalf("first poll = %v", queued)
			}
			running := poll(f, t, batchID)
			for index, got := range taskStatuses(t, running) {
				if want := map[string]string{"READY": "RUNNING", "REJECTED": "REJECTED"}[test.created[index]]; got != want {
					t.Fatalf("running task %d = %s, want %s", index+1, got, want)
				}
			}
			final := pollUntil(f, t, batchID, test.batch)
			if got := taskStatuses(t, final); !slices.Equal(got, test.final) {
				t.Fatalf("final statuses = %v, want %v", got, test.final)
			}
			rejected, accepted := 0, 0
			for _, want := range test.created {
				if want == "REJECTED" {
					rejected++
				} else {
					accepted++
				}
			}
			for _, document := range []map[string]any{created, final} {
				if count(t, document, "total_count") != float64(test.files) || count(t, document, "accepted_count") != float64(accepted) ||
					count(t, document, "rejected_count") != float64(rejected) {
					t.Fatalf("counts = %v", document)
				}
			}
			for index, want := range test.final {
				task := testkit.Field(t, final, "tasks", index).(map[string]any)
				id := task["task_id"].(string)
				if task["downloadable"] != (want == "SUCCEEDED") ||
					(task["invalid_reason"] != nil) != (want == "REJECTED") || (task["error_message"] != nil) != (want == "FAILED") {
					t.Fatalf("task %d = %v", index+1, task)
				}
				// Only a file that succeeded has an artifact to download.
				wantStatus := http.StatusNotFound
				if want == "SUCCEEDED" {
					wantStatus = http.StatusOK
				}
				if status, _, _ := f.Raw("GET", Prefix+"/tasks/"+id+"/download", key, nil); status != wantStatus {
					t.Fatalf("download of the %s task %d = %d, want %d", want, index+1, status, wantStatus)
				}
			}
		})
	}
}

// A renamed file is answered under a name of the service's own, and only that
// file: the others keep the names they were uploaded under, in the create
// response and in every poll, and a position is always the file's own.
func TestFileFaultsRenamedFileIsAnsweredUnderAnotherName(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	names := []string{"input_1.png", "input_2.jpg", "input_3.png", "input_4.png"}
	_, created := submitFiles(f, t, names, FileFaultsHeader, "3:renamed,4:failed")
	final := pollUntil(f, t, created["batch_id"].(string), "PARTIAL_SUCCESS")
	for _, document := range []map[string]any{created, final} {
		for index, name := range names {
			if index == 2 {
				name = renamedSource
			}
			task := testkit.Field(t, document, "tasks", index).(map[string]any)
			if task["source_name"] != name || task["source_index"] != float64(index) {
				t.Fatalf("task %d = %v, want source_name %q", index+1, task, name)
			}
		}
	}
	if status := taskStatuses(t, final); !slices.Equal(status, []string{"SUCCEEDED", "SUCCEEDED", "SUCCEEDED", "FAILED"}) {
		t.Fatalf("final statuses = %v", status)
	}
}

// The batch keeps the faults of the request that created it: a later poll that
// carries the header changes nothing, and a batch created without it has none.
func TestFileFaultsBelongToTheCreatingRequest(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, created := submitFiles(f, t, []string{"input_1.png", "input_2.png"})
	batchID := created["batch_id"].(string)
	var final map[string]any
	for range 6 {
		_, _, final = f.Do("GET", Prefix+"/batches/"+batchID, key, nil, FileFaultsHeader, "1:rejected,2:failed")
		if final["batch_status"] == "SUCCESS" {
			break
		}
	}
	if status := taskStatuses(t, final); final["batch_status"] != "SUCCESS" || !slices.Equal(status, []string{"SUCCEEDED", "SUCCEEDED"}) {
		t.Fatalf("a poll changed the batch: %v", final)
	}
}

func TestTaskCanceledScenarioEndsTheBatchCanceled(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, created := submit(f, t, "X-Fake-Scenario", "task-canceled")
	final := pollUntil(f, t, created["batch_id"].(string), "CANCELED")
	task := testkit.Field(t, final, "tasks", 0).(map[string]any)
	if task["status"] != "CANCELED" || task["downloadable"] != false {
		t.Fatalf("task = %v", task)
	}
	if count(t, final, "accepted_count") != 1 || count(t, final, "rejected_count") != 0 {
		t.Fatalf("counts = %v", final)
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
	t.Run("download answers the request faults", func(t *testing.T) {
		f := newFixture(t, short)
		_, created := submit(f, t)
		path := Prefix + "/tasks/" + testkit.Field(t, created, "tasks", 0, "task_id").(string) + "/download"
		if status, header, _ := f.Raw("GET", path, key, nil, "X-Fake-Scenario", "rate-limit"); status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" {
			t.Fatalf("rate limit: %d %s", status, header.Get("Retry-After"))
		}
		if status, _, _ := f.Raw("GET", path, key, nil, "X-Fake-Scenario", "server-error"); status != http.StatusInternalServerError {
			t.Fatalf("server error: %d", status)
		}
		request, _ := http.NewRequest("GET", f.Server.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "timeout")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("the timeout response should be dropped, got %d", response.StatusCode)
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
