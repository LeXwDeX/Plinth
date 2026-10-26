package wirefixture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type chunkBody struct {
	chunks [][]byte
	index  int
	offset int
}

func (b *chunkBody) Read(p []byte) (int, error) {
	if b.index >= len(b.chunks) {
		return 0, io.EOF
	}
	n := copy(p, b.chunks[b.index][b.offset:])
	b.offset += n
	if b.offset == len(b.chunks[b.index]) {
		b.index++
		b.offset = 0
	}
	return n, nil
}

func (b *chunkBody) Close() error { return nil }

type hangingBody struct {
	ctx     context.Context
	blocked chan struct{}
	once    sync.Once
	reads   int
}

func (b *hangingBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 1 {
		return copy(p, []byte(`{}`)), nil
	}
	b.once.Do(func() { close(b.blocked) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *hangingBody) Close() error { return nil }

type onceBody struct {
	data   []byte
	err    error
	reads  int
	closed bool
}

func (b *onceBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 1 {
		return copy(p, b.data), nil
	}
	return 0, b.err
}

func (b *onceBody) Close() error {
	b.closed = true
	return nil
}

func testMetadata() wirefixture.Metadata {
	return wirefixture.Metadata{
		Scenario:        "rodin",
		Connection:      "hyper3d",
		APIVersion:      "v1",
		RequestedModel:  "rodin-2.0-requested",
		ReportedModel:   "rodin-2.0-reported",
		ServerBuild:     "build-2026-10-25",
		CapturedAt:      time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC),
		AppRevision:     "d0598838",
		CatalogRevision: "catalog-1",
		Environment:     "test",
		Provenance:      wirefixture.ProvenanceSynthetic,
	}
}

func testPolicy() wirefixture.Policy {
	return wirefixture.Policy{
		MaxBodyBytes: 1 << 20,
		Keys: map[string]bool{
			"prompt": true, "result": true, "message": true, "url": true,
			"tool_call_id": true, "id": true, "role": true, "choices": true,
			"delta": true, "content": true, "job_id": true, "status": true,
		},
		Allowed: map[string]bool{
			"synthetic prompt": true, "synthetic result": true,
			"synthetic result <&>": true, "assistant": true, "stop": true,
			"v1": true, "application/json": true, "text/event-stream": true,
			"call-1": true, "tasks": true, "chat": true, "first": true, "second": true,
			"completions": true, "localhost": true, "https://provider.example/x?token=1": true,
			"100.64.0.1": true, "198.18.0.1": true, "fd00::1": true, "fe80::1": true,
			"service.corp": true, "0": true, "1": true, "1.5": true, "2": true,
		},
		Aliases: map[string]string{
			"api.meshy.ai":                           "example.test",
			"call_live":                              "call-1",
			"https://api.meshy.ai/file?token=secret": "https://example.test/file",
		},
	}
}

func mustRequest(t *testing.T, method, rawURL, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, rawURL, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Api-Version", "v1")
	}
	return request
}

func jsonResponse(status int, contentType, body string) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Body != nil {
			_, _ = io.Copy(io.Discard, request.Body)
		}
		header := http.Header{}
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
}

