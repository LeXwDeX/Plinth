package wirefixture_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// Upstream recorder (#220): opt-in, sanitized-at-write recording of real
// Provider exchanges, plus conversion into candidate drafts. All traffic here
// is synthetic and served by an in-process RoundTripper; nothing touches the
// network.

func recordDefaults() wirefixture.Metadata {
	return wirefixture.Metadata{
		Scenario: "upstream-record-test", AppRevision: "0123456789abcdef0123456789abcdef01234567",
		CatalogRevision: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		Environment:     "isolated-wire-capture", CapturedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
	}
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "upstream-record-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestUpstreamRecorderIsOffByDefault(t *testing.T) {
	recorder, err := wirefixture.UpstreamRecorderFromEnv(envOf(nil), repoRoot(t), recordDefaults())
	if err != nil || recorder != nil {
		t.Fatalf("recording must be off without switches: recorder=%v err=%v", recorder != nil, err)
	}
	base := http.DefaultTransport
	if got := recorder.Wrap("tripo", base); got != base {
		t.Fatal("a nil recorder must return the base transport unchanged")
	}
}

func TestUpstreamRecorderRefusesHalfSetAndProductionLikeConfig(t *testing.T) {
	root := repoRoot(t)
	dir := privateDir(t)
	cases := map[string]map[string]string{
		"dir without ack": {wirefixture.EnvUpstreamRecordDir: dir},
		"ack without dir": {wirefixture.EnvUpstreamRecordAck: "1"},
		"ack not 1":       {wirefixture.EnvUpstreamRecordDir: dir, wirefixture.EnvUpstreamRecordAck: "yes"},
		"production":      {wirefixture.EnvUpstreamRecordDir: dir, wirefixture.EnvUpstreamRecordAck: "1", wirefixture.EnvUpstreamRecordEnvironment: "production"},
		"formal-test":     {wirefixture.EnvUpstreamRecordDir: dir, wirefixture.EnvUpstreamRecordAck: "1", wirefixture.EnvUpstreamRecordEnvironment: "formal-test"},
		"relative dir":    {wirefixture.EnvUpstreamRecordDir: "records", wirefixture.EnvUpstreamRecordAck: "1"},
		"dir in worktree": {wirefixture.EnvUpstreamRecordDir: filepath.Join(root, "internal"), wirefixture.EnvUpstreamRecordAck: "1"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			recorder, err := wirefixture.UpstreamRecorderFromEnv(envOf(env), root, recordDefaults())
			if recorder != nil || !errors.Is(err, wirefixture.ErrUpstreamRecordRefused) {
				t.Fatalf("want refusal, got recorder=%v err=%v", recorder != nil, err)
			}
		})
	}
	open := privateDir(t)
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wirefixture.UpstreamRecorderFromEnv(envOf(map[string]string{
		wirefixture.EnvUpstreamRecordDir: open, wirefixture.EnvUpstreamRecordAck: "1",
	}), root, recordDefaults()); !errors.Is(err, wirefixture.ErrUpstreamRecordRefused) {
		t.Fatalf("a non-0700 directory must be refused, got %v", err)
	}
	missingRevision := recordDefaults()
	missingRevision.AppRevision = ""
	if _, err := wirefixture.UpstreamRecorderFromEnv(envOf(map[string]string{
		wirefixture.EnvUpstreamRecordDir: dir, wirefixture.EnvUpstreamRecordAck: "1",
	}), root, missingRevision); !errors.Is(err, wirefixture.ErrUpstreamRecordRefused) {
		t.Fatalf("missing revision must be refused, got %v", err)
	}
}

// Secret material the synthetic upstream exchanges carry. None of it may
// reach any recorded or exported file.
var recordSecrets = []string{
	"sk-live-0123456789abcdefABCDEF", "Bearer", "vendor-cloud.com", "assets.vendor-cdn.com",
	"X-Amz-Signature", "deadbeefcafebabe", "Signature=", "alice@example.org", "secret castle",
	"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "token=abc", "portrait-of-bob", "PNGRAWBYTES", "GLBRAWBYTES",
	"the assistant reply text",
}

