package wirecapture_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// The application's recorder (#233): off by default, private 0700/0600 files,
// retention by count and size, and structure-only content. All traffic here is
// synthetic and served by an in-process RoundTripper.

func runtimeDefaults() wirecapture.Metadata {
	return wirecapture.Metadata{
		Environment: "production", AppRevision: "0123456789abcdef0123456789abcdef01234567", CatalogRevision: "2026-11-13.299.2",
	}
}

func runtimeConfig(dir string) wirecapture.RuntimeConfig {
	return wirecapture.RuntimeConfig{Dir: dir, Defaults: runtimeDefaults(), MaxRecords: 1000, MaxBytes: 64 << 20}
}

func newRuntimeRecorder(t *testing.T, mutate func(*wirecapture.RuntimeConfig)) (string, *wirecapture.UpstreamRecorder) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "capture")
	config := runtimeConfig(dir)
	if mutate != nil {
		mutate(&config)
	}
	recorder, err := wirecapture.NewRuntimeRecorder(config)
	if err != nil {
		t.Fatalf("runtime recorder: %v", err)
	}
	return dir, recorder
}

// Secret material the synthetic upstream exchanges carry. None of it may reach
// a record file.
var runtimeSecrets = []string{
	"sk-live-0123456789abcdefABCDEF", "Bearer", "vendor-cloud.com", "assets.vendor-cdn.com", "127.0.0.1",
	"X-Amz-Signature", "deadbeefcafebabe", "Signature=", "alice@example.org", "secret castle",
	"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "token=abc", "portrait-of-bob", "PNGRAWBYTES", "GLBRAWBYTES",
	"the assistant reply text", "bob-private-project", "hunter2",
}

type scriptedUpstream struct{}

func (scriptedUpstream) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
	}
	respond := func(status int, contentType, body string) *http.Response {
		header := http.Header{"Content-Type": {contentType}, "X-Request-Id": {"req-0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d"},
			"Set-Cookie": {"session=token=abc"}}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)), Request: request}
	}
	switch {
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/text-to-3d"):
		return respond(202, "application/json", `{"result":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"PENDING","created_at":1730000000,"user_id":8841}`), nil
	case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/text-to-3d/"):
		return respond(200, "application/json", `{"id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"SUCCEEDED","progress":100,`+
			`"model_urls":{"glb":"https://assets.vendor-cdn.com/m/out.glb?Expires=1&Signature=deadbeefcafebabe"},`+
			`"task_error":{"message":"for alice@example.org"},"subject":"portrait of bob","owner_id":"bob-private-project"}`), nil
	case strings.HasSuffix(request.URL.Path, "/out.glb"):
		return respond(200, "model/gltf-binary", "GLBRAWBYTES"+strings.Repeat("x", 512)), nil
	case strings.HasSuffix(request.URL.Path, "/download.bin"):
		return respond(200, "binary/octet-stream", "GLBRAWBYTES"+strings.Repeat("y", 512)), nil
	case strings.HasSuffix(request.URL.Path, "/images/edits"):
		return respond(400, "application/json", `{"error":{"code":"invalid_size","message":"size must be one of 1024x1024 for the secret castle","type":"invalid_request_error"}}`), nil
	case strings.HasSuffix(request.URL.Path, "/bad-gateway"):
		return respond(502, "text/html", `<html>alice@example.org upstream down</html>`), nil
	case strings.HasSuffix(request.URL.Path, "/chat/completions"):
		stream := "data: {\"id\":\"chatcmpl-0193a5c27c1e7f00b2d28f1e3a\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-astra\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"the assistant \"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-0193a5c27c1e7f00b2d28f1e3a\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-astra\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"reply text\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"
		return respond(200, "text/event-stream", stream), nil
	}
	return respond(404, "application/json", `{}`), nil
}

func send(t *testing.T, client *http.Client, method, target, contentType string, body []byte) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer sk-live-0123456789abcdefABCDEF")
	request.Header.Set("X-Api-Key", "sk-live-0123456789abcdefABCDEF")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Request-ID", "0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const vendorBase = "https://api.vendor-cloud.com/openapi/v2"

