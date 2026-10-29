// Package core is the engine the Meshy route-version fakes (meshy/openapi-v1,
// meshy/openapi-v2) share. A version supplies its route table (Kind) and its
// committed real samples; the engine owns the task store, the task state
// machine, fault injection and the artifact files. Task bodies are derived from
// the samples: the engine only fills the dynamic fields (ids, status, progress
// and artifact URLs). Values are fixed; only the structure is promised.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// Task statuses of the official API.
const (
	StatusPending    = "PENDING"
	StatusInProgress = "IN_PROGRESS"
	StatusSucceeded  = "SUCCEEDED"
	StatusFailed     = "FAILED"
)

// MaxRigFaces is the input face limit the official Rigging documentation names
// (https://docs.meshy.ai/en/api/rigging-and-animation, 2026-10-29).
const MaxRigFaces = 320000

// Kind is one task-creating route of a version.
type Kind struct {
	// Path is relative to the version prefix, for example "/remesh".
	Path string
	// Type is the task type the poll body reports; TypeOf, when set, derives it
	// from the request (text-to-3d reports preview or refine).
	Type   string
	TypeOf func(request map[string]any) string
	// Need lists groups of alternative request fields; the request must carry
	// at least one field of every group.
	Need [][]string
	// Sources are request fields that name another task of the account.
	Sources []string
	// Fidelity is the structure the delivered model files carry ("skeleton" for
	// a rigged character, "animation" for keyframes, "" for plain geometry).
	Fidelity string
	// Real marks kinds with a captured sample of their own.
	Real bool
	// Patch, when set, edits a poll body before it is sent (keys the sample
	// sanitizer cannot hold).
	Patch func(body map[string]any)
}

// Config assembles one version's handler.
type Config struct {
	Env     *fakeupstream.Env
	Samples *fakeupstream.Samples
	// SamplePrefix is the route prefix the committed samples record
	// ("/openapi/v1").
	SamplePrefix string
	// Prefix is where the fake mounts this version ("/meshy/openapi/v1"); the
	// artifact URLs it writes live below it.
	Prefix  string
	Kinds   []Kind
	Balance bool
	// Store is shared by every Meshy version of one fake.
	Store *Store
}

// Store holds the tasks of one fake across the Meshy route versions.
type Store struct {
	mu      sync.Mutex
	tasks   map[string]*task
	counter atomic.Int64
	nonce   string
}

var stores sync.Map // *fakeupstream.Env -> *Store

// SharedStore returns the task store of an Env, so v1 and v2 handlers of the
// same fake see each other's tasks (Meshy lets a v1 task take a v2 task as its
// input).
func SharedStore(env *fakeupstream.Env) *Store {
	if store, ok := stores.Load(env); ok {
		return store.(*Store)
	}
	store, _ := stores.LoadOrStore(env, &Store{
		tasks: map[string]*task{}, nonce: fmt.Sprintf("%08x", uint32(env.Now().UnixMilli())),
	})
	return store.(*Store)
}

type task struct {
	id       string
	kind     Kind
	typ      string
	request  map[string]any
	scenario fakeupstream.Scenario
	polls    int
	// faces is the triangle count of the model the task delivers (0: none).
	faces int
	// failure is set when the task ends FAILED.
	failure *taskError
}

type taskError struct {
	Type, Code, Message string
}

type engine struct {
	cfg   Config
	mux   *http.ServeMux
	kinds map[string]Kind
	// polls[type][status] is a real poll body.
	polls map[string]map[string]map[string]any
	// creates[path] is the real create response.
	creates   map[string]createSample
	balance   map[string]any
	deleteSam *wirefixture.Exchange
	// errors are the real error bodies by status.
	errors map[int]map[string]any
}

type createSample struct {
	status int
	body   map[string]any
}