type scriptedUpstream struct{ t *testing.T }

func (s scriptedUpstream) RoundTrip(request *http.Request) (*http.Response, error) {
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
		return respond(202, "application/json", `{"result":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"PENDING","created_at":1730000000}`), nil
	case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/text-to-3d/"):
		return respond(200, "application/json", `{"id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d","status":"SUCCEEDED","progress":100,`+
			`"model_urls":{"glb":"https://assets.vendor-cdn.com/m/out.glb?Expires=1&Signature=deadbeefcafebabe"},`+
			`"task_error":{"message":"for alice@example.org"}}`), nil
	case strings.HasSuffix(request.URL.Path, "/out.glb"):
		return respond(200, "model/gltf-binary", "GLBRAWBYTES"+strings.Repeat("x", 512)), nil
	case strings.HasSuffix(request.URL.Path, "/images/edits"):
		return respond(400, "application/json", `{"error":{"code":"invalid_size","message":"size must be one of 1024x1024 for the secret castle","type":"invalid_request_error"}}`), nil
	case strings.HasSuffix(request.URL.Path, "/chat/completions"):
		stream := "data: {\"id\":\"chatcmpl-0193a5c27c1e7f00b2d28f1e3a\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-astra\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"the assistant \"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-0193a5c27c1e7f00b2d28f1e3a\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-astra\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"reply text\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"
		return respond(200, "text/event-stream", stream), nil
	}
	s.t.Errorf("unexpected scripted request %s %s", request.Method, request.URL.Path)
	return respond(404, "application/json", `{}`), nil
}

func send(t *testing.T, client *http.Client, method, url, contentType string, body []byte) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
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

