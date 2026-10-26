package tripo_test

// Generation-task format conversions (pre-completion for the full-format
// Offering, post-success for selected exports) treat an explicit HTTP 429 on
// the private convert POST like the native export ledger does (#185): the
// permit is settled as not dispatched and the same format is sent again after
// Retry-After or the backoff, within a bounded budget. Unconfirmed results
// (timeouts, 408, 5xx) stay unknown and are never resent.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// conversionPermit grants a permit only after the previous one was settled,
// like the kernel's checkpoint.
type conversionPermit struct {
	calls   int
	pending bool
	intent  json.RawMessage
}

func (p *conversionPermit) Checkpoint(_ context.Context, i generation.DispatchIntent) (generation.DispatchAck, error) {
	if p.pending {
		return generation.DispatchAck{}, generation.ErrDispatchPending
	}
	p.calls++
	p.pending = true
	p.intent = append(json.RawMessage(nil), i.Bytes...)
	return generation.DispatchAck{Sequence: int64(p.calls), AckToken: fmt.Sprint(p.calls), Opaque: i.Bytes}, nil
}

func (p *conversionPermit) settle(t *testing.T, obs generation.Observation) {
	t.Helper()
	if obs.Dispatch == nil {
		return
	}
	if !p.pending || obs.Dispatch.Sequence != int64(p.calls) || obs.Dispatch.AckToken != fmt.Sprint(p.calls) {
		t.Fatalf("receipt does not match the granted permit: %+v", obs.Dispatch)
	}
	p.pending = false
}

type refusalUpstream struct {
	mu       sync.Mutex
	converts []string
	refuse   func(format string, n int, w http.ResponseWriter) bool
}

