package mattingapiv1

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // decoders for the picture formats the Offering accepts, so an upload's size can be read
	_ "image/png"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

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

// outOfMemoryObserved is the date the service was seen failing a task for want
// of GPU memory (#299): a batch of a 2048x2048 and a 16x16 picture, both process
// resolutions at 2048, on a GPU other tenants were already using, failed ten
// seconds after it started, and the failed task's error_message was
// OutOfMemoryMessage's text with the size of the picture it names. The batch's
// status word and what the other picture's task showed were not recorded.
const outOfMemoryObserved = "2026-11-13"

// docs: the service is the operator's own and publishes no documentation; its
// FastAPI OpenAPI page is the reference.
const docs = "internal matting service (FastAPI /docs)"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/jobs", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v1/jobs",
		DocURL: docs, CheckedAt: captured,
		Note: "multipart request (files + pipeline_params_json); 401 {detail}, 422 validation list and the READY batch envelope are real samples. One task per uploaded files part, the REJECTED task and the PARTIAL_SUCCESS/all-rejected batches are constructed from the " + docs + " SubmitBatchResponse/TaskStatusItem/BatchStatus enums checked " + statusesChecked + ": the samples carry one accepted file per batch and no rejected one. The 422 for a process-resolution value outside the service's range is constructed too: the ranges are the PipelineParams bounds read from the service source on " + limitsChecked + " (birefnet_process_res 512..4096, sdmatte_process_res 512..2048), the body is the FastAPI/pydantic validation list the committed 422 sample already shows (type, loc, msg, input) plus pydantic's ctx bound; the loc prefix (body, pipeline_params_json) was not observed from the live service. Which files of a batch are rejected, fail while processing or come back under another source_name is chosen by the test-only " + FileFaultsHeader + " header (the partial-images scenario rejects every file but the first): the service's own reasons for refusing a file, and whether it echoes the name of such a file, were never observed, and the one reason it gave for failing a file was seen once (a GPU out of memory, see the batch route)"},
	{Method: "GET", Path: "/batches/{batch_id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/batches/{id}",
		DocURL: docs, CheckedAt: captured,
		Note: "READY, RUNNING and SUCCESS polls and the 404 {detail} are real. The FAILED batch (task FAILED with error_message), the PARTIAL_SUCCESS batch (some files REJECTED or FAILED), the completed batch with no artifact (every file REJECTED or FAILED) and the CANCELED batch (batch and its tasks CANCELED) were never observed: they are built from the SUCCESS poll with the " + docs + " BatchStatus/TaskStatus enums checked " + statusesChecked + ". RECEIVED/STORED are accepted states the service's samples never showed (the create route answers READY) and the Adapter treats them like READY, so the fake does not produce them. One reason a task fails is real: on " + outOfMemoryObserved + " the service failed a batch of a 2048x2048 and a 16x16 picture, both process resolutions at 2048, on a GPU other tenants were already using (#299), and the failed task's error_message was OutOfMemoryMessage's text (a " + FileFaultsHeader + " fault of kind gpu_oom reproduces it). Only that message was recorded: the batch's status word, what the other picture's task showed, and which resolutions the GPU can hold are unknown, so the fake does not claim them"},
	{Method: "GET", Path: "/tasks/{task_id}/download", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/tasks/{id}/download",
		DocURL: docs, CheckedAt: captured,
		Note: "authenticated download of the PNG result (image/png); 401 {detail} without a key. The service has no signed URLs, so an expired-URL scenario does not apply to it. Request-phase faults (429/500/timeout) answer here like on the other routes; a REJECTED task, and the task of a file that fails (" + FileFaultsHeader + "), has no artifact and answers 404 (constructed from the TaskStatus enum checked " + statusesChecked + ")"},
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
// input validation refused the file, failed that processing it fails once the
// batch finishes; neither task has an artifact. outOfMemory says a failed file
// fails for want of GPU memory, and width and height are the size read from the
// upload (zero when it is not a picture the fake can read). renamed records that
// the service answers the file under a name of its own.
type batchTask struct {
	id            string
	name          string
	rejected      bool
	failed        bool
	outOfMemory   bool
	width, height int
	renamed       bool
}