func multipartUpload(t *testing.T) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("prompt", "portrait-of-bob in a secret castle"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("size", "1024x1024"); err != nil {
		t.Fatal(err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="portrait-of-bob.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("PNGRAWBYTES" + strings.Repeat("p", 256)))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

// recordScriptedRun drives one synthetic Provider flow through the recorder.
func recordScriptedRun(t *testing.T) (dir string, recorder *wirefixture.UpstreamRecorder) {
	t.Helper()
	dir = privateDir(t)
	recorder, err := wirefixture.UpstreamRecorderFromEnv(envOf(map[string]string{
		wirefixture.EnvUpstreamRecordDir: dir, wirefixture.EnvUpstreamRecordAck: "1",
		wirefixture.EnvUpstreamRecordEnvironment: "isolated-wire-capture",
	}), repoRoot(t), recordDefaults())
	if err != nil || recorder == nil {
		t.Fatalf("enable recorder: %v", err)
	}
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{t})}
	base := "https://api.vendor-cloud.com/openapi/v2"
	created := send(t, client, http.MethodPost, base+"/text-to-3d?signature=deadbeefcafebabe&token=abc", "application/json",
		[]byte(`{"prompt":"a secret castle for alice@example.org","model":"meshy-5","seed":42,`+
			`"api_key":"sk-live-0123456789abcdefABCDEF","image_url":"https://assets.vendor-cdn.com/u/1.png?X-Amz-Signature=deadbeefcafebabe"}`))
	if !strings.Contains(string(created), "0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d") {
		t.Fatal("business response must pass through unchanged")
	}
	send(t, client, http.MethodGet, base+"/text-to-3d/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "", nil)
	glb := send(t, client, http.MethodGet, "https://assets.vendor-cdn.com/m/out.glb?Expires=1&Signature=deadbeefcafebabe", "", nil)
	if !strings.HasPrefix(string(glb), "GLBRAWBYTES") {
		t.Fatal("binary response must pass through unchanged")
	}
	upload, contentType := multipartUpload(t)
	send(t, client, http.MethodPost, base+"/images/edits", contentType, upload)
	send(t, client, http.MethodPost, base+"/chat/completions", "application/json",
		[]byte(`{"model":"gpt-astra","stream":true,"messages":[{"role":"user","content":"write about the secret castle"}]}`))
	if err := recorder.WriteError(); err != nil {
		t.Fatalf("record write: %v", err)
	}
	return dir, recorder
}

func TestUpstreamRecorderWritesSanitizedPrivateRecords(t *testing.T) {
	dir, recorder := recordScriptedRun(t)
	if recorder.Written() != 5 || recorder.Skipped() != 0 {
		t.Fatalf("written=%d skipped=%d, want 5 and 0", recorder.Written(), recorder.Skipped())
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("record directory mode changed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 5 {
		t.Fatalf("want 5 record files, got %d (%v)", len(entries), err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: record must be a 0600 regular file", entry.Name())
		}
		raw, _ := os.ReadFile(path)
		for _, secret := range recordSecrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s: record contains a secret marker (%d bytes in)", entry.Name(), len(secret))
			}
		}
		if bytes.Contains(bytes.ToLower(raw), []byte("authorization")) || bytes.Contains(bytes.ToLower(raw), []byte("x-api-key")) ||
			bytes.Contains(bytes.ToLower(raw), []byte("set-cookie")) || bytes.Contains(bytes.ToLower(raw), []byte("x-request-id")) {
			t.Errorf("%s: record keeps a header outside the replay set", entry.Name())
		}
	}
	exchanges, skipped, err := wirefixture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 0 || len(exchanges) != 5 {
		t.Fatalf("load records: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	// The create response's task ID and the poll path share one token.
	if !strings.Contains(string(exchanges[0].Response.Body.JSON), `"result":"id-`) ||
		!strings.HasPrefix(exchanges[1].Request.Path, "/openapi/v2/text-to-3d/id-") {
		t.Fatalf("task ID must map to a stable token: %s %s", exchanges[0].Response.Body.JSON, exchanges[1].Request.Path)
	}
	if media := exchanges[2].Response.Body.Media; media == nil || media.Recipe != "glb-triangle" || !media.Substituted {
		t.Fatalf("binary response must become a synthetic descriptor: %+v", media)
	}
	policy, err := wirefixture.DraftReplayPolicy(exchanges)
	if err != nil {
		t.Fatalf("replay policy: %v", err)
	}
	if findings := scanCandidate("records", exchanges, policy); len(findings) != 0 {
		t.Fatalf("privacy scan of records: %v", findings)
	}
	if findings := scanPolicy("records.policy", policy); len(findings) != 0 {
		t.Fatalf("privacy scan of derived policy: %v", findings)
	}
}

func TestUpstreamRecorderSkipsOversizeAndIncompleteExchanges(t *testing.T) {
	dir := privateDir(t)
	defaults := recordDefaults()
	recorder, err := wirefixture.NewUpstreamRecorder(wirefixture.UpstreamRecordConfig{
		Dir: dir, Worktree: repoRoot(t), Defaults: defaults, MaxBodyBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{t})}
	// The poll response is larger than 64 bytes: it passes through, unrecorded.
	body := send(t, client, http.MethodGet, "https://api.vendor-cloud.com/openapi/v2/text-to-3d/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", "", nil)
	if !strings.Contains(string(body), "SUCCEEDED") {
		t.Fatal("oversize response must pass through unchanged")
	}
	// Closing a response without reading it is incomplete.
	request, _ := http.NewRequest(http.MethodGet, "https://api.vendor-cloud.com/openapi/v2/text-to-3d/0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if recorder.Written() != 0 || recorder.Skipped() != 2 {
		t.Fatalf("written=%d skipped=%d, want 0 and 2", recorder.Written(), recorder.Skipped())
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		for _, secret := range recordSecrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s: skip record leaks a value", entry.Name())
			}
		}
	}
	if _, skipped, err := wirefixture.LoadUpstreamRecords(dir); err != nil || skipped != 2 {
		t.Fatalf("skip records must load as skipped: %d %v", skipped, err)
	}
}

func TestUpstreamDraftCandidatePassesLoadersAndPrivacyScan(t *testing.T) {
	dir, _ := recordScriptedRun(t)
	exchanges, _, err := wirefixture.LoadUpstreamRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := privateDir(t)
	root := repoRoot(t)
	draft, err := wirefixture.WriteDraftCandidate(root, out, "meshy-recorded-draft", "meshy-text-to-3d", exchanges)
	if err != nil {
		t.Fatalf("write draft: %v", err)
	}
	if _, err := wirefixture.WriteDraftCandidate(root, out, "meshy-recorded-draft", "", exchanges); !errors.Is(err, wirefixture.ErrExistingCandidate) {
		t.Fatalf("a second draft must not overwrite the first: %v", err)
	}
	if _, err := wirefixture.WriteDraftCandidate(root, filepath.Join(root, "internal"), "x", "", exchanges); err == nil {
		t.Fatal("a draft inside the worktree must be refused")
	}
	for _, path := range []string{draft.Candidate, draft.Policy, draft.Meta} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: draft files must be 0600", filepath.Base(path))
		}
	}
	// Load exactly as TestCommittedWireCorpusHasNoPrivateData does.
	raw, _ := os.ReadFile(draft.Candidate)
	policyRaw, _ := os.ReadFile(draft.Policy)
	metaRaw, _ := os.ReadFile(draft.Meta)
	var policy wirefixture.Policy
	var loaded []wirefixture.Exchange
	if decodeStrict(policyRaw, &policy) != nil || decodeStrict(raw, &loaded) != nil {
		t.Fatal("draft is not in wirefixture format")
	}
	if err := wirefixture.Validate(compactJSONBodies(loaded), policy); err != nil {
		t.Fatalf("Validate rejects the draft: %v", err)
	}
	findings := scanCandidate("draft.candidate.json", loaded, policy)
	findings = append(findings, scanPolicy("draft.policy.json", policy)...)
	findings = append(findings, scanGenericJSON("draft.meta.json", metaRaw)...)
	if len(findings) != 0 {
		t.Fatalf("privacy scan of the draft: %v", findings)
	}
	for _, data := range [][]byte{raw, policyRaw, metaRaw} {
		for _, secret := range recordSecrets {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("draft leaks a secret marker")
			}
		}
	}
	// The draft replays offline: the first recorded request matches.
	replay, err := wirefixture.NewReplay(loaded, policy)
	if err != nil {
		t.Fatalf("draft must load as a replay: %v", err)
	}
	if _, err := replay.RoundTrip(draftRequest(t, loaded[0])); err != nil {
		t.Fatalf("replay of the first draft exchange: %v", err)
	}
}

