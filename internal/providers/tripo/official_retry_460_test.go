package tripo_test

// #460 A: Tripo's retry, polling, concurrency and retention rules as the
// official documentation states them (developers.tripo3d.ai, read 2026-11-22):
//
//   - Billing: "Failed and cancelled tasks are not charged."  A format whose
//     convert child failed or was cancelled is sent again (at most five times,
//     1, 2, 4… seconds apart, capped at 32 s) instead of failing the whole run.
//   - Rate limits: code 1007 (request rate) and 2000 (full concurrency pool)
//     are answered with Retry-After; the retry budget belongs to each format.
//   - Task query: "unknown" is neither success nor failure — keep polling, with
//     a timeout, and never resend.
//   - Rate limits: the default pool holds five jobs shared by format-convert,
//     texture and refine.
//   - Files: an upload creates no task, so a failed upload is a retryable
//     transport error, not an ambiguous paid submission.
//   - Asset retention: seven days from 2026-12-01.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// exportFixture is a Tripo v3 upstream whose convert children answer on demand:
// the test decides which format fails, which is refused with 429, and how many
// polls a child stays running before it succeeds.
type exportFixture struct {
	server *httptest.Server

	mu sync.Mutex
	// posts records every convert format in request order.
	posts []string
	// inflightAtPost is the number of children that were submitted and not yet
	// complete when each convert POST arrived; completedAtPost is how many had
	// already delivered a result.
	inflightAtPost  []int
	completedAtPost []int
	// polls counts the polls of each child remote id.
	polls map[string]int
	// failFormats makes a child of that format end failed forever.
	failFormats map[string]bool
	// failCodes is the task-level error_code a failed child of that format
	// reports; absent means the official transient 1001, which the format's
	// retry path may resend (#487). A determinate code (1004, 2008, 2018…)
	// ends the format instead.
	failCodes map[string]int
	// cancelFormats makes the next child of that format end cancelled.
	cancelFormats map[string]bool
	// refusals counts the 429 answers per format; a format below its count is
	// refused, then accepted.
	refusals map[string]int
	// retryAfter is the Retry-After header of a refusal; empty sends none.
	retryAfter string
	// runningPolls is how many polls a child answers "running" before success.
	runningPolls int
	// uploadFailures is how many /files uploads fail with 500 before one works.
	uploadFailures int
	// uploads counts the /files requests.
	uploads int
	// submitted and completed are the child ids by format.
	submitted map[string]string
	completed map[string]bool
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	f := &exportFixture{
		polls:         map[string]int{},
		failFormats:   map[string]bool{},
		failCodes:     map[string]int{},
		cancelFormats: map[string]bool{},
		refusals:      map[string]int{},
		submitted:     map[string]string{},
		completed:     map[string]bool{},
		runningPolls:  1,
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files"):
			f.mu.Lock()
			f.uploads++
			failing := f.uploads <= f.uploadFailures
			f.mu.Unlock()
			if failing {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"code":1000,"message":"Internal server error"}`))
				return
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"file_token": "file_1"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			f.mu.Lock()
			f.posts = append(f.posts, format)
			f.inflightAtPost = append(f.inflightAtPost, len(f.submitted)-len(f.completed))
			f.completedAtPost = append(f.completedAtPost, len(f.completed))
			refused := f.refusals[format] > 0
			if refused {
				f.refusals[format]--
			}
			retryAfter := f.retryAfter
			id := fmt.Sprintf("child-%d-%s", len(f.posts), strings.ToLower(format))
			f.submitted[format] = id
			f.mu.Unlock()
			if refused {
				if retryAfter != "" {
					w.Header().Set("Retry-After", retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":2000,"message":"You have exceeded the limit of generation"}`))
				return
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": id}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/generation/image-to-model"):
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "generated"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/tasks/")
			if id == "base" {
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
					"task_id": "base", "status": "success", "progress": 100,
					"output": map[string]any{"model_url": f.server.URL + "/base.glb"},
				}})
				return
			}
			f.mu.Lock()
			f.polls[id]++
			n := f.polls[id]
			format := ""
			for name, remote := range f.submitted {
				if remote == id {
					format = name
				}
			}
			failing := f.failFormats[format]
			cancelled := f.cancelFormats[format]
			if cancelled {
				delete(f.cancelFormats, format)
			}
			running := n <= f.runningPolls
			f.mu.Unlock()
			status := "success"
			switch {
			case failing:
				status = "failed"
			case cancelled:
				status = "cancelled"
			case running:
				status = "running"
			}
			data := map[string]any{"task_id": id, "status": status, "progress": 50}
			if status == "failed" {
				// A failed task reports its own error_code: 1001 (a transient
				// server error) by default, or the code the test pinned.
				code := f.failCodes[format]
				if code == 0 {
					code = 1001
				}
				data["error_code"] = code
				data["error_message"] = "Fake injected convert failure"
			}
			if status == "success" {
				data["progress"] = 100
				data["output"] = map[string]any{"model_url": f.server.URL + "/" + id + ".glb"}
				data["credits_consumed"] = 5
			}
			if status == "failed" || status == "cancelled" {
				data["credits_consumed"] = 0
			}
			if status == "success" {
				f.completed[format] = true
			}
			writeJSON(w, map[string]any{"code": 0, "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *exportFixture) set(mutate func(*exportFixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(f)
}

func (f *exportFixture) snapshot() ([]string, []int, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...),
		append([]int(nil), f.inflightAtPost...),
		append([]int(nil), f.completedAtPost...)
}