func capture(t *testing.T, recorder *wirefixture.Recorder, request *http.Request, base http.RoundTripper) (*http.Response, []byte) {
	t.Helper()
	response, err := recorder.Wrap("hyper3d", base).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil {
		t.Fatal("nil response")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return response, body
}

func TestCaptureSanitizesAndPreservesResponse(t *testing.T) {
	policy := testPolicy()
	policy.Keys["subscription_key"] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	var calls int
	var baseGetBody bool
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		baseGetBody = request.GetBody != nil
		if request.Body != nil {
			_, _ = io.Copy(io.Discard, request.Body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": {"application/json"},
				"Retry-After":  {"5"},
				"Set-Cookie":   {"secret=1"},
			},
			Body:          io.NopCloser(strings.NewReader(`{"result":"synthetic result <&>","tool_call_id":"call_live","url":"https://api.meshy.ai/file?token=secret"}`)),
			ContentLength: int64(len(`{"result":"synthetic result <&>","tool_call_id":"call_live","url":"https://api.meshy.ai/file?token=secret"}`)),
		}, nil
	})
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt","tool_call_id":"call_live","subscription_key":"rodin-secret"}`)
	request.Header.Set("Authorization", "Bearer secret-token")
	request.Header.Set("Cookie", "session=secret")
	originalGetBody := request.GetBody
	response, body := capture(t, recorder, request, base)
	if string(body) != `{"result":"synthetic result <&>","tool_call_id":"call_live","url":"https://api.meshy.ai/file?token=secret"}` {
		t.Fatalf("business response changed: %s", body)
	}
	if calls != 1 || !baseGetBody || originalGetBody == nil || request.GetBody == nil {
		t.Fatalf("calls=%d baseGetBody=%v originalGetBody=%v", calls, baseGetBody, originalGetBody != nil)
	}
	if response.Header.Get("Set-Cookie") != "secret=1" {
		t.Fatal("business response headers changed")
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchanges = %d", len(exchanges))
	}
	exchange := exchanges[0]
	if !exchange.Complete || exchange.Invalid != "" {
		t.Fatalf("exchange invalid: %+v", exchange)
	}
	if exchange.Sequence != 1 || exchange.Metadata.CapturedAt.Location() != time.UTC {
		t.Fatalf("sequence/time: %+v", exchange)
	}
	if exchange.Metadata.Protocol != "json" || exchange.Metadata.Endpoint != "POST /v1/tasks" {
		t.Fatalf("metadata protocol/endpoint: %+v", exchange.Metadata)
	}
	if exchange.Metadata.APIVersion == exchange.Metadata.RequestedModel ||
		exchange.Metadata.RequestedModel == exchange.Metadata.ReportedModel ||
		exchange.Metadata.ReportedModel == exchange.Metadata.ServerBuild {
		t.Fatalf("metadata fields are not distinct: %+v", exchange.Metadata)
	}
	if exchange.Request.Host != "example.test" || exchange.Request.Path != "/v1/tasks" || exchange.Request.ContentType != "application/json" {
		t.Fatalf("request fields: %+v", exchange.Request)
	}
	if exchange.Request.Headers["authorization"] != "" || exchange.Request.Headers["cookie"] != "" {
		t.Fatalf("credential headers leaked: %+v", exchange.Request.Headers)
	}
	if exchange.Request.Headers["accept"] != "application/json" || exchange.Request.Headers["x-api-version"] != "v1" {
		t.Fatalf("allowed headers: %+v", exchange.Request.Headers)
	}
	requestBody := string(exchange.Request.Body.JSON)
	if strings.Contains(requestBody, "rodin-secret") || !strings.Contains(requestBody, `"call-1"`) || !strings.Contains(requestBody, `"subscription_key":"[redacted]"`) {
		t.Fatalf("request body: %s", requestBody)
	}
	responseBody := string(exchange.Response.Body.JSON)
	if strings.Contains(responseBody, "secret") || !strings.Contains(responseBody, `"https://example.test/file"`) ||
		!strings.Contains(responseBody, `"call-1"`) || !strings.Contains(responseBody, "synthetic result <&>") {
		t.Fatalf("response body: %s", responseBody)
	}
	if exchange.Response.Headers["retry-after"] != "5" || exchange.Response.Headers["set-cookie"] != "" {
		t.Fatalf("response headers: %+v", exchange.Response.Headers)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatalf("valid exchange did not validate: %v", err)
	}
}

