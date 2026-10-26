package tripo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// rateLimitPermit records every private dispatch intent. Like the kernel, it
// grants a new permit only after the previous one has been settled.
type rateLimitPermit struct {
	calls   int
	pending bool
	intents []json.RawMessage
}

func (p *rateLimitPermit) Checkpoint(_ context.Context, i generation.DispatchIntent) (generation.DispatchAck, error) {
	if p.pending {
		return generation.DispatchAck{}, generation.ErrDispatchPending
	}
	p.calls++
	p.pending = true
	p.intents = append(p.intents, append(json.RawMessage(nil), i.Bytes...))
	return generation.DispatchAck{Sequence: int64(p.calls), AckToken: fmt.Sprint(p.calls), Opaque: i.Bytes}, nil
}

// settle applies an observation's receipt the way the kernel does.
func (p *rateLimitPermit) settle(t *testing.T, obs generation.Observation) {
	t.Helper()
	if obs.Dispatch == nil {
		return
	}
	if !p.pending || obs.Dispatch.Sequence != int64(p.calls) || obs.Dispatch.AckToken != fmt.Sprint(p.calls) {
		t.Fatalf("receipt does not match the granted permit: %+v", obs.Dispatch)
	}
	p.pending = false
}

type rateLimitServer struct {
	converts []string
	answer   func(n int, w http.ResponseWriter) bool
}

func (s *rateLimitServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == http.MethodPost {
			if !strings.HasSuffix(q.URL.Path, "models/convert") {
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
				return
			}
			body, _ := io.ReadAll(q.Body)
			s.converts = append(s.converts, string(body))
			if s.answer(len(s.converts), w) {
				return
			}
			io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx"}}`)
			return
		}
		id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
		locator := "https://assets.example/original.glb"
		if id != "task_processed" {
			locator = "https://assets.example/" + id + ".fbx"
		}
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","credits_consumed":10,"output":{"model_url":%q}}}`, id, locator)
	}))
	t.Cleanup(server.Close)
	return server
}

func rateLimited(w http.ResponseWriter, retryAfter string) {
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(http.StatusTooManyRequests)
	io.WriteString(w, `{"code":2000,"message":"You have exceeded the limit of generation"}`)
}

func rateLimitAdapter(t *testing.T, server *httptest.Server) *Adapter {
	t.Helper()
	a := rigAdapter(t, server)
	a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, nil)
	return a
}

func conversionState(t *testing.T, opaque json.RawMessage) nativeProcessingState {
	t.Helper()
	var state nativeProcessingState
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// An explicit HTTP 429 on a private export child is an upstream refusal: the
// child was not accepted. The adapter settles the permit as not dispatched,
// keeps the export ledger, waits for Retry-After and sends the same child again
// under a new permit. It never becomes an unknown result.
func TestNativeExport429RetriesTheSameChildAfterRetryAfter(t *testing.T) {
	backend := &rateLimitServer{answer: func(n int, w http.ResponseWriter) bool {
		if n == 1 {
			rateLimited(w, "7")
			return true
		}
		return false
	}}
	server := backend.start(t)
	r := nativeDeliveryFixture(generation.Rigging)
	p := &rateLimitPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), p)
	accepted, err := rateLimitAdapter(t, server).Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p.settle(t, accepted)
	q := rigObserve(r, accepted)

	refused, err := rateLimitAdapter(t, server).Observe(ctx, q)
	if err != nil {
		t.Fatalf("explicit 429 became an error (unknown result): %v", err)
	}
	if refused.Phase != generation.UpstreamRunning || refused.Failure != nil || refused.Dispatch == nil || refused.Dispatch.Result != generation.DispatchResultNotDispatched {
		t.Fatalf("429 not settled as a refused, not-dispatched child: %+v", refused)
	}
	if refused.PollAfter != 7*time.Second {
		t.Fatalf("Retry-After not honoured: %v", refused.PollAfter)
	}
	if refused.Progress <= 80 || refused.CheckpointBaseSHA256 == "" {
		t.Fatalf("a refused child must advance one private stage so the ledger is kept: %+v", refused)
	}
	state := conversionState(t, refused.Opaque)
	item := state.Conversions["FBX"]
	if item.Status != conversionItemRateLimited || item.RemoteID != "" || item.RateLimitRetries != 1 || len(state.ConversionRequests) != 1 {
		t.Fatalf("ledger lost the refused child: %+v", state.nativeConversionLedger)
	}
	p.settle(t, refused)

	q.Opaque = refused.Opaque
	submitted, err := rateLimitAdapter(t, server).Observe(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.converts) != 2 || backend.converts[0] != backend.converts[1] {
		t.Fatalf("the retry is not the same child: %q", backend.converts)
	}
	if submitted.Dispatch == nil || submitted.Dispatch.Result != generation.DispatchResultDispatched || submitted.Progress <= refused.Progress {
		t.Fatalf("retried child not submitted under a new permit: %+v", submitted)
	}
	if got := conversionState(t, submitted.Opaque).Conversions["FBX"]; got.Status != conversionItemSubmitted || got.RemoteID != "child_fbx" || got.RateLimitRetries != 1 {
		t.Fatalf("retried child state %+v", got)
	}
	p.settle(t, submitted)

	q.Opaque = submitted.Opaque
	var obs generation.Observation
	for i := 0; i < 4; i++ {
		obs, err = rateLimitAdapter(t, server).Observe(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		p.settle(t, obs)
		q.Opaque = obs.Opaque
		if obs.Phase == generation.UpstreamCompleted {
			break
		}
	}
	if obs.Phase != generation.UpstreamCompleted || len(backend.converts) != 2 || p.calls != 3 {
		t.Fatalf("export did not finish after one refusal: converts%d permits%d %+v", len(backend.converts), p.calls, obs)
	}
}