// drive observes until the task reaches a terminal phase or the round budget is
// spent, settling every permit like the kernel's checkpoint does.
func driveExport(
	t *testing.T,
	adapter *tripo.Adapter,
	request generation.ObserveRequest,
	permit *conversionPermit,
	rounds int,
) generation.Observation {
	t.Helper()
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	var observation generation.Observation
	for round := 0; round < rounds; round++ {
		var err error
		observation, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		permit.settle(t, observation)
		request.Opaque = observation.Opaque
		if observation.Phase == generation.UpstreamCompleted || observation.Phase == generation.UpstreamFailed {
			return observation
		}
	}
	return observation
}

// A1: a failed convert child is resubmitted for its own format and the run
// completes; the resubmission is a new private permit with a new remote id, and
// the failed attempt is kept as private evidence with its (zero) credits.
func TestFailedConvertChildIsResubmittedForItsOwnFormat(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) {
		f.failFormats["GLTF"] = true
		f.runningPolls = 0
	})
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	// The GLTF child fails on its first attempt only: the fixture is switched
	// after the first failure is observed.
	var observation generation.Observation
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	for round := 0; round < 20; round++ {
		var err error
		observation, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		permit.settle(t, observation)
		request.Opaque = observation.Opaque
		if status, _ := conversionStatus(t, observation.Opaque, "GLTF"); status == "retry_pending" {
			break
		}
	}
	if status, retries := conversionStatus(t, observation.Opaque, "GLTF"); status != "retry_pending" || retries != 0 {
		t.Fatalf("GLTF = %s/%d, want retry_pending before the first resubmission", status, retries)
	}
	upstream.set(func(f *exportFixture) { f.failFormats["GLTF"] = false })
	observation = driveExport(t, adapter, request, permit, 40)
	if observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s, want completed after the resubmission: %+v", observation.Phase, observation.Failure)
	}
	posts, _, _ := upstream.snapshot()
	// The GLTF export is the blocking conversion of a mixed Offering (#462):
	// before success it is the only format sent, and the failed child is resent
	// once. The five requested formats are converted after success.
	if len(posts) != 2 || posts[0] != "GLTF" || posts[1] != "GLTF" {
		t.Fatalf("convert posts = %v, want the failed GLTF resent once", posts)
	}
	var state struct {
		Conversions map[string]struct {
			Status   string `json:"status"`
			Attempts []struct {
				RemoteID string `json:"remote_id"`
				Outcome  string `json:"outcome"`
			} `json:"attempts"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(observation.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	gltf := state.Conversions["GLTF"]
	if len(gltf.Attempts) != 2 || gltf.Attempts[0].Outcome != "failed" || gltf.Attempts[1].Outcome != "done" {
		t.Fatalf("GLTF attempts = %+v, want the failed attempt and the successful resubmission", gltf.Attempts)
	}
	if gltf.Attempts[0].RemoteID == "" || gltf.Attempts[0].RemoteID == gltf.Attempts[1].RemoteID {
		t.Fatalf("attempts must record each remote id: %+v", gltf.Attempts)
	}
}

// #487: a cancelled child is terminal. The official lifecycle page ends the
// flow at status=cancelled ("stop polling and report cancellation") and the
// billing page returns the frozen credits, but "not charged" does not mean
// "worth sending again": the format is not resent, and the run fails on it.
func TestCancelledConvertChildIsNotResent(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) {
		f.runningPolls = 0
		f.cancelFormats["GLTF"] = true
	})
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	observation := driveExport(t, adapter, request, permit, 40)
	if observation.Phase != generation.UpstreamFailed {
		t.Fatalf("phase = %s, want failed: %+v", observation.Phase, observation.Failure)
	}
	posts, _, _ := upstream.snapshot()
	if len(posts) != 1 || posts[0] != "GLTF" {
		t.Fatalf("convert posts = %v, want the cancelled GLTF alone", posts)
	}
	var state struct {
		Conversions map[string]struct {
			Status   string `json:"status"`
			Attempts []struct {
				Outcome string `json:"outcome"`
			} `json:"attempts"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(observation.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	attempts := state.Conversions["GLTF"].Attempts
	if len(attempts) != 1 || attempts[0].Outcome != "canceled" {
		t.Fatalf("GLTF attempts = %+v, want the cancelled attempt alone", attempts)
	}
}

// #487: a failed child whose task-level error_code is determinate (1004 invalid
// parameter, 2008 content policy, 2018 model too complex) is not resent: the
// official error-handling page sends those back as 400-class answers, so the
// same request fails the same way. Only the transient codes (1000, 1001) are.
func TestDeterminateConvertChildFailureIsNotResent(t *testing.T) {
	for _, code := range []int{1004, 2008, 2018} {
		upstream := newExportFixture(t)
		upstream.set(func(f *exportFixture) {
			f.runningPolls = 0
			f.failFormats["GLTF"] = true
			f.failCodes["GLTF"] = code
		})
		adapter := newAdapter(t, upstream.server)
		permit := &conversionPermit{}
		observation := driveExport(t, adapter, fullFormatRequest(t), permit, 40)
		if observation.Phase != generation.UpstreamFailed {
			t.Fatalf("code %d: phase = %s, want failed: %+v", code, observation.Phase, observation.Failure)
		}
		if posts, _, _ := upstream.snapshot(); len(posts) != 1 || posts[0] != "GLTF" {
			t.Fatalf("code %d: convert posts = %v, want the GLTF alone", code, posts)
		}
	}
}

// A2: the 429 budget belongs to each format. Three refusals of USDZ and five of
// FBX used to exhaust one shared budget of five and fail the run; each format
// now gets its own five. The formats of a mixed Offering (#462) that are
// requested rather than blocking are converted after success, so this runs the
// post-success phase.
func TestRateLimitBudgetIsPerFormat(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) {
		f.refusals["USDZ"] = 3
		f.refusals["FBX"] = 5
		f.retryAfter = "1"
	})
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	observation := drivePostSuccess(t, adapter, request, permit, 400)
	if observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s, want completed: %+v", observation.Phase, observation.Failure)
	}
	posts, _, _ := upstream.snapshot()
	counts := map[string]int{}
	for _, format := range posts {
		counts[format]++
	}
	if counts["USDZ"] != 4 || counts["FBX"] != 6 {
		t.Fatalf("counts = %v, want USDZ 4 (1+3 refusals) and FBX 6 (1+5 refusals)", counts)
	}
}