// scriptedRun drives one synthetic Provider flow, with secrets in the URL, the
// headers and the bodies, through the recorder.
func scriptedRun(t *testing.T, client *http.Client, base string) {
	t.Helper()
	created := send(t, client, http.MethodPost, base+"/text-to-3d?signature=deadbeefcafebabe&token=abc", "application/json",
		[]byte(`{"prompt":"a secret castle for alice@example.org","model":"meshy-5","seed":42,"user_id":8841,`+
			`"api_key":"sk-live-0123456789abcdefABCDEF","password":"hunter2",`+
			`"image_url":"https://assets.vendor-cdn.com/u/1.png?X-Amz-Signature=deadbeefcafebabe",`+
			`"subject":"portrait of bob","project":"bob-private-project"}`))
	if !strings.Contains(string(created), "0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d") {
		t.Fatal("business response must pass through unchanged")
	}
	send(t, client, http.MethodGet, base+"/text-to-3d/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "", nil)
	glb := send(t, client, http.MethodGet, "https://assets.vendor-cdn.com/m/out.glb?Expires=1&Signature=deadbeefcafebabe", "", nil)
	if !strings.HasPrefix(string(glb), "GLBRAWBYTES") {
		t.Fatal("binary response must pass through unchanged")
	}
	send(t, client, http.MethodPost, base+"/chat/completions", "application/json",
		[]byte(`{"model":"gpt-astra","stream":true,"messages":[{"role":"user","content":"write about the secret castle"}]}`))
}

func jsonUnmarshalStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func recordFiles(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []os.DirEntry
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), wirecapture.UpstreamRecordSuffix) {
			files = append(files, entry)
		}
	}
	return files
}

