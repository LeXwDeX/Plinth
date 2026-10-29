package mattingapiv1

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// docs: the service is the operator's own and publishes no documentation; its
// FastAPI OpenAPI page is the reference.
const docs = "internal matting service (FastAPI /docs)"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/jobs", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v1/jobs",
		DocURL: docs, CheckedAt: captured,
		Note: "multipart request (files + pipeline_params_json); 401 {detail}, 422 validation list and the READY batch envelope are real samples"},
	{Method: "GET", Path: "/batches/{batch_id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/batches/{id}",
		DocURL: docs, CheckedAt: captured,
		Note: "READY, RUNNING and SUCCESS polls and the 404 {detail} are real. The FAILED batch (task FAILED with error_message) was never observed: it is built from the SUCCESS poll with the task failed and not downloadable"},
	{Method: "GET", Path: "/tasks/{task_id}/download", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v1/tasks/{id}/download",
		DocURL: docs, CheckedAt: captured,
		Note: "authenticated download of the PNG result (image/png); 401 {detail} without a key. The service has no signed URLs, so an expired-URL scenario does not apply to it"},
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
	taskID   string
	scenario fakeupstream.Scenario
	polls    int
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
	names, ok := uploadedNames(r)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, h.invalid)
		return
	}
	h.mu.Lock()
	id := fmt.Sprintf("batch_fake%04d", len(h.batches)+1)
	b := &batch{id: id, taskID: fmt.Sprintf("task_fake%04d", len(h.batches)+1), scenario: fakeupstream.ScenarioOf(r)}
	h.batches[id], h.byTask[b.taskID] = b, b
	h.mu.Unlock()
	if b.scenario == fakeupstream.ScenarioLostResponse {
		fakeupstream.AbortConnection(w) // the batch exists; the response is lost
		return
	}
	writeJSON(w, http.StatusOK, h.envelope(h.ready, b, "READY", "READY", names))
}

// uploadedNames reads the file names of the uploaded parts; the request is
// valid when at least one file arrived.
func uploadedNames(r *http.Request) ([]string, bool) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, false
	}
	reader := multipart.NewReader(io.LimitReader(r.Body, 128<<20), params["boundary"])
	var names []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		_, _ = io.Copy(io.Discard, part)
		if part.FormName() == "files" && part.FileName() != "" {
			names = append(names, part.FileName())
		}
	}
	return names, len(names) > 0
}

// envelope clones a sample batch and fills the dynamic fields: the ids, the
// statuses and the download flag of its one task.
func (h *handler) envelope(template map[string]any, b *batch, batchStatus, taskStatus string, names []string) map[string]any {
	body := fakeupstream.Clone(template)
	body["batch_id"], body["batch_status"] = b.id, batchStatus
	tasks, _ := body["tasks"].([]any)
	if len(tasks) == 0 {
		return body
	}
	task, _ := tasks[0].(map[string]any)
	task["task_id"], task["status"] = b.taskID, taskStatus
	task["download_path"] = samplePrefix + "/tasks/" + b.taskID + "/download"
	task["downloadable"] = taskStatus == "SUCCEEDED"
	if len(names) > 0 {
		task["source_name"] = names[0]
	}
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
		writeJSON(w, http.StatusOK, h.envelope(h.ready, b, "READY", "READY", nil))
	case n <= h.env.QueuedPolls+h.env.RunningPolls:
		writeJSON(w, http.StatusOK, h.envelope(h.running, b, "RUNNING", "RUNNING", nil))
	case b.scenario == fakeupstream.ScenarioTaskFailed:
		body := h.envelope(h.success, b, "FAILED", "FAILED", nil)
		if tasks, _ := body["tasks"].([]any); len(tasks) > 0 {
			task, _ := tasks[0].(map[string]any)
			task["error_message"] = "Fake matting failure."
		}
		writeJSON(w, http.StatusOK, body)
	default:
		writeJSON(w, http.StatusOK, h.envelope(h.success, b, "SUCCESS", "SUCCEEDED", nil))
	}
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	h.mu.Lock()
	b := h.byTask[r.PathValue("task_id")]
	h.mu.Unlock()
	if b == nil || b.scenario == fakeupstream.ScenarioArtifactNotFound {
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
