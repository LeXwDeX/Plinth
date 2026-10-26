package wirefixture_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func safetyExchange() wirefixture.Exchange {
	metadata := testMetadata()
	metadata.Endpoint = "GET /v1/tasks"
	metadata.Protocol = "json"
	return wirefixture.Exchange{
		Sequence: 1,
		Metadata: metadata,
		Request: wirefixture.Request{
			Method: http.MethodGet, Host: "example.test", Path: "/v1/tasks",
			Body: wirefixture.Payload{Encoding: wirefixture.PayloadNone}, Complete: true,
		},
		Response: wirefixture.Response{
			Status: http.StatusOK, ContentType: "application/json",
			Body: wirefixture.Payload{Encoding: wirefixture.PayloadJSON, JSON: json.RawMessage(`{}`)}, Complete: true,
		},
		Complete: true,
	}
}

func safetyCandidateDir(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir, err := os.MkdirTemp("", "wirefixture-safety-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func TestValidationRejectsInactivePayloadRepresentation(t *testing.T) {
	policy := testPolicy()
	cases := []struct {
		name     string
		exchange wirefixture.Exchange
	}{
		{
			name: "JSON response with inactive SSE",
			exchange: func() wirefixture.Exchange {
				exchange := safetyExchange()
				exchange.Response.Body.SSE = "data: [DONE]\n\n"
				return exchange
			}(),
		},
		{
			name: "SSE response with inactive JSON",
			exchange: func() wirefixture.Exchange {
				exchange := safetyExchange()
				exchange.Metadata.Protocol = "sse"
				exchange.Response.ContentType = "text/event-stream"
				exchange.Response.Body = wirefixture.Payload{
					Encoding: wirefixture.PayloadSSE, Transform: wirefixture.TransformNormalizedSSE,
					JSON: json.RawMessage(`{}`), SSE: "data: {\"result\":\"synthetic result\"}\n\ndata: [DONE]\n\n",
				}
				return exchange
			}(),
		},
		{
			name: "JSON request with inactive SSE",
			exchange: func() wirefixture.Exchange {
				exchange := safetyExchange()
				exchange.Metadata.Endpoint = "POST /v1/tasks"
				exchange.Request.Method = http.MethodPost
				exchange.Request.ContentType = "application/json"
				exchange.Request.Body = wirefixture.Payload{
					Encoding: wirefixture.PayloadJSON, JSON: json.RawMessage(`{}`), SSE: "data: [DONE]\n\n",
				}
				return exchange
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := wirefixture.Validate([]wirefixture.Exchange{tc.exchange}, policy); err == nil {
				t.Fatal("poisoned exchange validated")
			}
			if _, err := wirefixture.NewReplay([]wirefixture.Exchange{tc.exchange}, policy); err == nil {
				t.Fatal("poisoned exchange was replayed")
			}
			root, dir := safetyCandidateDir(t)
			path := filepath.Join(dir, "poisoned.json")
			if err := wirefixture.WriteCandidate(root, path, []wirefixture.Exchange{tc.exchange}, policy); err == nil {
				t.Fatal("poisoned exchange was exported")
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("poisoned candidate exists: %v", err)
			}

			data, err := json.Marshal([]wirefixture.Exchange{tc.exchange})
			if err != nil {
				t.Fatal(err)
			}
			var loaded []wirefixture.Exchange
			if err := json.Unmarshal(data, &loaded); err != nil {
				t.Fatal(err)
			}
			if err := wirefixture.Validate(loaded, policy); err == nil {
				t.Fatal("loaded poisoned exchange validated")
			}
		})
	}
}

func TestValidateDoesNotExposeInvalidText(t *testing.T) {
	policy := testPolicy()
	secret := "synthetic-untrusted-invalid-7f3a"
	exchange := safetyExchange()
	exchange.Complete = false
	exchange.Invalid = secret

	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("validate error = %v", err)
	}
	if _, err := wirefixture.NewReplay([]wirefixture.Exchange{exchange}, policy); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("replay error = %v", err)
	}
	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "invalid.json")
	if err := wirefixture.WriteCandidate(root, path, []wirefixture.Exchange{exchange}, policy); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("candidate error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid candidate exists: %v", err)
	}
}

