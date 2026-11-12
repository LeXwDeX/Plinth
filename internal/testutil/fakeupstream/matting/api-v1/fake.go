package mattingapiv1

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

//go:embed testdata/matting-api-v1.candidate.json
var sampleFS embed.FS

const sampleFile = "testdata/matting-api-v1.candidate.json"

// Prefix is where this version is mounted: the stack's MATTING_BASE_URL points
// at <fake>/matting (Plinth appends api/v1/jobs and the other routes).
const Prefix = "/matting/api/v1"

// samplePrefix is the path prefix the service uses; the committed sample
// records it.
const samplePrefix = "/api/v1"

const captured = "2026-10-29"

// statusesChecked is the date the internal FastAPI OpenAPI enums were read.
// PARTIAL_SUCCESS, CANCELED and REJECTED are constructed from them: the
// committed samples only carry READY, RUNNING, SUCCESS and SUCCEEDED.
const statusesChecked = "2026-11-08"

// limitsChecked is the date the service's PipelineParams bounds were read from
// its source (issue #284): the process-resolution fields below.
const limitsChecked = "2026-11-12"

// docs: the service is the operator's own and publishes no documentation; its
// FastAPI OpenAPI page is the reference.
const docs = "internal matting service (FastAPI /docs)"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/jobs", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v1/jobs",
		DocURL: docs, CheckedAt: captured,
		Note: "multipart request (files + pipeline_params_json); 401 {detail}, 422 validation list and the READY batch envelope are real samples. One task per uploaded files part, the REJECTED task and the PARTIAL_SUCCESS/all-rejected batches are constructed from the " + docs + " SubmitBatchResponse/TaskStatusItem/BatchStatus enums checked " + statusesChecked + ": the samples carry one accepted file per batch and no rejected one. The 422 for a process-resolution value outside the service's range is constructed too: the ranges are the PipelineParams bounds read from the service source on " + limitsChecked + " (birefnet_process_res 512..4096, sdmatte_process_res 512..2048), the body is the FastAPI/pydantic validation list the committed 422 sample already shows (type, loc, msg, input) plus pydantic's ctx bound; the loc prefix (body, pipeline_params_json) was not observed from the live service"},
	{Method: "GET", Path: "/batches/{batch_id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/batches/{id}",
		DocURL: docs, CheckedAt: captured,
		Note: "READY, RUNNING and SUCCESS polls and the 404 {detail} are real. The FAILED batch (task FAILED with error_message), the PARTIAL_SUCCESS batch (some files REJECTED), the completed batch with no artifact (every file REJECTED) and the CANCELED batch (batch and its tasks CANCELED) were never observed: they are built from the SUCCESS poll with the " + docs + " BatchStatus/TaskStatus enums checked " + statusesChecked + ". RECEIVED/STORED are accepted states the service's samples never showed (the create route answers READY) and the Adapter treats them like READY, so the fake does not produce them"},
	{Method: "GET", Path: "/tasks/{task_id}/download", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/tasks/{id}/download",
		DocURL: docs, CheckedAt: captured,
		Note: "authenticated download of the PNG result (image/png); 401 {detail} without a key. The service has no signed URLs, so an expired-URL scenario does not apply to it. Request-phase faults (429/500/timeout) answer here like on the other routes; a REJECTED task has no artifact and answers 404 (constructed from the TaskStatus enum checked " + statusesChecked + ")"},
}

// Version is the matting service api/v1 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "matting", API: "api-v1", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

type batch struct {
	id       string
	tasks    []batchTask
	scenario fakeupstream.Scenario
	polls    int
}

// batchTask is one uploaded file's task. rejected records that the service's
// input validation refused the file; such a task has no artifact.
type batchTask struct {
	id       string
	name     string
	rejected bool
}

type handler struct {
	env *fakeupstream.Env

	denied, invalid, missing map[string]any
	ready, running, success  map[string]any

	mu      sync.Mutex
	batches map[string]*batch
	byTask  map[string]*batch
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		return nil, err
	}
	h := &handler{env: env, batches: map[string]*batch{}, byTask: map[string]*batch{}}
	for _, exchange := range samples.Find("POST "+samplePrefix+"/jobs", "") {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return nil, err
		}
		switch exchange.Response.Status {
		case http.StatusOK:
			h.ready = body
		case http.StatusUnauthorized:
			h.denied = body
		case http.StatusUnprocessableEntity:
			h.invalid = body
		}
	}
	for _, exchange := range samples.Find("GET "+samplePrefix+"/batches/{id}", "") {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return nil, err
		}
		switch status, _ := body["batch_status"].(string); {
		case exchange.Response.Status == http.StatusNotFound:
			h.missing = body
		case status == "RUNNING":
			h.running = body
		case status == "SUCCESS":
			h.success = body
		}
	}
	if h.ready == nil || h.running == nil || h.success == nil || h.denied == nil || h.invalid == nil || h.missing == nil {
		return nil, errors.New("matting/api-v1 sample lacks a READY, RUNNING, SUCCESS, 401, 422 or 404 exchange")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", h.create)
	mux.HandleFunc("GET /batches/{batch_id}", h.read)
	mux.HandleFunc("GET /tasks/{task_id}/download", h.download)
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// render answers the request-level scenarios. The service has no price, so
// insufficient-balance and key-quota have nothing to say and answer like an
// internal error; rate-limit adds Retry-After.
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	status, detail := http.StatusInternalServerError, "Internal Server Error"
	if scenario == fakeupstream.ScenarioRateLimit {
		status, detail = http.StatusTooManyRequests, "Too Many Requests"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, status, map[string]any{"detail": detail})
}