func TestUnknownValuesAndUnsupportedBodiesFailClosed(t *testing.T) {
	policy := testPolicy()
	cases := []struct {
		name        string
		body        string
		response    http.RoundTripper
		unsupported bool
	}{
		{"unapproved string", `{"message":"private value"}`, jsonResponse(200, "application/json", `{}`), false},
		{"unknown nonsecret key", `{"new_field":"synthetic result"}`, jsonResponse(200, "application/json", `{}`), false},
		{"unregistered URL", `{"url":"https://provider.example/x?token=1"}`, jsonResponse(200, "application/json", `{}`), false},
		{"private host even if allowed", `{"message":"localhost"}`, jsonResponse(200, "application/json", `{}`), false},
		{"URL inside free text", `{"prompt":"synthetic prompt https://api.meshy.ai/x"}`, jsonResponse(200, "application/json", `{}`), false},
		{"CGNAT", `{"message":"100.64.0.1"}`, jsonResponse(200, "application/json", `{}`), false},
		{"benchmark", `{"message":"198.18.0.1"}`, jsonResponse(200, "application/json", `{}`), false},
		{"ULA", `{"message":"fd00::1"}`, jsonResponse(200, "application/json", `{}`), false},
		{"link local", `{"message":"fe80::1"}`, jsonResponse(200, "application/json", `{}`), false},
		{"corp DNS", `{"message":"service.corp"}`, jsonResponse(200, "application/json", `{}`), false},
		{"unsupported multipart", `{"prompt":"synthetic prompt"}`, jsonResponse(200, "multipart/form-data; boundary=x", `--x--`), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", tc.body)
			_, body := capture(t, recorder, request, tc.response)
			exchange := recorder.Exchanges()[0]
			if exchange.Complete || exchange.Invalid == "" {
				t.Fatalf("unsafe exchange accepted: %+v", exchange)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
				t.Fatal("unsafe exchange validated")
			}
			if tc.unsupported && !strings.Contains(exchange.Invalid, "unsupported") {
				t.Fatalf("unsupported reason missing: %s", exchange.Invalid)
			}
			if tc.unsupported && string(body) != "--x--" {
				t.Fatalf("business response changed: %q", body)
			}
			if !tc.unsupported && string(body) != "{}" {
				t.Fatalf("business response changed: %q", body)
			}
		})
	}

	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks?token=secret", `{"prompt":"synthetic prompt"}`)
	_, body := capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	if string(body) != "{}" {
		t.Fatalf("query business response changed: %q", body)
	}
	if exchange := recorder.Exchanges()[0]; exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("query request accepted: %+v", exchange)
	}

	recorder = &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request = mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt"}`)
	_, body = capture(t, recorder, request, jsonResponse(200, "multipart/form-data; boundary=x", `binary-body`))
	if string(body) != "binary-body" {
		t.Fatalf("business response changed: %q", body)
	}
}

