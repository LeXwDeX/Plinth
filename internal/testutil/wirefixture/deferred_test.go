package wirefixture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func newDeferred(t *testing.T, limits wirefixture.DeferredLimits) *wirefixture.Recorder {
	t.Helper()
	r, err := wirefixture.NewDeferredRecorder(testMetadata(), limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Release() })
	return r
}

func deferredRecorder(t *testing.T) *wirefixture.Recorder {
	t.Helper()
	return newDeferred(t, wirefixture.DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 8})
}

func assertNoDeferredExport(t *testing.T, r *wirefixture.Recorder, policy wirefixture.Policy) {
	t.Helper()
	if err := r.AssertComplete(); err == nil {
		t.Fatal("unfinalized or rejected capture reported complete")
	}
	if len(r.Exchanges()) != 0 || len(r.MediaEvidence()) != 0 {
		t.Fatal("unfinalized or rejected capture exposed evidence")
	}
	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "deferred.json")
	if err := wirefixture.WriteCandidate(root, path, r.Exchanges(), policy); err == nil {
		t.Fatal("unfinalized or rejected capture exported")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected export created a file: %v", err)
	}
}

func TestDeferredLateExactPolicy(t *testing.T) {
	r := deferredRecorder(t)
	r.Policy = testPolicy() // The immediate-mode field must not approve this capture.
	request := mustRequest(t, "POST", "https://api.meshy.ai/v1/tasks/live-job", `{"prompt":"private prompt","id":"live-job"}`)
	_, body := capture(t, r, request, jsonResponse(200, "application/json", `{"result":"private result","id":"live-job"}`))
	if string(body) != `{"result":"private result","id":"live-job"}` {
		t.Fatal("business response changed")
	}
	assertNoDeferredExport(t, r, testPolicy())
	public, err := json.Marshal(r)
	if err != nil || bytes.Contains(public, []byte("private result")) || strings.Contains(fmt.Sprintf("%+v", r), "private result") {
		t.Fatal("raw body exposed before Finalize")
	}

	// These exact mappings are supplied only after collection has ended.
	policy := testPolicy()
	policy.Aliases["private prompt"] = "synthetic prompt"
	policy.Aliases["private result"] = "synthetic result"
	policy.Aliases["live-job"] = "call-1"
	before, _ := json.Marshal(policy)
	if err := r.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(policy)
	if !bytes.Equal(before, after) {
		t.Fatal("Finalize mutated caller policy maps")
	}
	if err := r.AssertComplete(); err != nil {
		t.Fatal(err)
	}
	exchanges := r.Exchanges()
	if len(exchanges) != 1 || exchanges[0].Sequence != 1 || exchanges[0].Request.Path != "/v1/tasks/call-1" ||
		string(exchanges[0].Request.Body.JSON) != `{"id":"call-1","prompt":"synthetic prompt"}` ||
		string(exchanges[0].Response.Body.JSON) != `{"id":"call-1","result":"synthetic result"}` {
		t.Fatalf("wrong sanitized evidence: %+v", exchanges)
	}
	root, dir := safetyCandidateDir(t)
	if err := wirefixture.WriteCandidate(root, filepath.Join(dir, "candidate.json"), exchanges, policy); err != nil {
		t.Fatal(err)
	}
	// Published snapshots and policy remain independent of the sealed recorder.
	exchanges[0].Response.Body.JSON[0] = '!'
	clear(policy.Keys)
	clear(policy.Allowed)
	clear(policy.Aliases)
	if err := r.AssertComplete(); err != nil || !json.Valid(r.Exchanges()[0].Response.Body.JSON) {
		t.Fatalf("sealed snapshot was mutable: %v", err)
	}
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrCaptureSealed) {
		t.Fatalf("second Finalize = %v", err)
	}
}

func TestDeferredStrictFinalizeCannotBeRepaired(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		edit func(*wirefixture.Policy)
	}{
		{"unknown key", `{"unregistered":true}`, nil},
		{"unknown sensitive key", `{"provider_auth_token":"private value"}`, nil},
		{"unmapped string", `{"id":"live-job"}`, nil},
		{"unmapped number", `{"id":987654321}`, nil},
		{"nested unknown key", `{"message":"{\"unregistered\":null}"}`, nil},
		{"alias chain", `{"id":"live-job"}`, func(p *wirefixture.Policy) {
			p.Aliases["live-job"], p.Aliases["call-1"] = "call-1", "synthetic result"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := deferredRecorder(t)
			policy := testPolicy()
			if tc.edit != nil {
				tc.edit(&policy)
			}
			capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""), jsonResponse(200, "application/json", tc.body))
			if err := r.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
				t.Fatalf("strict Finalize = %v", err)
			}
			assertNoDeferredExport(t, r, policy)
			policy.Keys["unregistered"], policy.Keys["provider_auth_token"] = true, true
			policy.Aliases["live-job"] = "call-1"
			if err := r.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
				t.Fatalf("rejected capture repaired by another policy: %v", err)
			}
			if len(r.Failures()) != 1 || strings.Contains(fmt.Sprint(r.Failures()), "private value") {
				t.Fatal("failure was not bounded and sanitized")
			}
		})
	}
}