func (u *refusalUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			u.mu.Lock()
			u.converts = append(u.converts, format)
			n := 0
			for _, f := range u.converts {
				if f == format {
					n++
				}
			}
			u.mu.Unlock()
			if u.refuse != nil && u.refuse(format, n, w) {
				return
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "child-" + strings.ToLower(format)}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tasks/base"):
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "base", "status": "success", "progress": 100,
				"output": map[string]any{"model_url": server.URL + "/base.glb"},
			}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tasks/child-"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": id, "status": "success", "progress": 100,
				"output": map[string]any{"model_url": server.URL + "/" + id + ".glb"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (u *refusalUpstream) count(format string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, f := range u.converts {
		if f == format {
			n++
		}
	}
	return n
}

func refuse429(w http.ResponseWriter, retryAfter string) bool {
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = io.WriteString(w, `{"code":2000,"message":"You have exceeded the limit of generation"}`)
	return true
}

func conversionStatus(t *testing.T, opaque json.RawMessage, format string) (string, int) {
	t.Helper()
	var state struct {
		Conversions map[string]struct {
			Status           string `json:"status"`
			RateLimitRetries int    `json:"rate_limit_retries"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatalf("decode %s: %v", opaque, err)
	}
	item := state.Conversions[format]
	return item.Status, item.RateLimitRetries
}

func fullFormatRequest(t *testing.T) generation.ObserveRequest {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("missing full-format offering")
	}
	return generation.ObserveRequest{AttemptID: "attempt", Offering: offering, Input: fullTextFrozenInput(t, c),
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"}}
}

// Pre-completion: the first GLTF convert is refused with 429 and Retry-After;
// the next observation resends GLTF under a new permit and the run completes
// with every format converted once.
func TestPreCompletion429RetriesTheSameFormatAfterRetryAfter(t *testing.T) {
	upstream := &refusalUpstream{refuse: func(format string, n int, w http.ResponseWriter) bool {
		return format == "GLTF" && n == 1 && refuse429(w, "9")
	}}
	adapter := newAdapter(t, upstream.server(t))
	permit := &conversionPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request := fullFormatRequest(t)
	refused, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatalf("explicit 429 became an error: %v", err)
	}
	if refused.Phase != generation.UpstreamRunning || refused.Dispatch == nil || refused.Dispatch.Result != generation.DispatchResultNotDispatched || refused.PollAfter != 9*time.Second || refused.ObservedAt.IsZero() {
		t.Fatalf("429 not settled as a refused, not-dispatched child: %+v", refused)
	}
	if status, retries := conversionStatus(t, refused.Opaque, "GLTF"); status != "rate_limited" || retries != 1 {
		t.Fatalf("GLTF state %s/%d", status, retries)
	}
	permit.settle(t, refused)
	request.Opaque = refused.Opaque
	var obs generation.Observation
	for round := 0; round < 30; round++ {
		obs, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		permit.settle(t, obs)
		request.Opaque = obs.Opaque
		if obs.Phase != generation.UpstreamRunning {
			break
		}
	}
	if obs.Phase != generation.UpstreamCompleted || upstream.count("GLTF") != 2 {
		t.Fatalf("run did not complete after one refusal: phase %s, GLTF converts %d", obs.Phase, upstream.count("GLTF"))
	}
	for _, format := range []string{"USDZ", "FBX", "OBJ", "STL", "3MF"} {
		if upstream.count(format) != 1 {
			t.Fatalf("%s converted %d times", format, upstream.count(format))
		}
	}
}

// Pre-completion: without Retry-After the waits double from 1 s; when the
// budget is spent the public task fails as a definite rate limit, not
// retryable and not ambiguous.
func TestPreCompletion429BacksOffAndFailsAfterTheBudget(t *testing.T) {
	upstream := &refusalUpstream{refuse: func(_ string, _ int, w http.ResponseWriter) bool { return refuse429(w, "") }}
	adapter := newAdapter(t, upstream.server(t))
	permit := &conversionPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request := fullFormatRequest(t)
	var waits []time.Duration
	var obs generation.Observation
	for round := 0; round < 10; round++ {
		var err error
		obs, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatalf("429 became an error: %v", err)
		}
		permit.settle(t, obs)
		if obs.Dispatch == nil || obs.Dispatch.Result != generation.DispatchResultNotDispatched {
			t.Fatalf("refusal not settled as not dispatched: %+v", obs)
		}
		request.Opaque = obs.Opaque
		if obs.Phase != generation.UpstreamRunning {
			break
		}
		waits = append(waits, obs.PollAfter)
	}
	if fmt.Sprint(waits) != fmt.Sprint([]time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}) {
		t.Fatalf("backoff %v", waits)
	}
	if obs.Phase != generation.UpstreamFailed || obs.Failure == nil || obs.Failure.Class != generation.FailureRateLimited || obs.Failure.Retryable || obs.Failure.Ambiguous {
		t.Fatalf("exhausted budget must fail definitely: %+v", obs.Failure)
	}
	if upstream.count("GLTF") != 6 {
		t.Fatalf("GLTF converts %d", upstream.count("GLTF"))
	}
}

// Pre-completion: an unconfirmed create result stays unknown; the format is
// never resent, before or after restoring the durable intent.
func TestPreCompletionUnconfirmedConvertStaysUnknown(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &refusalUpstream{refuse: func(_ string, _ int, w http.ResponseWriter) bool {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":2000,"message":"busy"}`)
				return true
			}}
			adapter := newAdapter(t, upstream.server(t))
			permit := &conversionPermit{}
			ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
			request := fullFormatRequest(t)
			obs, err := adapter.Observe(ctx, request)
			var provider *generation.ProviderError
			if err == nil || !errors.As(err, &provider) || provider.Failure == nil || !provider.Failure.Ambiguous || obs.Dispatch != nil {
				t.Fatalf("status %d must stay unknown: %v %+v", status, err, obs)
			}
			request.Opaque = permit.intent
			resumed, err := adapter.Observe(ctx, request)
			if upstream.count("GLTF") != 1 || err == nil && resumed.Phase != generation.UpstreamFailed {
				t.Fatalf("unknown result replayed: converts %d err %v %+v", upstream.count("GLTF"), err, resumed)
			}
		})
	}
}