// A5: at most five private children are in flight at once (the official default
// pool), and the last format is dispatched only after a slot frees. A mixed
// Offering (#462) has five requested formats after success, so the fan-out is
// exercised by holding every child running: each of them starts while the
// previous ones are still in flight, and none of them starts with more than five
// in flight.
func TestConversionConcurrencyIsBoundedByFive(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) { f.runningPolls = 100 })
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request.PostSuccess = true
	// Every child stays running: the five requested formats may all be started.
	for round := 0; round < 12; round++ {
		observation, err := adapter.Observe(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		permit.settle(t, observation)
		request.Opaque = observation.Opaque
	}
	posts, inflight, _ := upstream.snapshot()
	if len(posts) != 5 {
		t.Fatalf("posts = %v, want the five requested formats while every child is running", posts)
	}
	for index, count := range inflight {
		if count > 5 {
			t.Fatalf("post %d (%s) started with %d children in flight, want at most 5", index, posts[index], count)
		}
	}
	// Let the wave finish: every format is delivered and the run settles.
	upstream.set(func(f *exportFixture) { f.runningPolls = 0 })
	observation := driveExport(t, adapter, request, permit, 40)
	if observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s, want completed: %+v", observation.Phase, observation.Failure)
	}
	posts, _, _ = upstream.snapshot()
	if len(posts) != 5 {
		t.Fatalf("posts = %v, want no sixth format", posts)
	}
}