// Without Retry-After the wait doubles from 1 s (the official backoff). The
// retries are bounded; when they run out the export fails as rate limited and
// not retryable, so no new Attempt repeats the paid base operation.
func TestNativeExport429BacksOffAndStopsAfterBoundedRetries(t *testing.T) {
	backend := &rateLimitServer{answer: func(_ int, w http.ResponseWriter) bool {
		rateLimited(w, "")
		return true
	}}
	server := backend.start(t)
	r := nativeDeliveryFixture(generation.Rigging)
	p := &rateLimitPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), p)
	accepted, err := rateLimitAdapter(t, server).Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p.settle(t, accepted)
	q := rigObserve(r, accepted)
	var waits []time.Duration
	last := 0
	var obs generation.Observation
	for i := 0; i < 12; i++ {
		obs, err = rateLimitAdapter(t, server).Observe(ctx, q)
		if err != nil {
			t.Fatalf("429 became an error: %v", err)
		}
		p.settle(t, obs)
		if obs.Dispatch == nil || obs.Dispatch.Result != generation.DispatchResultNotDispatched {
			t.Fatalf("refusal not settled as not dispatched: %+v", obs)
		}
		q.Opaque = obs.Opaque
		if obs.Phase != generation.UpstreamRunning {
			break
		}
		if obs.Progress <= last {
			t.Fatalf("refusal stage not strictly increasing %d→%d", last, obs.Progress)
		}
		last = obs.Progress
		waits = append(waits, obs.PollAfter)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if fmt.Sprint(waits) != fmt.Sprint(want) {
		t.Fatalf("backoff %v, want %v", waits, want)
	}
	if obs.Phase != generation.UpstreamFailed || obs.Failure == nil || obs.Failure.Class != generation.FailureRateLimited || obs.Failure.Retryable || obs.Failure.Ambiguous {
		t.Fatalf("exhausted retries must fail as a definite, non-retryable rate limit: %+v", obs.Failure)
	}
	if len(backend.converts) != 1+maximumConversionRateLimitRetries {
		t.Fatalf("converts %d", len(backend.converts))
	}
	if got := conversionState(t, obs.Opaque).Conversions["FBX"]; got.Status != conversionItemFailed || got.RemoteID != "" {
		t.Fatalf("exhausted child state %+v", got)
	}
}

// A Retry-After beyond the bound is capped; an HTTP-date is accepted.
func TestNativeExport429RetryAfterIsBounded(t *testing.T) {
	for _, header := range []string{"86400", time.Now().Add(48 * time.Hour).UTC().Format(http.TimeFormat)} {
		backend := &rateLimitServer{answer: func(_ int, w http.ResponseWriter) bool {
			rateLimited(w, header)
			return true
		}}
		server := backend.start(t)
		r := nativeDeliveryFixture(generation.Rigging)
		p := &rateLimitPermit{}
		ctx := generation.WithDispatchCheckpointer(context.Background(), p)
		accepted, err := rateLimitAdapter(t, server).Start(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		p.settle(t, accepted)
		obs, err := rateLimitAdapter(t, server).Observe(ctx, rigObserve(r, accepted))
		if err != nil || obs.PollAfter != maximumConversionRateLimitWait {
			t.Fatalf("Retry-After %q gave wait %v err %v", header, obs.PollAfter, err)
		}
	}
}

// Only an explicit 429 is a refusal. A timeout or an unconfirmed server error
// on the create request stays an unknown result: no not-dispatched receipt,
// the permit stays pending and the child is never sent again.
func TestNativeExportUnconfirmedResultsStayUnknown(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			backend := &rateLimitServer{answer: func(_ int, w http.ResponseWriter) bool {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
				io.WriteString(w, `{"code":2000,"message":"busy"}`)
				return true
			}}
			server := backend.start(t)
			r := nativeDeliveryFixture(generation.Rigging)
			p := &rateLimitPermit{}
			ctx := generation.WithDispatchCheckpointer(context.Background(), p)
			accepted, err := rateLimitAdapter(t, server).Start(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			p.settle(t, accepted)
			q := rigObserve(r, accepted)
			obs, err := rateLimitAdapter(t, server).Observe(ctx, q)
			var provider *generation.ProviderError
			if err == nil || !errors.As(err, &provider) || provider.Failure == nil || !provider.Failure.Ambiguous || provider.Failure.Class != generation.FailureSubmissionUnknown || obs.Dispatch != nil {
				t.Fatalf("status %d must stay an unknown result: %v %+v", status, err, obs)
			}
			// Resume from the durable intent: no second create request.
			q.Opaque = p.intents[len(p.intents)-1]
			resumed, err := rateLimitAdapter(t, server).Observe(ctx, q)
			if len(backend.converts) != 1 || err == nil && resumed.Phase != generation.UpstreamFailed {
				t.Fatalf("unknown result replayed: converts %d err %v %+v", len(backend.converts), err, resumed)
			}
		})
	}
}