// Post-success: a selected export refused with 429 waits, is resent under a
// new permit, and is delivered; the refusal never terminates the slot.
func TestPostSuccess429RetriesTheSelectedFormat(t *testing.T) {
	upstream := &refusalUpstream{refuse: func(format string, n int, w http.ResponseWriter) bool {
		return format == "FBX" && n == 1 && refuse429(w, "3")
	}}
	server := upstream.server(t)
	adapter := newAdapterWithImage(t, server, nil, selectedSetOffering(t))
	permit := &conversionPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request := generation.ObserveRequest{
		TaskID: "parent", AttemptID: "attempt", Offering: selectedSetOffering(t), PostSuccess: true,
		Job:   generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input: json.RawMessage(`{"output_formats":["FBX"],"authorize_extra_credits":true}`),
	}
	refused, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatalf("explicit 429 became an error: %v", err)
	}
	if refused.Phase != generation.UpstreamRunning || refused.Dispatch == nil || refused.Dispatch.Result != generation.DispatchResultNotDispatched || refused.PollAfter != 3*time.Second || len(refused.ResourceUpdates) != 0 {
		t.Fatalf("429 not handled as a refusal: %+v", refused)
	}
	if status, retries := conversionStatus(t, refused.Opaque, "FBX"); status != "rate_limited" || retries != 1 {
		t.Fatalf("FBX state %s/%d", status, retries)
	}
	permit.settle(t, refused)
	request.Opaque = refused.Opaque
	submitted, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Dispatch == nil || submitted.Dispatch.Result != generation.DispatchResultDispatched || upstream.count("FBX") != 2 {
		t.Fatalf("FBX not resent under a new permit: %+v", submitted)
	}
	if status, retries := conversionStatus(t, submitted.Opaque, "FBX"); status != "submitted" || retries != 1 {
		t.Fatalf("FBX state %s/%d", status, retries)
	}
	permit.settle(t, submitted)
	request.Opaque = submitted.Opaque
	delivered, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.Phase != generation.UpstreamCompleted || len(delivered.Artifacts) != 1 || upstream.count("FBX") != 2 {
		t.Fatalf("FBX not delivered after the refusal: %+v", delivered)
	}
}

// Post-success: when the budget is spent the selected format fails and only
// its own slot is asserted failed.
func TestPostSuccess429FailsTheFormatAfterTheBudget(t *testing.T) {
	upstream := &refusalUpstream{refuse: func(_ string, _ int, w http.ResponseWriter) bool { return refuse429(w, "") }}
	adapter := newAdapterWithImage(t, upstream.server(t), nil, selectedSetOffering(t))
	permit := &conversionPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request := generation.ObserveRequest{
		TaskID: "parent", AttemptID: "attempt", Offering: selectedSetOffering(t), PostSuccess: true,
		Job:   generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input: json.RawMessage(`{"output_formats":["FBX"],"authorize_extra_credits":true}`),
	}
	var obs generation.Observation
	for round := 0; round < 10; round++ {
		var err error
		obs, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		permit.settle(t, obs)
		request.Opaque = obs.Opaque
		if obs.Phase != generation.UpstreamRunning {
			break
		}
	}
	if status, _ := conversionStatus(t, obs.Opaque, "FBX"); status != "failed" || upstream.count("FBX") != 6 {
		t.Fatalf("FBX state %s after %d converts", status, upstream.count("FBX"))
	}
	if len(obs.ResourceUpdates) != 1 || obs.ResourceUpdates[0].Variant != "converted.fbx" || obs.ResourceUpdates[0].State != generation.ResourceFailed {
		t.Fatalf("exhausted FBX must fail only its own slot: %+v", obs.ResourceUpdates)
	}
}