func TestContentTypeValidationAndRetention(t *testing.T) {
	policy := testPolicy()
	cases := []struct {
		name        string
		contentType string
		invalid     bool
		wantBase    string
		unsafe      string
	}{
		{
			name: "private address subtype", contentType: "application/203.0.113.7+json",
			invalid: true, unsafe: "203.0.113.7",
		},
		{
			name: "credential-shaped subtype", contentType: "application/sk-synthetic-not-a-key+json",
			invalid: true, unsafe: "sk-synthetic-not-a-key",
		},
		{
			name: "malformed parameters", contentType: "application/json; charset",
			invalid: true,
		},
		{
			name: "whitespace content type", contentType: " ",
			invalid: true,
		},
		{
			name: "vendor JSON parameters", contentType: "application/vnd.synthetic+json; charset=utf-8",
			wantBase: "application/vnd.synthetic+json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
			request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
			_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, tc.contentType, `{}`))
			if string(body) != "{}" {
				t.Fatalf("business response changed: %q", body)
			}
			exchange := recorder.Exchanges()[0]
			if tc.invalid {
				if exchange.Complete || exchange.Invalid == "" {
					t.Fatalf("unsafe content type accepted: %+v", exchange)
				}
				if exchange.Response.ContentType != "" {
					t.Fatalf("unsafe content type retained: %q", exchange.Response.ContentType)
				}
				if tc.unsafe != "" && strings.Contains(exchange.Invalid, tc.unsafe) {
					t.Fatalf("unsafe content type exposed: %s", exchange.Invalid)
				}
				if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
					t.Fatal("unsafe content type validated")
				}
				return
			}
			if !exchange.Complete || exchange.Response.ContentType != tc.wantBase {
				t.Fatalf("vendor content type: %+v", exchange)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(exchange)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("charset")) || bytes.Contains(data, []byte("utf-8")) {
				t.Fatalf("content type parameters published: %s", data)
			}
		})
	}

	requestPolicy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: requestPolicy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{}`)
	request.Header.Set("Content-Type", "application/vnd.synthetic+json; charset=utf-8")
	_, _ = capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete || exchange.Request.ContentType != "application/vnd.synthetic+json" {
		t.Fatalf("vendor request content type: %+v", exchange)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, requestPolicy); err != nil {
		t.Fatal(err)
	}

	parameterized := safetyExchange()
	parameterized.Response.ContentType = "application/json; charset=utf-8"
	if err := wirefixture.Validate([]wirefixture.Exchange{parameterized}, testPolicy()); err == nil {
		t.Fatal("parameterized response content type validated")
	}
	parameterized = safetyExchange()
	parameterized.Request.ContentType = "application/vnd.synthetic+json; charset=utf-8"
	if err := wirefixture.Validate([]wirefixture.Exchange{parameterized}, testPolicy()); err == nil {
		t.Fatal("parameterized request content type validated")
	}
}

func TestValidateResponseAndMetadataConsistency(t *testing.T) {
	policy := testPolicy()
	cases := []struct {
		name string
		edit func(*wirefixture.Exchange)
		want error
	}{
		{
			name: "3xx response",
			edit: func(exchange *wirefixture.Exchange) { exchange.Response.Status = http.StatusFound },
			want: wirefixture.ErrUnsupportedBody,
		},
		{
			name: "HEAD payload",
			edit: func(exchange *wirefixture.Exchange) {
				exchange.Request.Method = http.MethodHead
				exchange.Metadata.Endpoint = "HEAD /v1/tasks"
			},
			want: wirefixture.ErrUnsupportedBody,
		},
		{
			name: "204 payload",
			edit: func(exchange *wirefixture.Exchange) { exchange.Response.Status = http.StatusNoContent },
			want: wirefixture.ErrUnsupportedBody,
		},
		{
			name: "1xx payload",
			edit: func(exchange *wirefixture.Exchange) { exchange.Response.Status = http.StatusSwitchingProtocols },
			want: wirefixture.ErrUnsupportedBody,
		},
		{
			name: "endpoint method mismatch",
			edit: func(exchange *wirefixture.Exchange) { exchange.Metadata.Endpoint = "POST /v1/tasks" },
			want: wirefixture.ErrUnsafePublicValue,
		},
		{
			name: "endpoint path mismatch",
			edit: func(exchange *wirefixture.Exchange) { exchange.Metadata.Endpoint = "GET /v1/chat" },
			want: wirefixture.ErrUnsafePublicValue,
		},
		{
			name: "protocol mismatch",
			edit: func(exchange *wirefixture.Exchange) { exchange.Metadata.Protocol = "sse" },
			want: wirefixture.ErrUnsafePublicValue,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exchange := safetyExchange()
			tc.edit(&exchange)
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); !errors.Is(err, tc.want) {
				t.Fatalf("validate error = %v, want %v", err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		method string
		status int
	}{
		{"HEAD empty", http.MethodHead, http.StatusOK},
		{"204 empty", http.MethodGet, http.StatusNoContent},
		{"1xx empty", http.MethodGet, http.StatusSwitchingProtocols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exchange := safetyExchange()
			exchange.Request.Method = tc.method
			exchange.Metadata.Endpoint = tc.method + " /v1/tasks"
			exchange.Response.Status = tc.status
			exchange.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadNone}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRequiresPositiveUniqueSequence(t *testing.T) {
	policy := testPolicy()
	for _, sequence := range []int{0, -1} {
		exchange := safetyExchange()
		exchange.Sequence = sequence
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
			t.Fatalf("sequence %d error = %v", sequence, err)
		}
	}
	duplicate := safetyExchange()
	if err := wirefixture.Validate([]wirefixture.Exchange{safetyExchange(), duplicate}, policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("duplicate sequence error = %v", err)
	}

	second := safetyExchange()
	second.Sequence = 1
	second.Request.Path = "/v1/second"
	second.Metadata.Endpoint = "GET /v1/second"
	first := safetyExchange()
	first.Sequence = 2
	first.Request.Path = "/v1/first"
	first.Metadata.Endpoint = "GET /v1/first"
	replay, err := wirefixture.NewReplay([]wirefixture.Exchange{first, second}, policy)
	if err != nil {
		t.Fatal(err)
	}
	reordered := mustRequest(t, http.MethodGet, "https://example.test/v1/first", "")
	if _, err := replay.RoundTrip(reordered); !errors.Is(err, wirefixture.ErrReorderedRequest) {
		t.Fatalf("reordered request error = %v", err)
	}
	replay, err = wirefixture.NewReplay([]wirefixture.Exchange{first, second}, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/second", "/v1/first"} {
		response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test"+path, ""))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "reordered.json")
	if err := wirefixture.WriteCandidate(root, path, []wirefixture.Exchange{first, second}, policy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exported []wirefixture.Exchange
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 2 || exported[0].Sequence != 1 || exported[1].Sequence != 2 {
		t.Fatalf("exported sequence order = %+v", exported)
	}
}

func TestExchangeSnapshotsAreIndependent(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Api-Version", "v1")
	_, _ = capture(t, recorder, request, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}, "Retry-After": {"5"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)), ContentLength: 2,
		}, nil
	}))

	first := recorder.Exchanges()
	if len(first) != 1 || first[0].Response.Headers["retry-after"] != "5" || first[0].Request.Headers["accept"] != "application/json" {
		t.Fatalf("initial snapshot: %+v", first)
	}
	first[0].Response.Body.JSON[0] = 'X'
	first[0].Response.Headers["retry-after"] = "999"
	first[0].Request.Headers["accept"] = "changed"
	second := recorder.Exchanges()
	if string(second[0].Response.Body.JSON) != `{}` || second[0].Response.Headers["retry-after"] != "5" ||
		second[0].Request.Headers["accept"] != "application/json" {
		t.Fatalf("recorder snapshot was mutated: %+v", second[0])
	}

	replay, err := wirefixture.NewReplay(second, policy)
	if err != nil {
		t.Fatal(err)
	}
	second[0].Response.Body.JSON[0] = 'Y'
	second[0].Response.Headers["retry-after"] = "777"
	replayRequest := mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")
	replayRequest.Header.Set("Accept", "application/json")
	replayRequest.Header.Set("X-Api-Version", "v1")
	response, err := replay.RoundTrip(replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(replayed) != `{}` {
		t.Fatalf("replay snapshot changed: %q %v", replayed, err)
	}
	if response.Header.Get("Retry-After") != "5" {
		t.Fatalf("replay headers changed: %v", response.Header)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "snapshot.json")
	input := recorder.Exchanges()
	if err := wirefixture.WriteCandidate(root, path, input, policy); err != nil {
		t.Fatal(err)
	}
	input[0].Response.Body.JSON[0] = 'Z'
	input[0].Response.Headers["retry-after"] = "123"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exported []wirefixture.Exchange
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if string(exported[0].Response.Body.JSON) != `{}` || exported[0].Response.Headers["retry-after"] != "5" {
		t.Fatalf("candidate snapshot changed: %+v", exported[0])
	}
}

func TestRecorderAssertComplete(t *testing.T) {
	policy := testPolicy()
	t.Run("complete", func(t *testing.T) {
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
		_, _ = capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
		if err := recorder.AssertComplete(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unclosed response", func(t *testing.T) {
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
		response, err := recorder.Wrap("hyper3d", jsonResponse(http.StatusOK, "application/json", `{}`)).RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.AssertComplete(); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
			t.Fatalf("unclosed response error = %v", err)
		}
		_ = response.Body.Close()
	})

	t.Run("transport failure", func(t *testing.T) {
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
		_, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("synthetic transport failure")
		})).RoundTrip(request)
		if err == nil {
			t.Fatal("transport failure was ignored")
		}
		if err := recorder.AssertComplete(); !errors.Is(err, wirefixture.ErrTransportFailure) {
			t.Fatalf("transport failure assertion = %v", err)
		}
		if len(recorder.Exchanges()) != 0 || len(recorder.Failures()) != 1 {
			t.Fatalf("failure capture: exchanges=%d failures=%d", len(recorder.Exchanges()), len(recorder.Failures()))
		}
	})

	t.Run("invalid exchange", func(t *testing.T) {
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
		_, _ = capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{"message":"private value"}`))
		err := recorder.AssertComplete()
		if err == nil || errors.Is(err, wirefixture.ErrIncompleteCapture) {
			t.Fatalf("invalid exchange assertion = %v", err)
		}
	})
}

func TestWriteCandidateRejectsInsideWorktreeByIdentity(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "private")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "case-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, caseErr := os.Stat(filepath.Join(root, "CASE-PROBE"))
	caseInsensitive := caseErr == nil

	worktreeRoot := root
	if caseInsensitive {
		worktreeRoot = strings.ToUpper(root)
		t.Log("case-insensitive filesystem: checking case-variant root identity")
	} else {
		alias := filepath.Join(t.TempDir(), "root-alias")
		if err := os.Symlink(root, alias); err != nil {
			t.Fatal(err)
		}
		worktreeRoot = alias
		t.Log("case-sensitive filesystem: checking symlink root identity")
	}

	path := filepath.Join(inner, "inside.json")
	err := wirefixture.WriteCandidate(worktreeRoot, path, []wirefixture.Exchange{safetyExchange()}, testPolicy())
	if !errors.Is(err, wirefixture.ErrInsideWorktree) {
		t.Fatalf("inside worktree error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate file exists: %v", err)
	}
}