// A5: a refusal with Retry-After waits for it; without one the documented
// backoff applies. The budget is per format and each refusal is a new permit.
func TestRateLimitRefusalWaitsForRetryAfter(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) {
		f.refusals["GLTF"] = 1
		f.retryAfter = "7"
	})
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	refused, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatalf("explicit 429 became an error: %v", err)
	}
	permit.settle(t, refused)
	if refused.Dispatch == nil || refused.Dispatch.Result != generation.DispatchResultNotDispatched {
		t.Fatalf("refusal not settled as not dispatched: %+v", refused)
	}
	if refused.PollAfter != 7*time.Second {
		t.Fatalf("PollAfter = %s, want the Retry-After (7s)", refused.PollAfter)
	}
	if status, retries := conversionStatus(t, refused.Opaque, "GLTF"); status != "rate_limited" || retries != 1 {
		t.Fatalf("GLTF = %s/%d, want rate_limited/1", status, retries)
	}
	// No Retry-After: the documented 1 s, 2 s, 4 s… backoff.
	upstream.set(func(f *exportFixture) {
		f.refusals["GLTF"] = 2
		f.retryAfter = ""
	})
	waits := []time.Duration{refused.PollAfter}
	request.Opaque = refused.Opaque
	for round := 0; round < 10; round++ {
		observation, err := adapter.Observe(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		permit.settle(t, observation)
		request.Opaque = observation.Opaque
		if status, _ := conversionStatus(t, request.Opaque, "GLTF"); status == "submitted" || status == "done" {
			break
		}
		waits = append(waits, observation.PollAfter)
	}
	// The first refusal carried Retry-After (7 s); the next two are the
	// documented 2 s and 4 s steps of the same per-format counter.
	if fmt.Sprint(waits[1:]) != fmt.Sprint([]time.Duration{2 * time.Second, 4 * time.Second}) {
		t.Fatalf("backoff = %v, want 2s then 4s after the Retry-After refusal", waits)
	}
}

// A7: a free upload creates no task, so a failed upload is a retryable
// transport error rather than an ambiguous paid submission, and the next
// attempt resends it.
func TestFailedUploadIsARetryableTransportError(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) { f.uploadFailures = 1 })
	adapter := newAdapterWithImage(t, upstream.server, []byte("png-bytes"), uploadOffering())
	first := uploadStartRequest()
	_, err := adapter.Start(context.Background(), first)
	var provider *generation.ProviderError
	if err == nil || !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("upload failure = %v, want a provider failure", err)
	}
	// An upload creates no task: a 5xx is a retryable transport failure, never
	// the ambiguous "the paid submission may have been accepted" answer.
	if provider.Failure.Ambiguous || !provider.Failure.Retryable ||
		provider.Failure.Class == generation.FailureSubmissionUnknown {
		t.Fatalf("upload failure = %+v, want a retryable, non-ambiguous failure", provider.Failure)
	}
	second := uploadStartRequest()
	observation, err := adapter.Start(context.Background(), second)
	if err != nil {
		t.Fatalf("resubmission after a failed upload: %v", err)
	}
	if observation.Phase != generation.UpstreamAccepted {
		t.Fatalf("phase = %s, want accepted", observation.Phase)
	}
}

// A3: the duration bound and A4/A8 are declared in the Schema, not in code: the
// Tripo connection bounds "unknown" by time, every Tripo Offering polls every
// two seconds, the private conversions declare the concurrency, and the
// connection declares the seven-day asset retention.
func TestTripoSchemaDeclaresTheOfficialRetryFacts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var tripo *catalog.ProviderConnectionSchema
	for _, connection := range c.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "tripo.v3" {
			value := connection
			tripo = &value
		}
	}
	if tripo == nil {
		t.Fatal("missing tripo.v3 connection")
	}
	unknown, declared := tripo.StatusMapping.UpstreamStatus["unknown"]
	if !declared || unknown.MaxDurationSeconds != 300 || unknown.MaxConsecutive != 0 {
		t.Fatalf("unknown = %+v, want a 300s duration bound and no poll-count bound", unknown)
	}
	if tripo.ResultRefetch == nil || tripo.ResultRefetch.RetentionHours != 168 {
		t.Fatalf("result_refetch = %+v, want the documented seven-day retention", tripo.ResultRefetch)
	}
	checked := 0
	for _, capability := range tripo.Capabilities {
		for _, offering := range c.List(capability) {
			if offering.ProviderID != "tripo" {
				continue
			}
			if offering.PollInterval != 2*time.Second {
				t.Fatalf("%s poll interval = %s, want the documented 1-2 seconds", offering.ID, offering.PollInterval)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no Tripo Offering found")
	}
}

// uploadOffering is the smallest image-to-3d Offering that uploads its
// reference picture to the free /files endpoint.
func uploadOffering() generation.Offering {
	return generation.Offering{
		ProviderID:   "tripo",
		Capability:   generation.ImageTo3D,
		PollInterval: time.Second,
		ProviderOptions: []byte(`{"model":"v3.1-20260211","endpoint":"generation/image-to-model",` +
			`"observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
	}
}

// uploadStartRequest is a Start request for uploadOffering with one picture.
func uploadStartRequest() generation.StartRequest {
	return generation.StartRequest{
		TaskID:        "task",
		AttemptID:     "attempt",
		OwnerID:       "owner",
		SubmissionKey: "submission",
		Offering:      uploadOffering(),
		Input:         json.RawMessage(`{"reference_assets":["asset-input"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{
				ID:        "asset-input",
				OwnerID:   "owner",
				MediaType: "image/png",
				Size:      9,
				SHA256:    strings.Repeat("a", 64),
			}},
		},
	}
}