func TestRuntimeRecorderWritesStructureOnlyPrivateRecords(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	scriptedRun(t, client, vendorBase)
	if err := recorder.WriteError(); err != nil {
		t.Fatalf("record write: %v", err)
	}
	if recorder.Written() != 4 || recorder.Skipped() != 0 {
		t.Fatalf("written=%d skipped=%d, want 4 and 0", recorder.Written(), recorder.Skipped())
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("record directory must be 0700: %v", err)
	}
	files := recordFiles(t, dir)
	if len(files) != 4 {
		t.Fatalf("want 4 record files, got %d", len(files))
	}
	for _, entry := range files {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: record must be a 0600 regular file", entry.Name())
		}
		raw, _ := os.ReadFile(path)
		for _, secret := range runtimeSecrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s: record contains secret marker %q", entry.Name(), secret[:4]+"...")
			}
		}
		lower := bytes.ToLower(raw)
		for _, header := range []string{"authorization", "x-api-key", "set-cookie", "x-request-id"} {
			if bytes.Contains(lower, []byte(header)) {
				t.Errorf("%s: record keeps the %s header", entry.Name(), header)
			}
		}
	}
	// The independent second scan agrees: the same rules as the committed
	// corpus, over the recorder's own output.
	findings, covered, err := privacyscan.ScanRecordDir(dir)
	if err != nil || covered != 4 || len(findings) != 0 {
		t.Fatalf("privacy scan: covered=%d findings=%v err=%v", covered, findings, err)
	}
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 0 || len(exchanges) != 4 {
		t.Fatalf("load records: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	first := exchanges[0]
	if first.Metadata.Environment != "production" || first.Metadata.Connection != "meshy" || first.Metadata.CapturedAt.IsZero() ||
		first.Metadata.Provenance != wirecapture.ProvenanceCaptured || first.Request.Host != "example.test" {
		t.Fatalf("metadata: %+v host %q", first.Metadata, first.Request.Host)
	}
	// The create response's task ID and the poll path share one token.
	if !strings.Contains(string(first.Response.Body.JSON), `"result":"id-`) ||
		!strings.HasPrefix(exchanges[1].Request.Path, "/openapi/v2/text-to-3d/id-") {
		t.Fatalf("task ID must map to a stable token: %s %s", first.Response.Body.JSON, exchanges[1].Request.Path)
	}
	if media := exchanges[2].Response.Body.Media; media == nil || media.Recipe != "glb-triangle" || !media.Substituted {
		t.Fatalf("binary response must become a synthetic descriptor: %+v", media)
	}
}

// The strict profile keeps structure only: one enumeration-shaped token per
// string, a placeholder for anything else, and zero for numeric identifiers.
func TestRuntimeRecorderStrictProfileKeepsOnlyStructure(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	scriptedRun(t, client, vendorBase)
	exchanges, _, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	request := string(exchanges[0].Request.Body.JSON)
	for _, want := range []string{`"prompt":"[redacted]"`, `"subject":"[redacted]"`, `"project":"[redacted]"`, `"model":"meshy-5"`,
		`"seed":42`, `"user_id":0`, `"api_key":"[redacted]"`, `"password":"[redacted]"`} {
		if !strings.Contains(request, want) {
			t.Errorf("request body lacks %s: %s", want, request)
		}
	}
	response := string(exchanges[0].Response.Body.JSON)
	for _, want := range []string{`"status":"PENDING"`, `"created_at":1730000000`, `"user_id":0`} {
		if !strings.Contains(response, want) {
			t.Errorf("create response lacks %s: %s", want, response)
		}
	}
	poll := string(exchanges[1].Response.Body.JSON)
	for _, want := range []string{`"status":"SUCCEEDED"`, `"progress":100`, `"subject":"[redacted]"`, `"owner_id":"[redacted]"`} {
		if !strings.Contains(poll, want) {
			t.Errorf("poll response lacks %s: %s", want, poll)
		}
	}
	if strings.Contains(poll, "task_error\":{\"message\":\"for") {
		t.Errorf("error message text kept: %s", poll)
	}
	// The tooling recorder keeps short text for human review; the application's
	// recorder does not.
	toolingDir := t.TempDir()
	if err := os.Chmod(toolingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tooling, err := wirecapture.NewUpstreamRecorder(wirecapture.UpstreamRecordConfig{Dir: toolingDir, Worktree: t.TempDir(), Defaults: wirecapture.Metadata{
		Environment: "isolated-wire-capture", AppRevision: "0123456789abcdef0123456789abcdef01234567", CatalogRevision: "r1",
		CapturedAt: exchanges[0].Metadata.CapturedAt,
	}})
	if err != nil {
		t.Fatal(err)
	}
	scriptedRun(t, &http.Client{Transport: tooling.Wrap("meshy", scriptedUpstream{})}, vendorBase)
	loaded, _, err := wirecapture.LoadUpstreamRecords(toolingDir)
	if err != nil || len(loaded) == 0 {
		t.Fatalf("tooling records: %d %v", len(loaded), err)
	}
	if !strings.Contains(string(loaded[0].Request.Body.JSON), `"subject":"portrait of bob"`) {
		t.Fatalf("the tooling profile is expected to keep short text: %s", loaded[0].Request.Body.JSON)
	}
}

// Through net/http's own transport (plain HTTP on loopback): the request body
// is written by the transport's goroutine, a stream is flushed in pieces and a
// gzip answer is already decoded when the recorder reads it.
func TestRuntimeRecorderThroughTheRealTransport(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received.Store(int64(len(data)))
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, piece := range []string{"the assistant ", "reply text"} {
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-0193a5c27c1e7f00b2d28f1e3a","object":"chat.completion.chunk","model":"gpt-astra","choices":[{"index":0,"delta":{"content":"`+piece+`"}}]}`+"\n\n")
				flusher.Flush()
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			compressed := gzipBytes(t, `{"status":"ok","data":{"task_id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d"}}`)
			_, _ = w.Write(compressed)
		}
	}))
	t.Cleanup(server.Close)
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("LLM", http.DefaultTransport.(*http.Transport).Clone())}
	big := `{"model":"gpt-astra","note":"` + strings.Repeat("x", 100<<10) + `"}`
	if got := string(send(t, client, http.MethodPost, server.URL+"/v1/echo", "application/json", []byte(big))); !strings.Contains(got, "task_id") || received.Load() != int64(len(big)) {
		t.Fatalf("business exchange changed: %q (server received %d of %d bytes)", got, received.Load(), len(big))
	}
	if got := send(t, client, http.MethodPost, server.URL+"/v1/chat/completions", "application/json", []byte(`{"model":"gpt-astra","stream":true}`)); !strings.Contains(string(got), "reply text") {
		t.Fatalf("stream changed: %q", got)
	}
	status := recorder.Status()
	// The 100 KiB request is within the 1 MiB cap but its long text is redacted.
	if status.Written != 2 || status.Skipped != 0 {
		t.Fatalf("status = %+v (%v)", status, recorder.WriteError())
	}
	exchanges, _, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || len(exchanges) != 2 {
		t.Fatalf("load: %d %v", len(exchanges), err)
	}
	if body := string(exchanges[0].Request.Body.JSON); !strings.Contains(body, `"note":"[redacted]"`) || strings.Contains(body, "xxxx") {
		t.Fatalf("long text kept: %.200s", body)
	}
	if exchanges[0].Response.ContentType != "application/json" || !strings.Contains(string(exchanges[0].Response.Body.JSON), `"task_id":"id-`) {
		t.Fatalf("the gzip answer is read decoded: %+v", exchanges[0].Response)
	}
	if exchanges[1].Response.Body.Encoding != wirecapture.PayloadSSE || !strings.Contains(exchanges[1].Response.Body.SSE, "[DONE]") {
		t.Fatalf("stream: %+v", exchanges[1].Response.Body)
	}
	if findings, covered, err := privacyscan.ScanRecordDir(dir); err != nil || covered != 2 || len(findings) != 0 {
		t.Fatalf("scan: %d %v %v", covered, findings, err)
	}
}