// New builds the version's handler.
func New(cfg Config) (http.Handler, error) {
	e := &engine{
		cfg: cfg, mux: http.NewServeMux(), kinds: map[string]Kind{},
		polls: map[string]map[string]map[string]any{}, creates: map[string]createSample{},
		errors: map[int]map[string]any{},
	}
	for _, exchange := range cfg.Samples.Exchanges {
		if err := e.index(exchange); err != nil {
			return nil, err
		}
	}
	if cfg.Balance && e.balance == nil {
		return nil, fmt.Errorf("meshy: samples lack the balance exchange")
	}
	for _, status := range []int{400, 401, 404} {
		if e.errors[status] == nil {
			return nil, fmt.Errorf("meshy: samples lack a %d error body", status)
		}
	}
	for _, kind := range cfg.Kinds {
		e.kinds[kind.Path] = kind
		if _, ok := e.creates[kind.Path]; !ok {
			return nil, fmt.Errorf("meshy: samples lack the create response of %s", kind.Path)
		}
		for _, status := range []string{StatusInProgress, StatusSucceeded} {
			if e.pollTemplate(kind.Type, status) == nil && kind.TypeOf == nil {
				return nil, fmt.Errorf("meshy: samples lack a %s poll of %s", status, kind.Type)
			}
		}
		e.mux.HandleFunc("POST "+kind.Path, e.create(kind))
		e.mux.HandleFunc("GET "+kind.Path+"/{id}", e.get(kind))
		e.mux.HandleFunc("DELETE "+kind.Path+"/{id}", e.delete(kind))
	}
	if cfg.Balance {
		e.mux.HandleFunc("GET /balance", e.getBalance)
	}
	e.mux.HandleFunc("GET /assets/{task_id}/{name}", e.getAsset)
	return e.mux, nil
}

func (e *engine) index(exchange wirefixture.Exchange) error {
	apiPath := strings.TrimPrefix(exchange.Request.Path, e.cfg.SamplePrefix)
	if apiPath == exchange.Request.Path {
		return nil // another route version's exchange
	}
	if exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
		if exchange.Request.Method == http.MethodDelete && exchange.Response.Status == http.StatusOK {
			copy := exchange
			e.deleteSam = &copy
		}
		return nil
	}
	body, err := fakeupstream.JSONBody(exchange)
	if err != nil {
		return err
	}
	status := exchange.Response.Status
	switch {
	case exchange.Request.Method == http.MethodGet && apiPath == "/balance" && status == 200:
		e.balance = body
	case status >= 400 && len(body) == 1 && body["message"] != nil:
		if e.errors[status] == nil {
			e.errors[status] = body
		}
	case exchange.Request.Method == http.MethodPost && status == http.StatusAccepted:
		if _, has := e.creates[apiPath]; !has {
			e.creates[apiPath] = createSample{status: status, body: body}
		}
	case exchange.Request.Method == http.MethodGet && status == http.StatusOK:
		typ, _ := body["type"].(string)
		state, _ := body["status"].(string)
		if typ == "" || state == "" {
			return nil
		}
		if e.polls[typ] == nil {
			e.polls[typ] = map[string]map[string]any{}
		}
		if e.polls[typ][state] == nil {
			e.polls[typ][state] = body
		}
	}
	return nil
}

// pollTemplate returns the real poll body of a type and status.
func (e *engine) pollTemplate(typ, status string) map[string]any {
	return e.polls[typ][status]
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// errorBody is the {message} envelope of an error. Statuses the alignment run
// never provoked (402, 429, 5xx) share the structure of the observed 4xx body.
func (e *engine) errorBody(status int, message string) map[string]any {
	template := e.errors[status]
	if template == nil {
		template = e.errors[http.StatusBadRequest]
	}
	body := fakeupstream.Clone(template)
	body["message"] = message
	return body
}

func (e *engine) fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, e.errorBody(status, message))
}

// render writes the Meshy-shaped error of an injected scenario.
func (e *engine) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		e.fail(w, http.StatusPaymentRequired, "Insufficient funds")
	case fakeupstream.ScenarioKeyQuota:
		e.fail(w, http.StatusPaymentRequired, "API key credit limit reached")
	case fakeupstream.ScenarioRateLimit:
		w.Header().Set("Retry-After", e.cfg.Env.RetryAfterSeconds())
		e.fail(w, http.StatusTooManyRequests, "Too many requests")
	default:
		e.fail(w, http.StatusInternalServerError, "Internal server error")
	}
}

// auth answers 401 unless the request carries a bearer key.
func (e *engine) auth(w http.ResponseWriter, r *http.Request) bool {
	if fakeupstream.BearerKey(r) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		e.fail(w, http.StatusUnauthorized, "Missing API key")
	} else {
		e.fail(w, http.StatusUnauthorized, "Invalid API key")
	}
	return false
}

func validID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
			return false
		}
	}
	return true
}