func TestSequenceUsesRequestArrivalOrder(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	started := make(chan struct{})
	release := make(chan struct{})
	firstBase := roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), ContentLength: 2}, nil
	})
	first := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/first", "")
	done := make(chan error, 1)
	go func() {
		response, err := recorder.Wrap("hyper3d", firstBase).RoundTrip(first)
		if err == nil {
			_, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-started
	second := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/second", "")
	_, _ = capture(t, recorder, second, jsonResponse(200, "application/json", `{}`))
	if exchanges := recorder.Exchanges(); len(exchanges) != 1 || exchanges[0].Sequence != 2 {
		t.Fatalf("second exchange before first completed: %+v", exchanges)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 2 || exchanges[0].Sequence != 1 || exchanges[1].Sequence != 2 {
		t.Fatalf("arrival order: %+v", exchanges)
	}
	if exchanges[0].Request.Path != "/v1/first" || exchanges[1].Request.Path != "/v1/second" {
		t.Fatalf("paths: %+v", exchanges)
	}
}

func replayRequest(t *testing.T, body, accept, version string, query bool) *http.Request {
	t.Helper()
	rawURL := "https://example.test/v1/tasks"
	if query {
		rawURL += "?x=1"
	}
	request := mustRequest(t, http.MethodPost, rawURL, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Del("Accept")
	request.Header.Del("X-Api-Version")
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if version != "" {
		request.Header.Set("X-Api-Version", version)
	}
	request.Header.Set("Authorization", "Bearer ignored")
	request.Header.Set("User-Agent", "ignored")
	return request
}

func TestAliasesReplayOrderAndHeaders(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	first := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt","tool_call_id":"call_live"}`)
	_, _ = capture(t, recorder, first, jsonResponse(200, "application/json", `{"tool_call_id":"call_live","result":"synthetic result"}`))
	second := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic result","tool_call_id":"call-1"}`)
	_, _ = capture(t, recorder, second, jsonResponse(200, "application/json", `{"result":"synthetic result"}`))
	exchanges := recorder.Exchanges()
	if len(exchanges) != 2 {
		t.Fatalf("exchanges = %d", len(exchanges))
	}
	if !strings.Contains(string(exchanges[0].Response.Body.JSON), `"call-1"`) {
		t.Fatalf("alias missing: %s", exchanges[0].Response.Body.JSON)
	}

	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.RoundTrip(replayRequest(t, `{"prompt":"synthetic result","tool_call_id":"call-1"}`, "application/json", "v1", false)); !errors.Is(err, wirefixture.ErrReorderedRequest) {
		t.Fatalf("reordered error = %v", err)
	}
	replay, err = wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.RoundTrip(replayRequest(t, `{"prompt":"synthetic prompt","tool_call_id":"call_live"}`, "application/json", "v1", false))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), `"call-1"`) {
		t.Fatalf("replay response: %s", body)
	}
	response, err = replay.RoundTrip(replayRequest(t, `{"prompt":"synthetic result","tool_call_id":"call-1"}`, "application/json", "v1", false))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if _, err := replay.RoundTrip(replayRequest(t, `{"prompt":"synthetic result","tool_call_id":"call-1"}`, "application/json", "v1", false)); !errors.Is(err, wirefixture.ErrExhaustedRequests) {
		t.Fatalf("exhausted error = %v", err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("exhausted replay assertion = %v", err)
	}

	cases := map[string]struct {
		accept  string
		version string
		query   bool
	}{
		"wrong accept":    {"text/plain", "v1", false},
		"missing version": {"application/json", "", false},
		"query":           {"application/json", "v1", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fresh, err := wirefixture.NewReplay(exchanges, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.RoundTrip(replayRequest(t, `{"prompt":"synthetic prompt","tool_call_id":"call_live"}`, tc.accept, tc.version, tc.query)); !errors.Is(err, wirefixture.ErrUnexpectedRequest) {
				t.Fatalf("header/query error = %v", err)
			}
		})
	}
}

func TestSSEStrictJSONAndDONE(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{"object", "created", "model", "index", "finish_reason"},
		[]string{"chat.completion.chunk", "configured-model"})
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/chat", "")
	stream := `data: {"id":"call_live","object":"chat.completion.chunk","created":1,"model":"configured-model","choices":[{"index":0,"delta":{"content":"synthetic result"},"finish_reason":null}]}` + "\n\n" + "data: [DONE]\n\n"
	_, _ = capture(t, recorder, request, jsonResponse(200, "text/event-stream", stream))
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete || exchange.Metadata.Protocol != "sse" {
		t.Fatalf("SSE exchange: %+v", exchange)
	}
	if strings.Contains(exchange.Response.Body.SSE, "call_live") || !strings.Contains(exchange.Response.Body.SSE, "call-1") || !strings.Contains(exchange.Response.Body.SSE, "data: [DONE]") {
		t.Fatalf("SSE body: %s", exchange.Response.Body.SSE)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
		t.Fatal(err)
	}

	partial := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, partial, request, jsonResponse(200, "text/event-stream", `data: {"choices":[{"delta":{"content":"synthetic result"}}]}`+"\n\n"))
	if err := wirefixture.Validate(partial.Exchanges(), policy); !errors.Is(err, wirefixture.ErrPartialSSE) {
		t.Fatalf("partial SSE error = %v", err)
	}

	evented := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, evented, request, jsonResponse(200, "text/event-stream", "event: message\ndata: {\"choices\":[]}\n\n"))
	if err := wirefixture.Validate(evented.Exchanges(), policy); !errors.Is(err, wirefixture.ErrUnsupportedBody) {
		t.Fatalf("evented SSE error = %v", err)
	}

	unknown := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, unknown, request, jsonResponse(200, "text/event-stream", `data: {"choices":[{"delta":{"content":"private value"}}]}`+"\n\ndata: [DONE]\n\n"))
	if err := wirefixture.Validate(unknown.Exchanges(), policy); err == nil {
		t.Fatal("unknown SSE string validated")
	}
}