func gzipBytes(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Callers often stop before EOF: the chat adapter breaks out at [DONE], a JSON
// decoder stops after its value. Such a body arrived whole and is recorded; a
// caller that walks away from a half-read stream leaves only a marker.
func TestRuntimeRecorderRecordsBodiesTheCallerStopsReadingEarly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, piece := range []string{"the assistant ", "reply text"} {
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-0193a5c27c1e7f00b2d28f1e3a","object":"chat.completion.chunk","model":"gpt-astra","choices":[{"index":0,"delta":{"content":"`+piece+`"}}]}`+"\n\n")
				flusher.Flush()
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "/v1/half":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-0193a5c27c1e7f00b2d28f1e3a","object":"chat.completion.chunk","model":"gpt-astra","choices":[{"index":0,"delta":{"content":"x"}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Transfer-Encoding", "chunked")
			_, _ = io.WriteString(w, `{"status":"ok","data":{"task_id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d"}}`)
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("LLM", http.DefaultTransport.(*http.Transport).Clone())}

	post := func(path string) *http.Response {
		request, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"model":"gpt-astra"}`))
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	// An SSE reader that breaks out at [DONE] and closes.
	response := post("/v1/chat/completions")
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "data: [DONE]" {
			break
		}
	}
	_ = response.Body.Close()
	// A JSON decoder that stops after the value (chunked, so no length to count).
	response = post("/v1/echo")
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	// A stream the caller abandons half read.
	response = post("/v1/half")
	buffer := make([]byte, 16)
	_, _ = response.Body.Read(buffer)
	_ = response.Body.Close()

	status := recorder.Status()
	if status.Written != 2 || status.Skipped != 1 {
		t.Fatalf("status = %+v (%v)", status, recorder.WriteError())
	}
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 1 || len(exchanges) != 2 {
		t.Fatalf("load: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	if exchanges[0].Response.Body.Encoding != wirecapture.PayloadSSE || !strings.Contains(exchanges[0].Response.Body.SSE, "[DONE]") {
		t.Fatalf("the stream read to [DONE] must be recorded: %+v", exchanges[0].Response.Body)
	}
	if !strings.Contains(string(exchanges[1].Response.Body.JSON), `"status":"ok"`) {
		t.Fatalf("the JSON body read by a decoder must be recorded: %s", exchanges[1].Response.Body.JSON)
	}
	names, _ := wirecapture.ListUpstreamRecords(dir)
	var markers []wirecapture.UpstreamRecord
	for _, name := range names {
		record, err := wirecapture.ReadUpstreamRecord(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if record.Exchange == nil {
			markers = append(markers, record)
		}
	}
	if len(markers) != 1 || markers[0].Skipped != "incomplete-body" || markers[0].Status != 200 || markers[0].Method != "POST" {
		t.Fatalf("the abandoned stream must leave a marker: %+v", markers)
	}
}

func TestRuntimeRecorderAcceptsPlainHTTPUpstreams(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("matting", scriptedUpstream{})}
	send(t, client, http.MethodPost, "http://127.0.0.1:8001/openapi/v2/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 0 || len(exchanges) != 1 {
		t.Fatalf("a plain-HTTP upstream must be recorded: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	if exchanges[0].Request.Host != "example.test" || strings.Contains(fmt.Sprint(exchanges[0]), "127.0.0.1") {
		t.Fatalf("the upstream address must not be kept: %+v", exchanges[0].Request)
	}
}

// A response the recorder cannot represent leaves a marker with the structure
// that needs no value: the connection, a registered method and the status.
func TestRuntimeRecorderSkipMarkerKeepsOnlyStructure(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("tripo", scriptedUpstream{})}
	send(t, client, http.MethodGet, vendorBase+"/bad-gateway?token=abc", "", nil)
	if recorder.Written() != 0 || recorder.Skipped() != 1 {
		t.Fatalf("written=%d skipped=%d, want 0 and 1", recorder.Written(), recorder.Skipped())
	}
	files := recordFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want 1 marker, got %d", len(files))
	}
	raw, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	var record wirecapture.UpstreamRecord
	if err := jsonUnmarshalStrict(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Skipped != "unsupported-body" || record.Connection != "tripo" || record.Method != "GET" || record.Status != 502 || record.Exchange != nil {
		t.Fatalf("marker = %+v", record)
	}
	for _, secret := range []string{"alice", "example.org", "upstream down", "token", "bad-gateway", "vendor-cloud"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("skip marker keeps %q: %s", secret, raw)
		}
	}
	if findings, _, err := privacyscan.ScanRecordDir(dir); err != nil || len(findings) != 0 {
		t.Fatalf("scan: %v %v", findings, err)
	}
}

// Some CDNs answer every download with binary/octet-stream (Tripo's does); it
// is recorded as the registered application/octet-stream, not skipped.
func TestRuntimeRecorderReadsGenericBinaryDownloads(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: recorder.Wrap("tripo", scriptedUpstream{})}
	send(t, client, http.MethodGet, "https://assets.vendor-cdn.com/m/download.bin?Signature=deadbeefcafebabe", "", nil)
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 0 || len(exchanges) != 1 {
		t.Fatalf("generic binary: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	if media := exchanges[0].Response.Body.Media; media == nil || !media.Substituted || exchanges[0].Response.ContentType != "application/octet-stream" {
		t.Fatalf("response = %+v", exchanges[0].Response)
	}
}

func TestRuntimeRecorderRotatesByCount(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, func(config *wirecapture.RuntimeConfig) { config.MaxRecords = 5 })
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	for range 12 {
		send(t, client, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	}
	files := recordFiles(t, dir)
	if len(files) != 5 {
		t.Fatalf("count limit 5 left %d files", len(files))
	}
	status := recorder.Status()
	if status.Records != 5 || status.Rotated != 7 || status.Written != 12 || status.MaxRecords != 5 {
		t.Fatalf("status = %+v", status)
	}
	// The newest records survive: sequence numbers 8..12.
	exchanges, _, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || len(exchanges) != 5 || exchanges[0].Sequence != 8 || exchanges[4].Sequence != 12 {
		t.Fatalf("kept the wrong records: %d %v", len(exchanges), err)
	}
}

func TestRuntimeRecorderRotatesBySize(t *testing.T) {
	probeDir, probe := newRuntimeRecorder(t, nil)
	send(t, &http.Client{Transport: probe.Wrap("meshy", scriptedUpstream{})}, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	one := probe.Status().Bytes
	if one <= 0 || len(recordFiles(t, probeDir)) != 1 {
		t.Fatal("probe record missing")
	}
	// Room for three records, not four.
	dir, recorder := newRuntimeRecorder(t, func(config *wirecapture.RuntimeConfig) { config.MaxBytes = one*3 + one/2 })
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	for range 10 {
		send(t, client, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	}
	status := recorder.Status()
	if status.Bytes > status.MaxBytes || status.Records < 2 || status.Records > 3 || status.Rotated < 7 {
		t.Fatalf("size limit not enforced: %+v", status)
	}
	var onDisk int64
	for _, entry := range recordFiles(t, dir) {
		info, _ := entry.Info()
		onDisk += info.Size()
	}
	if onDisk != status.Bytes || onDisk > status.MaxBytes {
		t.Fatalf("directory holds %d bytes, status says %d (limit %d)", onDisk, status.Bytes, status.MaxBytes)
	}
}

// A restart counts the files already there, applies the (possibly smaller)
// limits at once, never touches other files and keeps sequence numbers rising.
func TestRuntimeRecorderResumesAnExistingDirectory(t *testing.T) {
	dir, first := newRuntimeRecorder(t, nil)
	client := &http.Client{Transport: first.Wrap("meshy", scriptedUpstream{})}
	for range 6 {
		send(t, client, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	}
	other := filepath.Join(dir, "operator-notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := wirecapture.NewRuntimeRecorder(func() wirecapture.RuntimeConfig {
		config := runtimeConfig(dir)
		config.MaxRecords = 4
		return config
	}())
	if err != nil {
		t.Fatal(err)
	}
	if status := second.Status(); status.Records != 4 || status.Rotated != 2 {
		t.Fatalf("limits not applied at start: %+v", status)
	}
	send(t, &http.Client{Transport: second.Wrap("meshy", scriptedUpstream{})}, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	if kept, err := os.ReadFile(other); err != nil || string(kept) != "keep me" {
		t.Fatalf("an unrelated file was touched: %q %v", kept, err)
	}
	exchanges, _, err := wirecapture.LoadUpstreamRecords(dir)
	if err != nil || len(exchanges) != 4 {
		t.Fatalf("load: %d %v", len(exchanges), err)
	}
	last := exchanges[len(exchanges)-1].Sequence
	for i := 1; i < len(exchanges); i++ {
		if exchanges[i].Sequence <= exchanges[i-1].Sequence {
			t.Fatalf("sequence numbers must keep rising across restarts: %d after %d", exchanges[i].Sequence, exchanges[i-1].Sequence)
		}
	}
	if last != 7 {
		t.Fatalf("the new record should continue after sequence 6, got %d", last)
	}
}

// A record appears under its final name only once complete; what a crash left
// half written is cleaned up at the next start and never read as a record.
func TestRuntimeRecorderRemovesRecordsLeftHalfWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "20261116T100000.000000000Z-000001"+wirecapture.UpstreamRecordSuffix+".tmp")
	unrelated := filepath.Join(dir, "operator.tmp")
	for _, path := range []string{partial, unrelated} {
		if err := os.WriteFile(path, []byte(`{"version":1,"exch`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if exchanges, skipped, err := wirecapture.LoadUpstreamRecords(dir); err != nil || len(exchanges) != 0 || skipped != 0 {
		t.Fatalf("a half-written record must not be read: %d %d %v", len(exchanges), skipped, err)
	}
	recorder, err := wirecapture.NewRuntimeRecorder(runtimeConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("the half-written record survived the start: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("an unrelated file was removed: %v", err)
	}
	send(t, &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") && entry.Name() != "operator.tmp" {
			t.Fatalf("a finished write left %s behind", entry.Name())
		}
	}
	if recorder.Status().Records != 1 {
		t.Fatalf("status = %+v", recorder.Status())
	}
}