func (s *Store) newID() string {
	n := s.counter.Add(1)
	return fmt.Sprintf("%s-0000-7000-8000-%012d", s.nonce, n)
}

func (s *Store) lookup(id string) *task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id]
}

// ---- endpoints ----

func (e *engine) getBalance(w http.ResponseWriter, r *http.Request) {
	if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseRead, e.render) {
		return
	}
	body := fakeupstream.Clone(e.balance)
	for _, key := range []string{"balance", "monthly_credits", "permanent_credits"} {
		if _, has := body[key]; has {
			body[key] = json.Number("1000")
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func has(request map[string]any, field string) bool {
	switch typed := request[field].(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	}
	return true
}

func (e *engine) create(kind Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseCreate, e.render) {
			return
		}
		var request map[string]any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 128<<20))
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil || request == nil {
			e.fail(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		for _, group := range kind.Need {
			present := false
			for _, field := range group {
				present = present || has(request, field)
			}
			if !present {
				e.fail(w, http.StatusBadRequest, "Either "+strings.Join(group, " or ")+" must be provided")
				return
			}
		}
		store := e.cfg.Store
		t := &task{id: store.newID(), kind: kind, typ: kind.Type, request: request, scenario: fakeupstream.ScenarioOf(r)}
		if kind.TypeOf != nil {
			t.typ = kind.TypeOf(request)
		}
		// A source task must exist and have succeeded.
		for _, field := range kind.Sources {
			source, _ := request[field].(string)
			if source == "" {
				continue
			}
			if !validID(source) {
				e.fail(w, http.StatusBadRequest, "Invalid ID")
				return
			}
			found := store.lookup(source)
			if found == nil {
				e.fail(w, http.StatusNotFound, "Task not found")
				return
			}
			if e.statusOf(found) != StatusSucceeded {
				e.fail(w, http.StatusBadRequest, "The input task has not succeeded")
				return
			}
			t.faces = found.faces
		}
		t.faces = facesOf(t)
		if kind.Path == "/rigging" {
			// The official API accepts an oversized model and fails the task when
			// it runs (observed 2026-10-29; see the alignment report).
			if t.faces > MaxRigFaces {
				t.failure = &taskError{Type: "invalid_input", Code: "invalid_input", Message: "This model could not be rigged. Please check that the mesh is a single, well-formed humanoid character."}
			}
		}
		if t.scenario == fakeupstream.ScenarioTaskFailed && t.failure == nil {
			t.failure = &taskError{Type: "invalid_input", Code: "moderation_blocked", Message: "Fake injected task failure"}
		}
		store.mu.Lock()
		store.tasks[t.id] = t
		store.mu.Unlock()
		if t.scenario == fakeupstream.ScenarioLostResponse {
			fakeupstream.AbortConnection(w)
			return
		}
		sample := e.creates[kind.Path]
		response := fakeupstream.Clone(sample.body)
		response["result"] = t.id
		writeJSON(w, sample.status, response)
	}
}

// facesOf estimates the triangle count of the model a task delivers: the
// requested polycount (a quad mesh counts twice, as triangles), else the
// source's, else a high default for an untouched generation.
func facesOf(t *task) int {
	count := 0
	if number, ok := t.request["target_polycount"].(json.Number); ok {
		if parsed, err := number.Int64(); err == nil {
			count = int(parsed)
		}
	}
	if count == 0 {
		count = t.faces
	}
	if count == 0 {
		count = 30000
	}
	if topology, _ := t.request["topology"].(string); topology == "quad" {
		count *= 2
	}
	return count
}

// statusOf is the status of the task's latest poll answer (Pending before the
// first): a task is Succeeded or Failed only once a poll has said so.
func (e *engine) statusOf(t *task) string {
	e.cfg.Store.mu.Lock()
	n := t.polls
	e.cfg.Store.mu.Unlock()
	return e.statusAt(t, n)
}

func (e *engine) statusAt(t *task, n int) string {
	switch {
	case n <= e.cfg.Env.QueuedPolls:
		return StatusPending
	case n <= e.cfg.Env.QueuedPolls+e.cfg.Env.RunningPolls:
		return StatusInProgress
	case t.failure != nil:
		return StatusFailed
	}
	return StatusSucceeded
}

