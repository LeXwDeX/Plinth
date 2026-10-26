package wirefixture

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func internalDeferred(t *testing.T, limits DeferredLimits) *Recorder {
	t.Helper()
	r, err := NewDeferredRecorder(Metadata{Scenario: "test", Provenance: ProvenanceSynthetic}, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Release() })
	return r
}

func deferredRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", "https://example.test/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Check old entries too: dropping the pending slice alone must not retain raw
// data through another capture-owned reference or cancellation closure.
func assertDeferredCleared(t *testing.T, r *Recorder, entries []*deferredExchange) {
	t.Helper()
	d := r.deferred
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used != 0 || d.pending != nil {
		t.Fatalf("capture retained budget/pending: %d / %d", d.used, len(d.pending))
	}
	for _, c := range entries {
		if c.reqRaw != nil || c.respRaw != nil || c.requestHeaders != nil || c.responseHeaders != nil ||
			c.stop != nil || c.rec != nil || c.metadata != (Metadata{}) || c.method != "" || c.path != "" || c.host != "" || c.scheme != "" {
			t.Fatal("entry retained raw data or callback references")
		}
	}
}

func TestDeferredBudgetIncludesPendingAndInFlightBodies(t *testing.T) {
	r := internalDeferred(t, DeferredLimits{MaxBytes: 3500, MaxExchanges: 3})
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1000))), ContentLength: 1000}, nil
	})
	trip := r.Wrap("LLM", base)
	first, err := trip.RoundTrip(deferredRequest(t, strings.Repeat("x", 1000)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, first.Body); err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()
	if len(r.Failures()) != 0 {
		t.Fatal("first completed exchange exceeded budget")
	}
	second, err := trip.RoundTrip(deferredRequest(t, strings.Repeat("x", 1000)))
	if err != nil || len(r.Failures()) != 0 {
		t.Fatalf("second request should still fit: %v", err)
	}
	entries := append([]*deferredExchange(nil), r.deferred.pending...)
	buf := make([]byte, 1000)
	n, err := second.Body.Read(buf)
	if n != 1000 || err != nil || string(buf) != strings.Repeat("x", 1000) {
		t.Fatalf("budget failure changed business Read: %d %v", n, err)
	}
	// No EOF/Close has happened: accounting must reject during this Read.
	if !errors.Is(r.AssertComplete(), ErrCaptureLimit) {
		t.Fatal("pending + in-flight budget was not enforced before EOF")
	}
	assertDeferredCleared(t, r, entries)
	_, _ = io.Copy(io.Discard, second.Body)
	_ = second.Body.Close()
	for range 100 {
		resp, err := trip.RoundTrip(deferredRequest(t, "{}"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if len(r.Failures()) != 1 || len(r.Exchanges()) != 0 {
		t.Fatal("failed capture resumed or grew its failure list")
	}
	assertDeferredCleared(t, r, entries)
}

func TestDeferredExchangeLimitBoundsPendingAndInFlight(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "in flight", true: "completed pending"}[pending], func(t *testing.T) {
			r := internalDeferred(t, DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 2})
			var responses []*http.Response
			calls := 0
			trip := r.Wrap("LLM", roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader("{}")), ContentLength: 2}, nil
			}))
			for range 2 {
				resp, err := trip.RoundTrip(deferredRequest(t, ""))
				if err != nil {
					t.Fatal(err)
				}
				responses = append(responses, resp)
				if pending {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}
			entries := append([]*deferredExchange(nil), r.deferred.pending...)
			resp, err := trip.RoundTrip(deferredRequest(t, ""))
			responses = append(responses, resp)
			if err != nil || calls != 3 || !errors.Is(r.AssertComplete(), ErrCaptureLimit) {
				t.Fatalf("exchange limit affected transport or was ignored: calls=%d err=%v", calls, err)
			}
			for _, resp := range responses {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			assertDeferredCleared(t, r, entries)
		})
	}
}

func TestDeferredBudgetsMetadataAndHeadersBeforeCopy(t *testing.T) {
	for _, field := range []string{"metadata", "request header", "response header"} {
		t.Run(field, func(t *testing.T) {
			r := internalDeferred(t, DeferredLimits{MaxBytes: 4096, MaxExchanges: 1})
			req := deferredRequest(t, "")
			large := strings.Repeat("x", 8192)
			if field == "metadata" {
				r.Default.ReportedModel = large
			}
			if field == "request header" {
				req.Header.Set("Accept", large)
			}
			calls := 0
			resp, err := r.Wrap("LLM", roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				h := http.Header{}
				if field == "response header" {
					h.Set("Retry-After", large)
				}
				return &http.Response{StatusCode: 204, Header: h, Body: http.NoBody}, nil
			})).RoundTrip(req)
			if err != nil || resp.StatusCode != 204 || calls != 1 || !errors.Is(r.AssertComplete(), ErrCaptureLimit) {
				t.Fatalf("unbounded retained %s: %v", field, err)
			}
			assertDeferredCleared(t, r, nil)
		})
	}
}