func TestWriteCandidateExclusiveAndUnsupported(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt"}`)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{"result":"synthetic result <&>"}`))
	exchanges := recorder.Exchanges()
	root := t.TempDir()
	dir, err := os.MkdirTemp("", "wirefixture-export-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "golden.json")
	if err := wirefixture.WriteCandidate(root, path, exchanges, policy); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded []wirefixture.Exchange
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.Validate(loaded, policy); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("synthetic result <&>")) {
		t.Fatalf("HTML escaping changed public bytes: %s", data)
	}
	if err := wirefixture.WriteCandidate(root, path, exchanges, policy); !errors.Is(err, wirefixture.ErrExistingCandidate) {
		t.Fatalf("overwrite error = %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.WriteCandidate(root, link, exchanges, policy); !errors.Is(err, wirefixture.ErrExistingCandidate) {
		t.Fatalf("symlink error = %v", err)
	}

	inner := filepath.Join(root, "private")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.WriteCandidate(root, filepath.Join(inner, "inside.json"), exchanges, policy); !errors.Is(err, wirefixture.ErrInsideWorktree) {
		t.Fatalf("inside error = %v", err)
	}

	unsupported := exchanges[0]
	unsupported.Response.ContentType = "multipart/form-data"
	unsupported.Response.Headers = nil
	unsupported.Response.Body = wirefixture.Payload{}
	unsupportedPath := filepath.Join(dir, "unsupported.json")
	if err := wirefixture.WriteCandidate(root, unsupportedPath, []wirefixture.Exchange{unsupported}, policy); !errors.Is(err, wirefixture.ErrUnsupportedBody) {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := os.Lstat(unsupportedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported file exists: %v", err)
	}
}

func TestBoundedBodyDoesNotChangeResponse(t *testing.T) {
	policy := testPolicy()
	policy.MaxBodyBytes = 8
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	_, body := capture(t, recorder, request, jsonResponse(200, "application/json", `{"result":"synthetic result"}`))
	if string(body) != `{"result":"synthetic result"}` {
		t.Fatalf("business response changed: %s", body)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || !strings.Contains(exchange.Invalid, "truncated") {
		t.Fatalf("bounded exchange: %+v", exchange)
	}
}

func TestJSONTypesArePreserved(t *testing.T) {
	policy := testPolicy()
	for _, key := range []string{"n", "b", "z", "s"} {
		policy.Keys[key] = true
	}
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"n":1.5,"b":true,"z":null,"s":"synthetic result"}`)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{"n":1.5,"b":true,"z":null,"s":"synthetic result"}`))
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete {
		t.Fatalf("typed exchange: %+v", exchange)
	}
	text := string(exchange.Response.Body.JSON)
	if !strings.Contains(text, `"n":1.5`) || !strings.Contains(text, `"b":true`) || !strings.Contains(text, `"z":null`) {
		t.Fatalf("typed JSON: %s", text)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
		t.Fatal(err)
	}
}

func TestTransportFailureDoesNotCreateExchange(t *testing.T) {
	sentinel := errors.New("dial failed")
	recorder := &wirefixture.Recorder{Policy: testPolicy(), Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})).RoundTrip(request)
	if response != nil || !errors.Is(err, sentinel) {
		t.Fatalf("response=%v err=%v", response, err)
	}
	failures := recorder.Failures()
	if len(failures) != 1 || !errors.Is(failures[0], wirefixture.ErrTransportFailure) {
		t.Fatalf("failures=%v", failures)
	}
	if strings.Contains(failures[0].Error(), "dial failed") {
		t.Fatalf("failure exposed upstream error: %v", failures[0])
	}
	if len(recorder.Exchanges()) != 0 {
		t.Fatalf("exchanges=%d", len(recorder.Exchanges()))
	}
}

func TestMetadataRejectsPrivateFieldsAndDefaultProvenance(t *testing.T) {
	policy := testPolicy()
	for name, meta := range map[string]wirefixture.Metadata{
		"private model":      func() wirefixture.Metadata { m := testMetadata(); m.RequestedModel = "service.corp"; return m }(),
		"missing provenance": func() wirefixture.Metadata { m := testMetadata(); m.Provenance = ""; return m }(),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: meta}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt"}`)
			_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
			exchange := recorder.Exchanges()[0]
			if exchange.Complete || exchange.Invalid == "" {
				t.Fatalf("invalid metadata accepted: %+v", exchange)
			}
			if exchange.Metadata != (wirefixture.Metadata{}) {
				t.Fatalf("invalid metadata exposed: %+v", exchange.Metadata)
			}
		})
	}
}