func TestDeferredFinalizeHonorsSmallerPolicyBodyLimit(t *testing.T) {
	r := deferredRecorder(t)
	capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""), jsonResponse(200, "application/json", `{}`))
	policy := testPolicy()
	policy.MaxBodyBytes = 1
	if err := r.Finalize(policy); !errors.Is(err, wirefixture.ErrCaptureLimit) {
		t.Fatalf("Finalize ignored policy body limit: %v", err)
	}
	assertNoDeferredExport(t, r, policy)
}

func TestDeferredMediaRequiresEvidence(t *testing.T) {
	for _, side := range []string{"request", "response"} {
		t.Run(side, func(t *testing.T) {
			r := deferredRecorder(t)
			_, dir := privateStore(t)
			policy := testPolicy()
			policy.Keys["file"] = true
			policy.Allowed["inputs"] = true
			req := mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", "")
			base := jsonResponse(200, "image/png", string(syntheticPNG(t)))
			if side == "request" {
				raw := multipartBody(t, "deferred-boundary", nil, []multipartFile{{name: "file", filename: "private.png", mediaType: "image/png", data: syntheticPNG(t)}})
				req = multipartRequest(t, raw, "deferred-boundary")
				base = jsonResponse(200, "application/json", `{}`)
			}
			capture(t, r, req, base)
			want := wirefixture.ErrInvalidExchange
			if side == "response" {
				want = wirefixture.ErrMediaEvidence
			}
			if err := r.Finalize(policy); !errors.Is(err, want) {
				t.Fatalf("private media accepted: %v", err)
			}
			assertNoDeferredExport(t, r, policy)
			if len(privateFiles(t, dir)) != 0 {
				t.Fatal("deferred raw bytes reached disk")
			}
		})
	}
}

func TestDeferredSSELateExactToolMappings(t *testing.T) {
	r := deferredRecorder(t)
	stream := sseStream(t, toolCallChunk(0, "private-", "private-", `{"id":"private-`),
		toolCallChunk(0, "call", "tool", `job"}`))
	_, got := capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/chat", ""), jsonResponse(200, "text/event-stream", stream))
	if string(got) != stream || len(r.Exchanges()) != 0 {
		t.Fatal("SSE changed or was exposed before Finalize")
	}
	policy := testPolicy()
	allowPolicy(&policy, []string{"tool_calls", "index", "function", "name", "arguments"}, []string{"refine_image"})
	policy.Aliases["private-call"] = "call-1"
	policy.Aliases["private-tool"] = "refine_image"
	policy.Aliases["private-job"] = "call-1"
	if err := r.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	e := r.Exchanges()[0]
	if e.Response.Body.Transform != wirefixture.TransformNormalizedSSE || strings.Contains(e.Response.Body.SSE, "private-") ||
		!strings.Contains(e.Response.Body.SSE, "refine_image") || !strings.Contains(e.Response.Body.SSE, "call-1") {
		t.Fatalf("late SSE mappings did not use strict normalization: %+v", e)
	}
}