func draftRequest(t *testing.T, exchange wirefixture.Exchange) *http.Request {
	t.Helper()
	request, err := http.NewRequest(exchange.Request.Method, "https://"+exchange.Request.Host+exchange.Request.Path,
		bytes.NewReader(exchange.Request.Body.JSON))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", exchange.Request.ContentType)
	for name, value := range exchange.Request.Headers {
		request.Header.Set(name, value)
	}
	return request
}

// Through the real net/http transport (loopback TLS, no external network) the
// recorder sees the request body written by the transport goroutine.
func TestUpstreamRecorderThroughRealTransport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"task_id":"0193a5c2-7c1e-7f00-b2d2-8f1e3a4b5c6d"}}`)
	}))
	defer server.Close()
	dir := privateDir(t)
	recorder, err := wirefixture.NewUpstreamRecorder(wirefixture.UpstreamRecordConfig{Dir: dir, Worktree: repoRoot(t), Defaults: recordDefaults()})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: recorder.Wrap("tripo", server.Client().Transport)}
	send(t, client, http.MethodPost, server.URL+"/v2/openapi/task", "application/json", []byte(`{"type":"text_to_model","prompt":"a secret castle"}`))
	exchanges, skipped, err := wirefixture.LoadUpstreamRecords(dir)
	if err != nil || skipped != 0 || len(exchanges) != 1 {
		t.Fatalf("load: %d %d %v", len(exchanges), skipped, err)
	}
	if body := string(exchanges[0].Request.Body.JSON); !strings.Contains(body, `"prompt":"[redacted]"`) || strings.Contains(body, "castle") {
		t.Fatalf("request body not redacted: %s", body)
	}
	if exchanges[0].Request.Host != "example.test" {
		t.Fatalf("host must be aliased, got %q", exchanges[0].Request.Host)
	}
}