// genericFailure is the error_message of a task that fails with no reason the
// service was ever seen to give.
const genericFailure = "Fake matting failure."

// OutOfMemoryMessage is the error_message the service gave the task of a
// picture its GPU could not hold, verbatim as seen on outOfMemoryObserved (#299)
// except for the pixel size, which is the picture's own. A size the fake could
// not read leaves out the size clause: the service always reads the size, so
// that form was never observed.
func OutOfMemoryMessage(width, height int) string {
	size := ""
	if width > 0 && height > 0 {
		size = fmt.Sprintf("（图片尺寸: %d×%d）", width, height)
	}
	return "GPU 显存不足，无法处理该图片" + size + "。建议：使用更大显存的 GPU，或降低 BIREFNET_PROCESS_RES / SDMATTE_PROCESS_RES 配置。"
}

// failureMessage is the error_message of the task once it is FAILED.
func (t batchTask) failureMessage() string {
	if t.outOfMemory {
		return OutOfMemoryMessage(t.width, t.height)
	}
	return genericFailure
}

// renamedSource is the source_name of a renamed file. It is not a name the
// Adapter's upload filename template produces, so the position of the file
// cannot be read back from it.
const renamedSource = "renamed-by-the-service.png"

// echoedName is the source_name the service answers the file with.
func (t batchTask) echoedName() string {
	if t.renamed {
		return renamedSource
	}
	return t.name
}

// FileFaultsHeader selects what becomes of individual files of a batch, on the
// request that creates it (the batch keeps it for every later poll). The value
// is a comma-separated list of <position>:<fault> entries: positions are
// 1-based in upload order and a fault is one of
//
//   - rejected: the service's input validation refuses the file. The task is
//     REJECTED in the create response already, with an invalid_reason, and has no
//     artifact (ScenarioPartialImages rejects every file but the first the same
//     way);
//   - failed: processing the file fails. The task shows the state of the others
//     (READY, RUNNING) and ends FAILED, with an error_message and no artifact,
//     once the batch finishes;
//   - gpu_oom: processing the file fails for want of GPU memory, the one
//     failure reason seen from the live service (#299, outOfMemoryObserved). It
//     is a failed whose error_message is OutOfMemoryMessage: the service's
//     out-of-memory text, naming the size the fake reads from the uploaded
//     picture (a file that is not a readable picture is named without a size);
//   - renamed: the service answers the file under a name of its own, so its
//     source_name is not the name that was uploaded.
//
// A file may carry several faults; rejected wins over failed. An entry the fake
// does not understand (an unknown word, a position that is not a file of the
// batch) is ignored. A batch in which every file succeeds ends SUCCESS, any other
// one PARTIAL_SUCCESS. Test-only and constructed from the same enums as the
// rest of the batch states (see the endpoint notes): the service's own reasons
// for refusing a file, and whether it echoes the name of such a file, were never
// observed, and its reason for failing one only once (gpu_oom).
const FileFaultsHeader = "X-Fake-File-Faults"

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
	faults := fileFaults(r, scenario, len(names))
	h.mu.Lock()
	number := len(h.batches) + 1
	b := &batch{id: fmt.Sprintf("batch_fake%04d", number), scenario: scenario}
	for index, name := range names {
		b.tasks = append(b.tasks, batchTask{
			id:          fmt.Sprintf("task_fake%04d_%02d", number, index+1),
			name:        name,
			rejected:    faults[index].rejected,
			failed:      faults[index].failed && !faults[index].rejected,
			outOfMemory: faults[index].outOfMemory && !faults[index].rejected,
			width:       form.sizes[index].width,
			height:      form.sizes[index].height,
			renamed:     faults[index].renamed,
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

// fileFault is what is wrong with one file of a batch (see FileFaultsHeader).
type fileFault struct{ rejected, failed, outOfMemory, renamed bool }

// fileFaults says what is wrong with each of the total files of a batch: what
// the request's FileFaultsHeader names, and under ScenarioPartialImages the
// rejection of every file but the first; a single-file batch is rejected whole,
// so a completed batch with no artifact is reachable too (the Adapter's
// no_artifacts branch). Constructed: the committed samples carry no rejected
// file.
func fileFaults(r *http.Request, scenario fakeupstream.Scenario, total int) []fileFault {
	faults := make([]fileFault, total)
	for _, entry := range strings.Split(r.Header.Get(FileFaultsHeader), ",") {
		position, word, found := strings.Cut(entry, ":")
		index, err := strconv.Atoi(strings.TrimSpace(position))
		if !found || err != nil || index < 1 || index > total {
			continue
		}
		switch strings.TrimSpace(word) {
		case "rejected":
			faults[index-1].rejected = true
		case "failed":
			faults[index-1].failed = true
		case "gpu_oom":
			faults[index-1].failed, faults[index-1].outOfMemory = true, true
		case "renamed":
			faults[index-1].renamed = true
		}
	}
	if scenario == fakeupstream.ScenarioPartialImages {
		for index := range faults {
			if index > 0 || total == 1 {
				faults[index].rejected = true
			}
		}
	}
	return faults
}

// upload is what the create route reads from the multipart request: the file
// names of the files parts, the size of each picture and the raw
// pipeline_params_json field.
type upload struct {
	names    []string
	sizes    []pictureSize
	pipeline string
}

// pictureSize is the pixel size of an uploaded picture; zero when its header
// cannot be read.
type pictureSize struct{ width, height int }

// headerRead bounds how much of a file is looked at for its size: every
// supported format states it in the first bytes, a JPEG after any metadata
// segments in front of it.
const headerRead = 1 << 20

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
			head, _ := io.ReadAll(io.LimitReader(part, headerRead))
			_, _ = io.Copy(io.Discard, part)
			result.names = append(result.names, part.FileName())
			result.sizes = append(result.sizes, sizeOf(head))
		case part.FormName() == PipelineField && part.FileName() == "":
			raw, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			result.pipeline = string(raw)
		default:
			_, _ = io.Copy(io.Discard, part)
		}
	}
	return result, len(result.names) > 0
}