func TestDeferredProjectedToolArgumentsValidate(t *testing.T) {
	r := deferredRecorder(t)
	stream := "" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-\",\"function\":{\"name\":\"create_\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"model\",\"function\":{\"name\":\"model_3d\",\"arguments\":\"\\\"x\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/chat", ""), jsonResponse(200, "text/event-stream", stream))
	policy := testPolicy()
	allowPolicy(&policy, []string{"tool_calls", "index", "function", "name", "arguments"}, []string{"create_model_3d", "prompt", "deepseek"})
	policy.Projection = map[string]string{"id": "id"}
	if err := r.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	if err := r.AssertComplete(); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredProjectedCumulativeToolArguments(t *testing.T) {
	r := deferredRecorder(t)
	stream := sseStream(t,
		toolCallChunk(0, "call-1", "create_model_3d", `{"prompt":"x"}`),
		toolCallChunk(0, "", "", `{"prompt":"x"}`),
	)
	capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/chat", ""), jsonResponse(200, "text/event-stream", stream))
	policy := testPolicy()
	allowPolicy(&policy, []string{"tool_calls", "index", "function", "name", "arguments"}, []string{"create_model_3d", "prompt", "deepseek"})
	policy.Projection = map[string]string{"id": "id"}
	if err := r.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	if err := r.AssertComplete(); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredFinalizeSingleUseAndLateRequest(t *testing.T) {
	r := deferredRecorder(t)
	capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""), jsonResponse(200, "application/json", `{}`))
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- r.Finalize(testPolicy()) }()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, wirefixture.ErrCaptureSealed)) || (second == nil && errors.Is(first, wirefixture.ErrCaptureSealed))) {
		t.Fatalf("concurrent Finalize = %v / %v", first, second)
	}
	_, body := capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""), jsonResponse(200, "application/json", `{"id":"private late result"}`))
	if string(body) != `{"id":"private late result"}` || !errors.Is(r.AssertComplete(), wirefixture.ErrCaptureSealed) {
		t.Fatal("post-seal request changed business or did not invalidate evidence")
	}
	assertNoDeferredExport(t, r, testPolicy())
}

func TestDeferredWaitsForRequestAndRoundTrip(t *testing.T) {
	r := deferredRecorder(t)
	var requestBody io.ReadCloser
	entered, returnResponse := make(chan struct{}), make(chan struct{})
	done := make(chan *http.Response, 1)
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestBody = req.Body
		close(entered)
		<-returnResponse
		return &http.Response{StatusCode: 204, Body: http.NoBody}, nil
	})
	go func() {
		resp, _ := r.Wrap("hyper3d", base).RoundTrip(mustRequest(t, "POST", "https://api.meshy.ai/v1/tasks", `{}`))
		done <- resp
	}()
	<-entered
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("Finalize during RoundTrip = %v", err)
	}
	close(returnResponse)
	<-done
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("Finalize before asynchronous request body finished = %v", err)
	}
	if _, err := io.Copy(io.Discard, requestBody); err != nil {
		t.Fatal(err)
	}
	if err := requestBody.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(testPolicy()); err != nil {
		t.Fatal(err)
	}
}

type deferredFinalizingBody struct {
	io.ReadCloser
	onClose func()
}

func (b *deferredFinalizingBody) Close() error {
	b.onClose()
	return b.ReadCloser.Close()
}