func TestNestedJSONCannotPublishRawCredentials(t *testing.T) {
	policy := testPolicy()
	for _, key := range []string{"arguments", "payload", "password", "token"} {
		policy.Keys[key] = true
	}
	malicious := `{"password":"hunter2"}`
	policy.Allowed[malicious] = true
	policy.Allowed["token=abc"] = true
	policy.Allowed[`{"password":"hunter2"`] = true
	policy.Allowed[`password="hunter2"`] = true
	policy.Allowed[`password: "hunter2"`] = true
	policy.Allowed[`token='abc'`] = true
	policy.Aliases["raw-nested"] = `{"password":"public"}`
	policy.Aliases["raw-secret"] = "[redacted]"

	cases := []struct {
		name string
		body string
		keys []string
	}{
		{"nested password sanitized", `{"arguments":"{\"password\":\"hunter2\"}"}`, nil},
		{"nested token redacted", `{"payload":"{\"token\":\"abc\"}"}`, nil},
		{"plain token assignment redacted", `{"message":"token=abc"}`, nil},
		{"alias replacement nested sanitized", `{"message":"raw-nested"}`, nil},
		{"alias to redacted", `{"message":"raw-secret"}`, nil},
		{"malformed nested JSON", `{"message":"{\"password\":\"hunter2\""}`, nil},
		{"quoted assignment", `{"message":"password=\"hunter2\""}`, nil},
		{"quoted colon assignment", `{"message":"password: \"hunter2\""}`, nil},
		{"single quoted assignment", `{"message":"token='abc'"}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := policy
			for _, key := range tc.keys {
				current.Keys[key] = true
			}
			recorder := &wirefixture.Recorder{Policy: current, Default: testMetadata()}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", tc.body)
			_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
			exchange := recorder.Exchanges()[0]
			for _, raw := range []string{"hunter2", "token=abc"} {
				if strings.Contains(string(exchange.Request.Body.JSON), raw) || strings.Contains(exchange.Invalid, raw) {
					t.Fatalf("raw credential published: body=%s invalid=%s", exchange.Request.Body.JSON, exchange.Invalid)
				}
			}
			if !exchange.Complete && exchange.Invalid == "" {
				t.Fatalf("nested value neither sanitized nor rejected: %+v", exchange)
			}
			if exchange.Complete {
				if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, current); err != nil {
					t.Fatalf("sanitized nested value did not validate: %v", err)
				}
			}
		})
	}
}

func TestApprovedBracketTextSurvivesJSONAndSSE(t *testing.T) {
	policy := testPolicy()
	for _, value := range []string{"[Image result]", "{note} ready"} {
		policy.Allowed[value] = true
	}
	policy.Keys["index"] = true
	policy.Aliases["bracket-alias"] = "[Image result]"

	cases := []struct {
		name        string
		contentType string
		response    string
		want        []string
	}{
		{
			name:        "json",
			contentType: "application/json",
			response:    `{"message":"[Image result]","result":"{note} ready","url":"bracket-alias"}`,
			want:        []string{`"[Image result]"`, `"{note} ready"`, `"url":"[Image result]"`},
		},
		{
			name:        "sse",
			contentType: "text/event-stream",
			response: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"[Image result]\"}}," +
				"{\"index\":1,\"delta\":{\"content\":\"{note} ready\"}}," +
				"{\"index\":2,\"delta\":{\"content\":\"bracket-alias\"}}]}\n\ndata: [DONE]\n\n",
			want: []string{`[Image result]`, `{note} ready`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
			request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
			_, body := capture(t, recorder, request, jsonResponse(200, tc.contentType, tc.response))
			if string(body) != tc.response {
				t.Fatalf("business response changed: %s", body)
			}
			exchanges := recorder.Exchanges()
			if len(exchanges) != 1 || !exchanges[0].Complete {
				t.Fatalf("bracket text capture: %+v", exchanges)
			}
			payload := string(exchanges[0].Response.Body.JSON)
			if tc.contentType == "text/event-stream" {
				payload = exchanges[0].Response.Body.SSE
			}
			for _, want := range tc.want {
				if !strings.Contains(payload, want) {
					t.Fatalf("missing %s in %s", want, payload)
				}
			}
			if err := wirefixture.Validate(exchanges, policy); err != nil {
				t.Fatal(err)
			}
			replay, err := wirefixture.NewReplay(exchanges, policy)
			if err != nil {
				t.Fatal(err)
			}
			response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", ""))
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if string(replayed) != payload {
				t.Fatalf("replayed payload = %s, want %s", replayed, payload)
			}
			if err := replay.AssertConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplayRequiresApprovedHost(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt"}`)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	exchanges := recorder.Exchanges()
	if exchanges[0].Request.Host != "example.test" {
		t.Fatalf("host = %q", exchanges[0].Request.Host)
	}
	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	evil := replayRequest(t, `{"prompt":"synthetic prompt"}`, "application/json", "v1", false)
	evil.URL.Host = "evil.example"
	evil.Host = "evil.example"
	if _, err := replay.RoundTrip(evil); !errors.Is(err, wirefixture.ErrUnexpectedRequest) {
		t.Fatalf("evil host error = %v", err)
	}
	replay, err = wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	original := replayRequest(t, `{"prompt":"synthetic prompt"}`, "application/json", "v1", false)
	original.URL.Host = "api.meshy.ai"
	original.Host = "api.meshy.ai"
	if _, err := replay.RoundTrip(original); err != nil {
		t.Fatalf("aliased host failed: %v", err)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAICompatibleStreamAndCompleteThroughRecorder(t *testing.T) {
	policy := testPolicy()
	for _, key := range []string{"model", "messages", "stream", "temperature", "message"} {
		policy.Keys[key] = true
	}
	for _, value := range []string{"configured-model", "user", "hello"} {
		policy.Allowed[value] = true
	}
	policy.Aliases["api.meshy.ai"] = "example.test"

	streamRecorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	streamBody := `data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n" + "data: [DONE]\n"
	bodyReader := &onceBody{data: []byte(streamBody), err: errors.New("must not read after DONE")}
	var streamCalls int
	streamBase := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		streamCalls++
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: bodyReader, ContentLength: -1}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://api.meshy.ai/v1", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", APIKey: "secret",
		RequestTimeout: time.Minute, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: streamRecorder.Wrap("agent", streamBase)},
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := adapter.Stream(context.Background(), agents.Request{Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hello"}}}, nil)
	if err != nil || completion.Content != "hello" {
		t.Fatalf("stream completion=%+v err=%v", completion, err)
	}
	if streamCalls != 1 || bodyReader.reads != 1 {
		t.Fatalf("stream calls=%d reads=%d", streamCalls, bodyReader.reads)
	}
	if exchanges := streamRecorder.Exchanges(); len(exchanges) != 1 || !exchanges[0].Complete ||
		!strings.Contains(exchanges[0].Response.Body.SSE, "data: [DONE]\n\n") {
		t.Fatalf("stream capture: %+v", exchanges)
	}

	completeRecorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	completeBody := `{"choices":[{"message":{"content":"hello"}}]}`
	completeBase := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: &chunkBody{chunks: [][]byte{[]byte(completeBody), []byte(" \n")}}, ContentLength: -1}, nil
	})
	adapter, err = openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://api.meshy.ai/v1", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", APIKey: "secret",
		RequestTimeout: time.Minute, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: completeRecorder.Wrap("agent", completeBase)},
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err = adapter.Complete(context.Background(), agents.Request{Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hello"}}})
	if err != nil || completion.Content != "hello" {
		t.Fatalf("complete completion=%+v err=%v", completion, err)
	}
	if exchanges := completeRecorder.Exchanges(); len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("complete capture: %+v", exchanges)
	}
}

func TestInvalidHostIsNotExposed(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://evil.example/v1/tasks", `{"prompt":"synthetic prompt"}`)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("unknown host accepted: %+v", exchange)
	}
	if exchange.Request.Host != "" || strings.Contains(exchange.Invalid, "evil.example") {
		t.Fatalf("host exposed: %+v", exchange)
	}
}

func TestJSONCloseDrainsChunkedBody(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	body := &chunkBody{chunks: [][]byte{[]byte(`{"result":"synthetic result"}`), []byte(" \n")}}
	var calls int
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: body, ContentLength: -1}, nil
	})).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if calls != 1 {
		t.Fatalf("capture calls = %d", calls)
	}
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete || string(exchange.Response.Body.JSON) != `{"result":"synthetic result"}` {
		t.Fatalf("chunked JSON capture: %+v", exchange)
	}
}

func TestJSONDrainCancellationDoesNotHang(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	ctx, cancel := context.WithCancel(context.Background())
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	request = request.WithContext(ctx)
	body := &hangingBody{ctx: ctx, blocked: make(chan struct{})}
	var calls int
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: body, ContentLength: -1}, nil
	})).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- response.Body.Close() }()
	<-body.blocked
	cancel()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("business close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not stop after cancellation")
	}
	if calls != 1 {
		t.Fatalf("capture calls = %d", calls)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("canceled drain capture: %+v", exchange)
	}
	if len(recorder.Failures()) != 0 {
		t.Fatalf("drain failure became upstream failure: %v", recorder.Failures())
	}
}

func TestSSEChunkStringCredentialRejected(t *testing.T) {
	policy := testPolicy()
	for _, key := range []string{"choices", "delta", "content"} {
		policy.Keys[key] = true
	}
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/chat", "")
	stream := `data: {"choices":[{"delta":{"content":"password=\"hunter2\""}}]}` + "\n\ndata: [DONE]\n\n"
	_, _ = capture(t, recorder, request, jsonResponse(200, "text/event-stream", stream))
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("SSE credential exchange accepted: %+v", exchange)
	}
	if strings.Contains(exchange.Invalid, "hunter2") || strings.Contains(exchange.Response.Body.SSE, "hunter2") {
		t.Fatalf("SSE credential leaked: %+v", exchange)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
		t.Fatal("SSE credential fixture validated")
	}
}

func TestReplayRejectsHostOverride(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", ""),
		jsonResponse(http.StatusOK, "application/json", `{}`))
	exchanges := recorder.Exchanges()

	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	override := mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")
	override.Host = "evil.example"
	if _, err := replay.RoundTrip(override); !errors.Is(err, wirefixture.ErrUnexpectedRequest) {
		t.Fatalf("host override error = %v", err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("host override assertion = %v", err)
	}

	replay, err = wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	same := mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")
	same.Host = "example.test"
	response, err := replay.RoundTrip(same)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestReplayExhaustedIsSticky(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", ""),
		jsonResponse(http.StatusOK, "application/json", `{}`))
	replay, err := wirefixture.NewReplay(recorder.Exchanges(), policy)
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", ""))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if _, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")); !errors.Is(err, wirefixture.ErrExhaustedRequests) {
		t.Fatalf("exhausted error = %v", err)
	}
	if _, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("sticky exhausted error = %v", err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("exhausted assertion = %v", err)
	}
}

func TestReplayUnconsumed(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	replay, err := wirefixture.NewReplay(recorder.Exchanges(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrUnconsumedRequest) {
		t.Fatalf("unconsumed error = %v", err)
	}
}

func TestReplaySnapshotsPolicy(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	body := `{"prompt":"synthetic prompt"}`
	_, _ = capture(t, recorder, mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", body),
		jsonResponse(200, "application/json", `{}`))
	replay, err := wirefixture.NewReplay(recorder.Exchanges(), policy)
	if err != nil {
		t.Fatal(err)
	}
	delete(policy.Allowed, "synthetic prompt")
	delete(policy.Keys, "prompt")
	delete(policy.Aliases, "api.meshy.ai")
	request := replayRequest(t, body, "application/json", "v1", false)
	request.URL.Host = "api.meshy.ai"
	request.Host = "api.meshy.ai"
	response, err := replay.RoundTrip(request)
	if err != nil {
		t.Fatalf("snapshot policy not retained: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteCandidateRejectsNonDirectoryRoot(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, recorder, mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"prompt":"synthetic prompt"}`),
		jsonResponse(200, "application/json", `{}`))
	exchanges := recorder.Exchanges()
	root := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "wirefixture-export-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "candidate.json")
	if err := wirefixture.WriteCandidate(root, path, exchanges, policy); err == nil {
		t.Fatal("non-directory worktree root accepted")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate created for invalid root: %v", err)
	}
}