func TestDeferredRawAndCallbacksReleasedOnEveryExit(t *testing.T) {
	for _, exit := range []string{"success", "invalid policy", "release", "cancel", "transport error"} {
		t.Run(exit, func(t *testing.T) {
			r := internalDeferred(t, DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 1})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var entries []*deferredExchange
			var reqWrapper *deferredBody
			base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				reqWrapper = req.Body.(*deferredBody)
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
				entries = append(entries, r.deferred.pending...)
				if exit == "transport error" {
					return nil, errors.New("private upstream error")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"id":"private value"}`)), ContentLength: 22}, nil
			})
			resp, err := r.Wrap("LLM", base).RoundTrip(deferredRequest(t, "{}").WithContext(ctx))
			if exit == "transport error" {
				if err == nil {
					t.Fatal("transport error lost")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if exit == "success" || exit == "invalid policy" {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					// Completion must stop the context callback before Finalize.
					if entries[0].stop != nil {
						t.Fatal("completed exchange retained cancellation registration")
					}
					p := Policy{Keys: map[string]bool{"id": true}, Aliases: map[string]string{"private value": "[redacted]"}}
					if exit == "invalid policy" {
						p.Keys = nil
					}
					if err := r.Finalize(p); (exit == "success") != (err == nil) {
						t.Fatalf("Finalize = %v", err)
					}
				} else if exit == "release" {
					_ = r.Release()
				} else {
					cancel()
					deadline := time.After(time.Second)
					for len(r.Failures()) == 0 {
						select {
						case <-deadline:
							t.Fatal("cancellation did not release capture")
						default:
							time.Sleep(time.Millisecond)
						}
					}
				}
				// A retained business wrapper must not keep an entry/raw closure.
				if reqWrapper.capture != r.deferred {
					t.Fatal("unexpected request wrapper")
				}
				if exit == "release" || exit == "cancel" {
					got, err := io.ReadAll(resp.Body)
					if err != nil || string(got) != `{"id":"private value"}` {
						t.Fatal("cleanup consumed or closed business body")
					}
					_ = resp.Body.Close()
				}
			}
			assertDeferredCleared(t, r, entries)
		})
	}
}

func TestDeferredCancellationStartedBeforeCompletion(t *testing.T) {
	r := internalDeferred(t, DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 1})
	resp, err := r.Wrap("LLM", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader("{}")), ContentLength: 2}, nil
	})).RoundTrip(deferredRequest(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	d := r.deferred
	d.mu.Lock()
	entries := append([]*deferredExchange(nil), d.pending...)
	c := entries[0]
	c.stop()
	// AfterFunc has started but its callback is still waiting for d.mu.
	c.stop = func() bool { return false }
	c.respDone = true
	d.stopCompletedLocked(c)
	d.mu.Unlock()
	if !errors.Is(r.AssertComplete(), ErrIncompleteCapture) || len(r.Failures()) != 1 {
		t.Fatal("completion hid a cancellation that had already started")
	}
	assertDeferredCleared(t, r, entries)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func TestDeferredSingleBodyCeilingAndConfiguration(t *testing.T) {
	for _, limits := range []DeferredLimits{{}, {MaxBytes: 1}, {MaxExchanges: 1}, {MaxBytes: -1, MaxExchanges: 1}} {
		if _, err := NewDeferredRecorder(Metadata{}, limits); !errors.Is(err, ErrCaptureLimit) {
			t.Fatalf("invalid limits accepted: %+v", limits)
		}
	}
	ordinary := &Recorder{}
	if !errors.Is(ordinary.Finalize(Policy{}), ErrNotDeferred) || !errors.Is(ordinary.Release(), ErrNotDeferred) {
		t.Fatal("deferred lifecycle applied to immediate recorder")
	}
	r := internalDeferred(t, DeferredLimits{MaxBytes: 20 << 20, MaxExchanges: 1})
	resp, err := r.Wrap("LLM", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, req.Body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (8<<20)+1))), ContentLength: -1}, nil
	})).RoundTrip(deferredRequest(t, "{}"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := io.CopyN(io.Discard, resp.Body, 8<<20); n != 8<<20 || err != nil || len(r.Failures()) != 0 {
		t.Fatalf("8 MiB boundary rejected: n=%d err=%v", n, err)
	}
	entries := append([]*deferredExchange(nil), r.deferred.pending...)
	if n, err := io.Copy(io.Discard, resp.Body); n != 1 || err != nil || !errors.Is(r.AssertComplete(), ErrCaptureLimit) {
		t.Fatalf("body ceiling changed business or was ignored: n=%d err=%v", n, err)
	}
	_ = resp.Body.Close()
	assertDeferredCleared(t, r, entries)
}