func authorized(r *http.Request) bool { return fakeupstream.BearerKey(r) != "" }

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	form, ok := readUpload(r)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, h.invalid)
		return
	}
	if problems := pipelineProblems(form.pipeline); len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": problems})
		return
	}
	names := form.names
	scenario := fakeupstream.ScenarioOf(r)
	h.mu.Lock()
	number := len(h.batches) + 1
	b := &batch{id: fmt.Sprintf("batch_fake%04d", number), scenario: scenario}
	for index, name := range names {
		b.tasks = append(b.tasks, batchTask{
			id:       fmt.Sprintf("task_fake%04d_%02d", number, index+1),
			name:     name,
			rejected: rejectedBy(scenario, index, len(names)),
		})
	}
	h.batches[b.id] = b
	for _, task := range b.tasks {
		h.byTask[task.id] = b
	}
	h.mu.Unlock()
	if b.scenario == fakeupstream.ScenarioLostResponse {
		fakeupstream.AbortConnection(w) // the batch exists; the response is lost
		return
	}
	writeJSON(w, http.StatusOK, h.envelope(h.ready, b, "READY", "READY"))
}

// rejectedBy says whether the service's input validation refuses the file at
// index of a batch of total files. Under ScenarioPartialImages the fake
// rejects every file but the first; a single-file batch is rejected whole, so
// a completed batch with no artifact is reachable too (the Adapter's
// no_artifacts branch). Constructed: the committed samples carry no rejected
// file.
func rejectedBy(scenario fakeupstream.Scenario, index, total int) bool {
	return scenario == fakeupstream.ScenarioPartialImages && (index > 0 || total == 1)
}

// upload is what the create route reads from the multipart request: the file
// names of the files parts and the raw pipeline_params_json field.
type upload struct {
	names    []string
	pipeline string
}

// readUpload reads the uploaded parts; the request is valid when at least one
// file arrived.
func readUpload(r *http.Request) (upload, bool) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return upload{}, false
	}
	reader := multipart.NewReader(io.LimitReader(r.Body, 128<<20), params["boundary"])
	var result upload
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		switch {
		case part.FormName() == "files" && part.FileName() != "":
			_, _ = io.Copy(io.Discard, part)
			result.names = append(result.names, part.FileName())
		case part.FormName() == PipelineField && part.FileName() == "":
			raw, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			result.pipeline = string(raw)
		default:
			_, _ = io.Copy(io.Discard, part)
		}
	}
	return result, len(result.names) > 0
}

// PipelineField is the multipart field that carries the pipeline parameters as
// one JSON object.
const PipelineField = "pipeline_params_json"

// ResolutionLimit is the range of one process-resolution field of the
// service's PipelineParams: an integer from Min to Max. A null birefnet value
// is "auto" (the service picks GPU 2048, CPU 1024); sdmatte has a default of
// 1024 instead, so null is not a valid value there.
type ResolutionLimit struct {
	Field    string
	Min, Max int64
	NullOK   bool
}

// resolutionLimits are the bounds the service validates (read from the
// service source on limitsChecked): birefnet_process_res ge=512 le=4096 and
// sdmatte_process_res ge=512 le=2048. A value outside them is HTTP 422. The
// service lowers both to 1024 only when it runs on a CPU; that is not a
// request error and the fake does not model it.
var resolutionLimits = []ResolutionLimit{
	{Field: "birefnet_process_res", Min: 512, Max: 4096, NullOK: true},
	{Field: "sdmatte_process_res", Min: 512, Max: 2048},
}

// ResolutionLimits returns the process-resolution bounds the fake enforces, so
// a test can hold the product Schema's declared ranges against them.
func ResolutionLimits() []ResolutionLimit {
	return append([]ResolutionLimit(nil), resolutionLimits...)
}

// pipelineProblems validates the pipeline parameters the fake knows bounds
// for and answers them as the FastAPI/pydantic validation list. A field that
// is absent takes the service's default; a pipeline that is not a JSON object
// is not judged (the service's reaction to it was never observed).
func pipelineProblems(raw string) []any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil
	}
	var problems []any
	for _, limit := range resolutionLimits {
		value, present := fields[limit.Field]
		if !present || value == nil && limit.NullOK {
			continue
		}
		if problem := limit.problem(value); problem != nil {
			problems = append(problems, problem)
		}
	}
	return problems
}