func TestRuntimeRecorderPreparesTheDirectory(t *testing.T) {
	root := t.TempDir()
	// Missing: created with every parent, mode 0700.
	nested := filepath.Join(root, "a", "b", "capture")
	if _, err := wirecapture.NewRuntimeRecorder(runtimeConfig(nested)); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	for _, path := range []string{nested, filepath.Dir(nested)} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s must be 0700: %v", path, err)
		}
	}
	// A wider existing directory is tightened.
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wirecapture.NewRuntimeRecorder(runtimeConfig(open)); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	if info, err := os.Stat(open); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("an existing directory must end up 0700: %v", err)
	}
	// A symbolic link, a file and a relative path are refused.
	link := filepath.Join(root, "link")
	if err := os.Symlink(open, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"symlink": link, "file": file, "relative": "capture", "empty": ""} {
		if _, err := wirecapture.NewRuntimeRecorder(runtimeConfig(dir)); !errors.Is(err, wirecapture.ErrUpstreamRecordRefused) {
			t.Errorf("%s directory must be refused, got %v", name, err)
		}
	}
}

func TestRuntimeRecorderRefusesIncompleteConfiguration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	for name, mutate := range map[string]func(*wirecapture.RuntimeConfig){
		"no record limit":     func(c *wirecapture.RuntimeConfig) { c.MaxRecords = 0 },
		"no size limit":       func(c *wirecapture.RuntimeConfig) { c.MaxBytes = 0 },
		"no environment":      func(c *wirecapture.RuntimeConfig) { c.Defaults.Environment = "" },
		"no app revision":     func(c *wirecapture.RuntimeConfig) { c.Defaults.AppRevision = "" },
		"unknown revision":    func(c *wirecapture.RuntimeConfig) { c.Defaults.AppRevision = "unknown" },
		"no catalog revision": func(c *wirecapture.RuntimeConfig) { c.Defaults.CatalogRevision = "" },
		"bad catalog label":   func(c *wirecapture.RuntimeConfig) { c.Defaults.CatalogRevision = "has space" },
	} {
		config := runtimeConfig(dir)
		mutate(&config)
		if _, err := wirecapture.NewRuntimeRecorder(config); !errors.Is(err, wirecapture.ErrUpstreamRecordRefused) {
			t.Errorf("%s: want refusal, got %v", name, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a refused configuration must not create the directory: %v", err)
	}
}

// Recording is observation only: a failing upstream, a vanished directory and
// concurrent callers leave the business traffic and the counters intact.
func TestRuntimeRecorderNeverDisturbsTheBusinessCall(t *testing.T) {
	var logged strings.Builder
	dir, recorder := newRuntimeRecorder(t, func(config *wirecapture.RuntimeConfig) {
		config.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})
	failing := wirecapture.RoundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: refused") })
	if _, err := (&http.Client{Transport: recorder.Wrap("meshy", failing)}).Get(vendorBase + "/text-to-3d"); err == nil || !strings.Contains(err.Error(), "dial tcp: refused") {
		t.Fatalf("transport errors must pass through unchanged: %v", err)
	}
	if recorder.Skipped() != 1 {
		t.Fatalf("a transport error leaves a marker: skipped=%d", recorder.Skipped())
	}
	// The directory vanishes (volume unmounted, disk problem): requests still work.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	created := send(t, client, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	if !strings.Contains(string(created), "PENDING") {
		t.Fatal("business response changed")
	}
	if recorder.WriteError() == nil || recorder.Status().WriteErrors != 1 {
		t.Fatalf("the failed write must be counted: %+v", recorder.Status())
	}
	if !strings.Contains(logged.String(), "业务请求不受影响") || strings.Contains(logged.String(), "PENDING") {
		t.Fatalf("one warning without content expected, got %q", logged.String())
	}
}