// A rate-limited ledger entry is private state; a forged or inconsistent one
// fails closed.
func TestNativeExportRateLimitCheckpointFailsClosed(t *testing.T) {
	r := nativeDeliveryFixture(generation.Retexture)
	var options providerOptions
	if err := json.Unmarshal(r.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	formats := options.NativeConversion.Formats
	units := nativeConversionProgressUnits(len(formats))
	request := func(format string) json.RawMessage {
		raw, _ := json.Marshal(nativeConversionBody(options.NativeConversion, "https://assets.example/original.glb", format))
		return raw
	}
	ledger := func(items map[string]conversionItem, progressUnits int) nativeConversionLedger {
		l := nativeConversionLedger{ConversionSource: &nativeConversionSource{Alias: "model_url", SHA256: strings.Repeat("a", 64), Size: 16}, Conversions: items, ConversionRequests: map[string]json.RawMessage{}, ProgressUnits: progressUnits}
		for format := range items {
			l.ConversionRequests[format] = request(format)
		}
		return l
	}
	valid := ledger(map[string]conversionItem{"FBX": {Status: conversionItemDone, RemoteID: "child_fbx", Locator: "https://assets.example/fbx"}, "OBJ": {Status: conversionItemRateLimited, RateLimitRetries: 2}}, units)
	if err := nativeConversionCheckpoint(valid, options); err != nil {
		t.Fatalf("valid rate-limited ledger rejected: %v", err)
	}
	budget := units - 2*len(formats)
	cases := map[string]nativeConversionLedger{
		"remote id on a refused child": ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited, RemoteID: "child", RateLimitRetries: 1}}, units),
		"refusal without a count":      ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited}}, units),
		"count over the budget":        ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited, RateLimitRetries: budget + 1}}, units),
		"total over the budget":        ledger(map[string]conversionItem{"FBX": {Status: conversionItemDone, RemoteID: "child_fbx", Locator: "https://assets.example/fbx", RateLimitRetries: budget}, "OBJ": {Status: conversionItemRateLimited, RateLimitRetries: 1}}, units),
		"refusal before another child": ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited, RateLimitRetries: 1}, "OBJ": {Status: conversionItemSubmitted, RemoteID: "child_obj"}}, units),
		"legacy ledger with refusal":   ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited, RateLimitRetries: 1}}, 0),
		"forged progress units":        ledger(map[string]conversionItem{"FBX": {Status: conversionItemRateLimited, RateLimitRetries: 1}}, units+1),
	}
	for name, l := range cases {
		if nativeConversionCheckpoint(l, options) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// Every stage, including each refused child, strictly raises the progress
// within the private export band for the largest frozen format set.
func TestNativeExportStagesWithRefusalsStrictlyIncrease(t *testing.T) {
	for count := 1; count <= maximumNativeConversionFormats; count++ {
		units := nativeConversionProgressUnits(count)
		budget := units - 2*count
		if budget < 1 || budget > maximumConversionRateLimitRetries || units > 18 {
			t.Fatalf("count %d: units %d budget %d", count, units, budget)
		}
		state := nativeProcessingState{nativeConversionLedger: nativeConversionLedger{Conversions: map[string]conversionItem{}, ProgressUnits: units}}
		run := &nativeConversionRun{ledger: &state.nativeConversionLedger, encode: func() json.RawMessage { return state.encode() }}
		last := 80
		step := func() {
			obs := nativeConversionRunning(generation.ObserveRequest{}, run, generation.Observation{}, count)
			if obs.Progress <= last || obs.Progress > 98 {
				t.Fatalf("count %d: stage %d→%d", count, last, obs.Progress)
			}
			last = obs.Progress
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprint(i)
			if i == 0 {
				for retry := 1; retry <= budget; retry++ {
					state.Conversions[key] = conversionItem{Status: conversionItemRateLimited, RateLimitRetries: retry}
					step()
				}
			}
			retries := state.Conversions[key].RateLimitRetries
			for _, status := range []string{conversionItemSubmitted, conversionItemDone} {
				state.Conversions[key] = conversionItem{Status: status, RemoteID: "child", RateLimitRetries: retries}
				step()
			}
		}
	}
}