// problem is the pydantic error of a value that does not fit the limit, or nil.
func (limit ResolutionLimit) problem(value any) map[string]any {
	loc := []any{"body", PipelineField, limit.Field}
	number, isNumber := value.(json.Number)
	if !isNumber {
		return map[string]any{"type": "int_type", "loc": loc, "msg": "Input should be a valid integer", "input": value}
	}
	parsed, err := number.Float64()
	if err != nil {
		return map[string]any{"type": "int_type", "loc": loc, "msg": "Input should be a valid integer", "input": value}
	}
	if parsed != math.Trunc(parsed) {
		return map[string]any{"type": "int_from_float", "loc": loc,
			"msg": "Input should be a valid integer, got a number with a fractional part", "input": value}
	}
	switch {
	case parsed < float64(limit.Min):
		return map[string]any{"type": "greater_than_equal", "loc": loc, "input": value,
			"msg": fmt.Sprintf("Input should be greater than or equal to %d", limit.Min), "ctx": map[string]any{"ge": limit.Min}}
	case parsed > float64(limit.Max):
		return map[string]any{"type": "less_than_equal", "loc": loc, "input": value,
			"msg": fmt.Sprintf("Input should be less than or equal to %d", limit.Max), "ctx": map[string]any{"le": limit.Max}}
	}
	return nil
}

// envelope fills a sample batch with this batch's tasks: the ids, the uploaded
// names with their indexes, the per-task status and the counts. The sample's
// task object supplies the field structure; every task is a clone of it.
func (h *handler) envelope(template map[string]any, b *batch, batchStatus, taskStatus string) map[string]any {
	body := fakeupstream.Clone(template)
	body["batch_id"], body["batch_status"] = b.id, batchStatus
	templateTasks, _ := template["tasks"].([]any)
	taskTemplate, _ := templateTasks[0].(map[string]any)
	if taskTemplate == nil {
		taskTemplate = map[string]any{}
	}
	tasks := make([]any, 0, len(b.tasks))
	accepted, rejected := 0, 0
	for index, entry := range b.tasks {
		task := fakeupstream.Clone(taskTemplate)
		task["task_id"], task["source_index"], task["source_name"] = entry.id, index, entry.name
		if entry.rejected {
			rejected++
			task["status"], task["downloadable"] = "REJECTED", false
			task["error_message"], task["invalid_reason"] = nil, "Fake matting rejection."
			task["download_path"] = ""
			tasks = append(tasks, task)
			continue
		}
		accepted++
		task["status"] = taskStatus
		task["downloadable"] = taskStatus == "SUCCEEDED"
		task["download_path"] = samplePrefix + "/tasks/" + entry.id + "/download"
		task["error_message"], task["invalid_reason"] = nil, nil
		if taskStatus == "FAILED" {
			task["error_message"] = "Fake matting failure."
		}
		tasks = append(tasks, task)
	}
	body["tasks"] = tasks
	body["total_count"], body["accepted_count"], body["rejected_count"] = len(b.tasks), accepted, rejected
	return body
}

func (h *handler) read(w http.ResponseWriter, r *http.Request) {
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	h.mu.Lock()
	b := h.batches[r.PathValue("batch_id")]
	var n int
	if b != nil {
		b.polls++
		n = b.polls
	}
	h.mu.Unlock()
	if b == nil {
		writeJSON(w, http.StatusNotFound, h.missing)
		return
	}
	switch {
	case n <= h.env.QueuedPolls:
		writeJSON(w, http.StatusOK, h.envelope(h.ready, b, "READY", "READY"))
	case n <= h.env.QueuedPolls+h.env.RunningPolls:
		writeJSON(w, http.StatusOK, h.envelope(h.running, b, "RUNNING", "RUNNING"))
	case b.scenario == fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "FAILED", "FAILED"))
	case b.scenario == fakeupstream.ScenarioTaskCanceled:
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "CANCELED", "CANCELED"))
	case b.scenario == fakeupstream.ScenarioPartialImages:
		// every accepted file succeeds and every rejected one stays REJECTED,
		// so a batch with at least one accepted file ends PARTIAL_SUCCESS and
		// an all-rejected one ends completed with no artifact.
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "PARTIAL_SUCCESS", "SUCCEEDED"))
	default:
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "SUCCESS", "SUCCEEDED"))
	}
}

// rejects reports whether the task of this batch was refused by the service's
// input validation; such a task never has an artifact.
func (b *batch) rejects(taskID string) bool {
	for _, task := range b.tasks {
		if task.id == taskID {
			return task.rejected
		}
	}
	return false
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	taskID := r.PathValue("task_id")
	h.mu.Lock()
	b := h.byTask[taskID]
	rejected := b != nil && b.rejects(taskID)
	h.mu.Unlock()
	if b == nil || rejected || b.scenario == fakeupstream.ScenarioArtifactNotFound {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Task not found"})
		return
	}
	file, err := fakeupstream.ImageFile("png")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := file.Data
	if b.scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}