// sizeOf reads the pixel size from the start of a picture file; zero when it is
// not a picture the fake can read.
func sizeOf(head []byte) pictureSize {
	config, _, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		return pictureSize{}
	}
	return pictureSize{width: config.Width, height: config.Height}
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
		task["task_id"], task["source_index"], task["source_name"] = entry.id, index, entry.echoedName()
		if entry.rejected {
			rejected++
			task["status"], task["downloadable"] = "REJECTED", false
			task["error_message"], task["invalid_reason"] = nil, "Fake matting rejection."
			task["download_path"] = ""
			tasks = append(tasks, task)
			continue
		}
		accepted++
		status := taskStatus
		if entry.failed && status == "SUCCEEDED" {
			// A file that fails shows it only once the batch has finished.
			status = "FAILED"
		}
		task["status"] = status
		task["downloadable"] = status == "SUCCEEDED"
		task["download_path"] = samplePrefix + "/tasks/" + entry.id + "/download"
		task["error_message"], task["invalid_reason"] = nil, nil
		if status == "FAILED" {
			task["error_message"] = entry.failureMessage()
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
	case b.incomplete():
		// every accepted file succeeds unless it fails and every rejected one
		// stays REJECTED, so a batch with at least one file that succeeds ends
		// PARTIAL_SUCCESS and one without any ends completed with no artifact.
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "PARTIAL_SUCCESS", "SUCCEEDED"))
	default:
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "SUCCESS", "SUCCEEDED"))
	}
}

// incomplete reports whether some file of the batch produces no artifact: the
// service's input validation refused it, or processing it fails.
func (b *batch) incomplete() bool {
	for _, task := range b.tasks {
		if task.rejected || task.failed {
			return true
		}
	}
	return false
}

// lacksArtifact reports whether the task of this batch never has an artifact:
// its file was refused by the service's input validation or fails.
func (b *batch) lacksArtifact(taskID string) bool {
	for _, task := range b.tasks {
		if task.id == taskID {
			return task.rejected || task.failed
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
	noArtifact := b != nil && b.lacksArtifact(taskID)
	h.mu.Unlock()
	if b == nil || noArtifact || b.scenario == fakeupstream.ScenarioArtifactNotFound {
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