func (e *engine) lookupFor(w http.ResponseWriter, r *http.Request, kind Kind) *task {
	id := r.PathValue("id")
	if !validID(id) {
		e.fail(w, http.StatusBadRequest, "Invalid ID")
		return nil
	}
	t := e.cfg.Store.lookup(id)
	if t == nil {
		e.fail(w, http.StatusNotFound, "Task not found")
		return nil
	}
	return t
}

func (e *engine) get(kind Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseRead, e.render) {
			return
		}
		t := e.lookupFor(w, r, kind)
		if t == nil {
			return
		}
		store := e.cfg.Store
		store.mu.Lock()
		t.polls++
		n := t.polls
		store.mu.Unlock()
		status := e.statusAt(t, n)
		if status == StatusPending || status == StatusInProgress {
			// Observed on every non-final poll (the final one carries no header).
			w.Header().Set("Retry-After", "5")
		}
		writeJSON(w, http.StatusOK, e.pollBody(r, t, status))
	}
}

func (e *engine) delete(kind Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseRead, e.render) {
			return
		}
		t := e.lookupFor(w, r, kind)
		if t == nil {
			return
		}
		// A task that is running cannot be deleted (documented; not provoked in the
		// alignment run, which deleted finished tasks only).
		if e.statusOf(t) == StatusInProgress {
			e.fail(w, http.StatusConflict, "The task is in progress and cannot be deleted")
			return
		}
		e.cfg.Store.mu.Lock()
		delete(e.cfg.Store.tasks, t.id)
		e.cfg.Store.mu.Unlock()
		if e.deleteSam != nil {
			if contentType := e.deleteSam.Response.Headers["content-type"]; contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ---- task bodies ----

func (e *engine) pollBody(r *http.Request, t *task, status string) map[string]any {
	template := e.pollTemplate(t.typ, status)
	if template == nil {
		// PENDING and FAILED bodies share the structure of the running body; a
		// failed one carries the error object.
		template = e.pollTemplate(t.typ, StatusInProgress)
		if template == nil {
			template = e.anyTemplate(t.typ)
		}
	}
	body := fakeupstream.Clone(template)
	body["id"], body["status"] = t.id, status
	switch status {
	case StatusPending:
		body["progress"] = json.Number("0")
	case StatusInProgress:
		body["progress"] = json.Number("50")
	default:
		body["progress"] = json.Number("100")
	}
	if status == StatusFailed {
		failure := t.failure
		if failure == nil {
			failure = &taskError{Type: "server_error", Message: "Fake injected task failure"}
		}
		errObject := map[string]any{"type": failure.Type, "message": failure.Message}
		if failure.Code != "" {
			errObject["code"] = failure.Code
			errObject["doc_url"] = "https://docs.meshy.ai/en/api/errors#" + strings.ReplaceAll(failure.Code, "_", "-")
		}
		body["task_error"] = errObject
		body["consumed_credits"] = json.Number("0")
		return body
	}
	if status == StatusSucceeded {
		body = e.fillURLs(r, t, body)
		if t.kind.Patch != nil {
			t.kind.Patch(body)
		}
	}
	return body
}

func (e *engine) anyTemplate(typ string) map[string]any {
	for _, byStatus := range e.polls {
		for _, body := range byStatus {
			return body
		}
	}
	return nil
}

// fillURLs replaces every artifact URL of a success body with a URL of this
// fake. A model_urls object follows the formats the request asked for.
func (e *engine) fillURLs(r *http.Request, t *task, body map[string]any) map[string]any {
	origin := e.cfg.Env.ArtifactOriginFor(r) + e.cfg.Prefix + "/assets/" + t.id + "/"
	var walk func(trail []string, value any) any
	walk = func(trail []string, value any) any {
		switch typed := value.(type) {
		case string:
			if strings.HasPrefix(typed, "http") {
				return origin + artifactName(trail, typed)
			}
			return typed
		case map[string]any:
			if len(trail) == 1 && trail[0] == "model_urls" && len(typed) > 0 {
				typed = e.modelURLs(t, typed)
			}
			out := make(map[string]any, len(typed))
			for key, child := range typed {
				out[key] = walk(append(append([]string(nil), trail...), key), child)
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for index, child := range typed {
				out[index] = walk(append(append([]string(nil), trail...), fmt.Sprint(index)), child)
			}
			return out
		}
		return value
	}
	return walk(nil, body).(map[string]any)
}

// modelURLs makes a model_urls object follow the request's target_formats: one
// key per requested format, plus the material library an OBJ comes with.
func (e *engine) modelURLs(t *task, template map[string]any) map[string]any {
	formats, _ := t.request["target_formats"].([]any)
	if len(formats) == 0 {
		return template
	}
	sample := ""
	for _, value := range template {
		if text, ok := value.(string); ok {
			sample = text
			break
		}
	}
	out := map[string]any{}
	for _, format := range formats {
		name, _ := format.(string)
		if name == "" {
			continue
		}
		out[name] = sample
		if name == "obj" {
			out["mtl"] = sample
		}
	}
	return out
}

// artifactName names the file of a URL field: the field path, with the
// extension the format key (or the real sample's URL) carries.
func artifactName(trail []string, sample string) string {
	last := trail[len(trail)-1]
	ext := path.Ext(strings.SplitN(sample, "?", 2)[0])
	if len(trail) >= 2 && trail[len(trail)-2] == "model_urls" {
		ext = "." + last
	}
	if ext == "" || len(sample) < 8 || strings.HasSuffix(sample, "/") {
		ext = extensionOf(last)
	}
	// Formats the fake serves under another extension keep a fixed name.
	return strings.Join(trail, "_") + ext
}

// extensionOf guesses an extension from a field name when no sample URL
// carries one.
func extensionOf(field string) string {
	for _, suffix := range []string{"glb", "fbx", "usdz", "obj", "stl", "3mf", "mtl", "png", "bvh"} {
		if strings.Contains(field, suffix) {
			return "." + suffix
		}
	}
	return ".png"
}

// ---- artifacts ----

// contentTypes are what the official CDN serves, as observed in the alignment
// run (see the version's alignment report).
var contentTypes = map[string]string{}

// SetContentTypes registers the observed content types by extension (".glb").
// A version package calls it once from its fixed table.
func SetContentTypes(types map[string]string) {
	for ext, mediaType := range types {
		contentTypes[ext] = mediaType
	}
}

func (e *engine) getAsset(w http.ResponseWriter, r *http.Request) {
	t := e.cfg.Store.lookup(r.PathValue("task_id"))
	if t == nil || t.scenario == fakeupstream.ScenarioArtifactNotFound {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	file, err := artifactFile(name, t)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := file.Data
	if t.scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	mediaType := contentTypes[path.Ext(name)]
	if mediaType == "" {
		mediaType = file.MediaType
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// artifactFile returns the fixed bytes of an artifact name. A rigged
// character carries a skeleton and an animation clip carries keyframes.
func artifactFile(name string, t *task) (fakeupstream.File, error) {
	fidelity := t.kind.Fidelity
	// Within a rigging result only the animation clips carry keyframes.
	if strings.Contains(name, "animations") || strings.Contains(name, "animation_") && fidelity == "skeleton" {
		fidelity = "animation"
	}
	if strings.Contains(name, "armature") {
		fidelity = "skeleton"
	}
	model := func(format string) (fakeupstream.File, error) {
		if fidelity != "" && (format == "glb" || format == "fbx") {
			return fakeupstream.ModelFile(format + ":" + fidelity)
		}
		return fakeupstream.ModelFile(format)
	}
	switch path.Ext(name) {
	case ".glb":
		return model("glb")
	case ".fbx":
		return model("fbx")
	case ".usdz":
		return fakeupstream.ModelFile("usdz")
	case ".stl":
		return fakeupstream.ModelFile("stl")
	case ".3mf":
		return fakeupstream.ModelFile("3mf")
	case ".obj":
		obj, _, err := fakeupstream.ObjFiles()
		return obj, err
	case ".mtl":
		_, mtl, err := fakeupstream.ObjFiles()
		return mtl, err
	case ".png":
		return fakeupstream.ModelFile("png")
	case ".bvh":
		return fakeupstream.File{MediaType: "application/octet-stream", Data: []byte("HIERARCHY\nROOT Hips\n{\n OFFSET 0 0 0\n CHANNELS 3 Xposition Yposition Zposition\n End Site\n {\n  OFFSET 0 1 0\n }\n}\nMOTION\nFrames: 1\nFrame Time: 0.033333\n0 0 0\n")}, nil
	}
	return fakeupstream.File{}, fmt.Errorf("unknown artifact %q", name)
}

// Digest is a stable hash of a body, for tests.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}