func TestDeferredFinalizeRejectsActiveCloseAndReentry(t *testing.T) {
	r := deferredRecorder(t)
	entered, finishClose := make(chan struct{}), make(chan struct{})
	reentrant := make(chan error, 1)
	body := &deferredFinalizingBody{ReadCloser: io.NopCloser(strings.NewReader(`{}`)), onClose: func() {
		reentrant <- r.Finalize(testPolicy())
		close(entered)
		<-finishClose
	}}
	resp, err := r.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, ContentLength: 2}, nil
	})).RoundTrip(mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- resp.Body.Close() }()
	<-entered
	// Let Close finish even when an assertion fails.
	defer close(finishClose)
	if err := <-reentrant; !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("Finalize reentered from Close = %v", err)
	}
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("Finalize during Close = %v", err)
	}
	// Release the gate before waiting, without closing it twice in the defer.
	finishClose <- struct{}{}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(testPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredEntryOrderNotCompletionOrder(t *testing.T) {
	r := deferredRecorder(t)
	entered := make(chan string, 2)
	releaseFirst := make(chan struct{})
	completed := make(chan string, 2)
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		entered <- name
		if name == "first" {
			<-releaseFirst
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"result":"` + name + `"}`)), ContentLength: -1}, nil
	})
	start := func(name string) {
		go func() {
			defer func() { completed <- name }()
			capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks/"+name, ""), base)
		}()
	}
	start("first")
	<-entered
	start("second")
	<-entered
	if got := <-completed; got != "second" {
		t.Fatalf("completion order = %s", got)
	}
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("Finalize with one in-flight request = %v", err)
	}
	close(releaseFirst)
	<-completed
	if err := r.Finalize(testPolicy()); err != nil {
		t.Fatal(err)
	}
	for i, e := range r.Exchanges() {
		want := []string{"first", "second"}[i]
		if e.Sequence != i+1 || !strings.HasSuffix(e.Request.Path, want) || !bytes.Contains(e.Response.Body.JSON, []byte(want)) {
			t.Fatalf("wrong entry order: %+v", e)
		}
	}
}

type deferredErrorBody struct {
	onceBody
	closeErr error
}

func (b *deferredErrorBody) Close() error {
	b.closed = true
	return b.closeErr
}

func TestDeferredReadAndCloseAreTransparent(t *testing.T) {
	for _, side := range []string{"request", "response"} {
		for _, failure := range []string{"none", "read", "close", "close EOF"} {
			t.Run(side+"/"+failure, func(t *testing.T) {
				r := deferredRecorder(t)
				readErr, closeErr := errors.New("private read detail"), errors.New("private close detail")
				body := &deferredErrorBody{onceBody: onceBody{data: []byte(`{}`), err: io.EOF}}
				if failure == "read" {
					body.err = readErr
				}
				if failure == "close" {
					body.closeErr = closeErr
				}
				if failure == "close EOF" {
					body.closeErr = io.EOF
				}
				req := mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", "")
				var wrapped io.ReadCloser
				base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if side == "request" {
						wrapped = req.Body
						return &http.Response{StatusCode: 204, Body: http.NoBody}, nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, ContentLength: 2}, nil
				})
				if side == "request" {
					req.Method, req.Body, req.ContentLength = "POST", body, 2
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := r.Wrap("hyper3d", base).RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				if side == "response" {
					wrapped = resp.Body
				}
				if body.reads != 0 {
					t.Fatal("RoundTrip pre-read a body")
				}
				got, err := io.ReadAll(wrapped)
				if string(got) != `{}` || (failure == "read" && err != readErr) || (failure != "read" && err != nil) {
					t.Fatalf("Read changed bytes/error: %q %v", got, err)
				}
				reads := body.reads
				if err := wrapped.Close(); err != body.closeErr || body.reads != reads || !body.closed {
					t.Fatalf("Close changed error or drained body: %v, reads %d -> %d", err, reads, body.reads)
				}
				if err := r.Finalize(testPolicy()); (failure == "none") != (err == nil) {
					t.Fatalf("Finalize = %v", err)
				}
				if strings.Contains(fmt.Sprint(r.Failures()), "private") {
					t.Fatal("underlying error details escaped")
				}
			})
		}
	}
	// In contrast to immediate JSON capture, Close must not finish an unread body.
	r := deferredRecorder(t)
	body := &onceBody{data: []byte(`{}`), err: io.EOF}
	resp, err := r.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, ContentLength: 2}, nil
	})).RoundTrip(mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil || body.reads != 0 || !body.closed {
		t.Fatal("unread Close was not a pure passthrough")
	}
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("unread body accepted: %v", err)
	}
}

func TestDeferredInvalidEvidenceNeverRetriesOrRewritesRequests(t *testing.T) {
	for _, state := range []string{"over budget", "released", "sealed", "invalid URL"} {
		t.Run(state, func(t *testing.T) {
			r := deferredRecorder(t)
			if state == "over budget" {
				r = newDeferred(t, wirefixture.DeferredLimits{MaxBytes: 1, MaxExchanges: 1})
			}
			if state == "released" {
				_ = r.Release()
			}
			if state == "sealed" {
				capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", ""), jsonResponse(200, "application/json", `{}`))
				if err := r.Finalize(testPolicy()); err != nil {
					t.Fatal(err)
				}
			}
			req := mustRequest(t, "POST", "https://api.meshy.ai/v1/tasks?private=query", `{"prompt":"private input"}`)
			if state == "invalid URL" {
				req.URL = nil
			}
			originalBody, originalURL, originalGetBody := req.Body, req.URL, req.GetBody
			calls, getBodyCalls := 0, 0
			req.GetBody = func() (io.ReadCloser, error) {
				getBodyCalls++
				return originalGetBody()
			}
			getBodyPointer := reflect.ValueOf(req.GetBody).Pointer()
			transportErr := errors.New("private transport detail")
			base := roundTripFunc(func(got *http.Request) (*http.Response, error) {
				calls++
				if got.URL != originalURL || got.ContentLength != req.ContentLength || reflect.ValueOf(got.GetBody).Pointer() != getBodyPointer {
					t.Fatal("request URL, ContentLength or GetBody changed")
				}
				data, err := io.ReadAll(got.Body)
				if err != nil || string(data) != `{"prompt":"private input"}` {
					t.Fatal("business request body changed")
				}
				return nil, transportErr
			})
			_, err := r.Wrap("hyper3d", base).RoundTrip(req)
			if err != transportErr || calls != 1 || getBodyCalls != 0 || req.Body != originalBody || req.URL != originalURL {
				t.Fatalf("evidence failure affected business: err=%v calls=%d GetBody=%d", err, calls, getBodyCalls)
			}
			assertNoDeferredExport(t, r, testPolicy())
		})
	}
}

func TestDeferredBodyBudgetFailurePreservesBusinessIO(t *testing.T) {
	for _, side := range []string{"request", "response"} {
		t.Run(side, func(t *testing.T) {
			r := newDeferred(t, wirefixture.DeferredLimits{MaxBytes: 2048, MaxExchanges: 1})
			raw := strings.Repeat("x", 4096)
			readErr := errors.New("private body read failure")
			body := io.NopCloser(io.MultiReader(strings.NewReader(raw), &onceBody{err: readErr}))
			requestRaw := `{}`
			if side == "request" {
				requestRaw = raw
			}
			req := mustRequest(t, "POST", "https://api.meshy.ai/v1/tasks", requestRaw)
			if side == "request" {
				req.Body = body
			}
			originalBody, originalURL, originalGetBody := req.Body, req.URL, req.GetBody
			calls, getBodyCalls := 0, 0
			req.GetBody = func() (io.ReadCloser, error) {
				getBodyCalls++
				return originalGetBody()
			}
			getBodyPointer := reflect.ValueOf(req.GetBody).Pointer()
			resp, err := r.Wrap("hyper3d", roundTripFunc(func(got *http.Request) (*http.Response, error) {
				calls++
				if got.URL != originalURL || got.ContentLength != req.ContentLength || reflect.ValueOf(got.GetBody).Pointer() != getBodyPointer {
					t.Fatal("in-flight request properties changed")
				}
				data, err := io.ReadAll(got.Body)
				if string(data) != requestRaw || (side == "request" && err != readErr) || (side == "response" && err != nil) {
					t.Fatalf("request bytes/error changed: %d bytes, %v", len(data), err)
				}
				_ = got.Body.Close()
				if side == "request" {
					return &http.Response{StatusCode: 204, Body: http.NoBody}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, ContentLength: int64(len(raw))}, nil
			})).RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			if side == "response" && (string(data) != raw || err != readErr) {
				t.Fatalf("response bytes/error changed: %d bytes, %v", len(data), err)
			}
			_ = resp.Body.Close()
			if calls != 1 || getBodyCalls != 0 || req.Body != originalBody || req.URL != originalURL || !errors.Is(r.AssertComplete(), wirefixture.ErrCaptureLimit) {
				t.Fatalf("body limit changed transport or was ignored: calls=%d GetBody=%d failure=%v", calls, getBodyCalls, r.AssertComplete())
			}
			assertNoDeferredExport(t, r, testPolicy())
		})
	}
}

func TestDeferredConcurrentObserversAndCancellation(t *testing.T) {
	r := deferredRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := r.Wrap("hyper3d", jsonResponse(200, "application/json", `{}`)).RoundTrip(mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", "").WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = r.Exchanges()
				_ = r.Failures()
				_ = r.AssertComplete()
				_ = r.Finalize(testPolicy())
			}
		}()
	}
	cancel()
	_ = r.Release()
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	wg.Wait()
	assertNoDeferredExport(t, r, testPolicy())
}

func TestDeferredAlreadyCanceledRequestStillPassesThrough(t *testing.T) {
	r := deferredRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, body := capture(t, r, mustRequest(t, "GET", "https://api.meshy.ai/v1/tasks", "").WithContext(ctx), roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if len(r.Failures()) != 0 {
			t.Fatal("canceled request started collecting raw evidence")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{}`)), ContentLength: 2}, nil
	}))
	if string(body) != `{}` || calls != 1 {
		t.Fatal("cancellation changed business I/O")
	}
	if err := r.Finalize(testPolicy()); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("canceled request produced evidence: %v", err)
	}
	assertNoDeferredExport(t, r, testPolicy())
}

func TestDeferredRetriesAssetTransportFailure(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := testPolicy()
	policy.PublicHosts = map[string]bool{"assets.meshy.ai": true}
	policy.Keys["ok"] = true
	policy.Allowed["output"] = true
	policy.Allowed["model.3mf"] = true
	request, _ := http.NewRequest(http.MethodGet, "https://assets.meshy.ai/output/model.3mf", nil)
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		body := `{"ok":true}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	if _, err := recorder.Wrap("meshy", base).RoundTrip(request); err != io.ErrUnexpectedEOF {
		t.Fatalf("first attempt = %v", err)
	}
	response, err := recorder.Wrap("meshy", base).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 || exchanges[0].Sequence != 2 || calls != 2 {
		t.Fatalf("exchanges = %+v calls=%d", exchanges, calls)
	}
	if err := recorder.AssertComplete(); err != nil {
		t.Fatal(err)
	}
}