func TestRuntimeRecorderIsSafeForConcurrentCallers(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, func(config *wirecapture.RuntimeConfig) { config.MaxRecords = 40 })
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				send(t, client, http.MethodGet, vendorBase+"/text-to-3d/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "", nil)
			}
		}()
	}
	wg.Wait()
	status := recorder.Status()
	if status.Written != 80 || status.Records != 40 || status.Rotated != 40 || status.WriteErrors != 0 {
		t.Fatalf("status = %+v", status)
	}
	if len(recordFiles(t, dir)) != 40 {
		t.Fatalf("directory holds %d records", len(recordFiles(t, dir)))
	}
}

func TestDisabledRecorderCostsNothing(t *testing.T) {
	var recorder *wirecapture.UpstreamRecorder
	base := http.DefaultTransport
	if got := recorder.Wrap("meshy", base); got != base {
		t.Fatal("a nil recorder must return the base transport unchanged")
	}
	if status := recorder.Status(); status.Enabled || status.Records != 0 || status.Directory != "" {
		t.Fatalf("a nil recorder reports off: %+v", status)
	}
}

func TestRuntimeRecorderStatusDescribesTheRecorder(t *testing.T) {
	dir, recorder := newRuntimeRecorder(t, func(config *wirecapture.RuntimeConfig) { config.MaxRecords, config.MaxBytes = 7, 3<<20 })
	status := recorder.Status()
	real, _ := filepath.EvalSymlinks(dir)
	if !status.Enabled || status.Directory != real || status.MaxRecords != 7 || status.MaxBytes != 3<<20 || status.Records != 0 {
		t.Fatalf("status = %+v", status)
	}
}
