package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// Opt-in capture of upstream calls (#233): the runtime settings, the wiring
// that wraps every connection's transport, and the proof against the fake
// stack that each Provider connection leaves a sanitized record.

func captureEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func captureConfigFor(t *testing.T, values map[string]string) (Config, error) {
	t.Helper()
	return loadConfigFromSchema(defaultSchema(t), captureEnvironment(values))
}

func TestUpstreamCaptureIsOffByDefaultAndWrapsNothing(t *testing.T) {
	config, err := captureConfigFor(t, nil)
	if err != nil || config.UpstreamCapture.Enabled() {
		t.Fatalf("default config: enabled=%v err=%v", config.UpstreamCapture.Enabled(), err)
	}
	recorder, err := newUpstreamCapture(config, defaultSchema(t), discardLogger())
	if err != nil || recorder != nil {
		t.Fatalf("no directory must mean no recorder: %v %v", recorder, err)
	}
	// Nothing is wrapped: the decorator is the very function it was.
	decorate := func(_ string, base http.RoundTripper) http.RoundTripper { return base }
	if got := withUpstreamCapture(nil, decorate); reflect.ValueOf(got).Pointer() != reflect.ValueOf(decorate).Pointer() {
		t.Fatal("an off recorder must leave the connection decorator untouched")
	}
	if withUpstreamCapture(nil, nil) != nil {
		t.Fatal("an off recorder with no decorator must stay nil: the adapters keep their own default clients")
	}
	if client := wireHTTPClient(withUpstreamCapture(nil, nil), "tripo", 0); client != nil {
		t.Fatal("capture off must not build a wrapped client")
	}
	// An unset directory ignores the limit variables entirely.
	config, err = captureConfigFor(t, map[string]string{"UPSTREAM_CAPTURE_MAX_RECORDS": "garbage", "UPSTREAM_CAPTURE_MAX_MEGABYTES": "-1"})
	if err != nil || config.UpstreamCapture.Enabled() {
		t.Fatalf("limits without a directory are ignored: enabled=%v err=%v", config.UpstreamCapture.Enabled(), err)
	}
}

func TestUpstreamCaptureSettingsAreReadFromTheEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	config, err := captureConfigFor(t, map[string]string{"UPSTREAM_CAPTURE_DIR": dir})
	if err != nil || !config.UpstreamCapture.Enabled() {
		t.Fatalf("enabled config: %v", err)
	}
	// The Schema declares the defaults: 2000 records, 64 MiB.
	if got := config.UpstreamCapture; got.Directory != dir || got.MaxRecords != 2000 || got.MaxBytes != 64<<20 {
		t.Fatalf("defaults = %+v", got)
	}
	config, err = captureConfigFor(t, map[string]string{"UPSTREAM_CAPTURE_DIR": dir,
		"UPSTREAM_CAPTURE_MAX_RECORDS": "50", "UPSTREAM_CAPTURE_MAX_MEGABYTES": "3"})
	if err != nil || config.UpstreamCapture.MaxRecords != 50 || config.UpstreamCapture.MaxBytes != 3<<20 {
		t.Fatalf("explicit limits = %+v, %v", config.UpstreamCapture, err)
	}
	for name, values := range map[string]map[string]string{
		"relative directory": {"UPSTREAM_CAPTURE_DIR": "capture"},
		"zero records":       {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_RECORDS": "0"},
		"text records":       {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_RECORDS": "many"},
		"too many records":   {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_RECORDS": "100001"},
		"negative size":      {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_MEGABYTES": "-5"},
		"zero size":          {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_MEGABYTES": "0"},
		"too large":          {"UPSTREAM_CAPTURE_DIR": dir, "UPSTREAM_CAPTURE_MAX_MEGABYTES": "4097"},
	} {
		if _, err := captureConfigFor(t, values); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Records must never become reachable through the web root or be swept up by
// asset maintenance: the directory may not be, hold or sit inside an
// application tree.
func TestUpstreamCaptureRefusesDirectoriesThatOverlapApplicationTrees(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Environment: "production", StorageRoot: filepath.Join(root, "storage"), WebAssetsRoot: filepath.Join(root, "web"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), Logging: LoggingConfig{Directory: filepath.Join(root, "logs")},
	}
	for _, directory := range []string{
		config.StorageRoot, filepath.Join(config.StorageRoot, "capture"), config.WebAssetsRoot, filepath.Join(config.WebAssetsRoot, "x"),
		root, config.LegacyGeneratedImagesRoot, config.Logging.Directory,
	} {
		config.UpstreamCapture = UpstreamCaptureConfig{Directory: directory, MaxRecords: 10, MaxBytes: 1 << 20}
		if _, err := newUpstreamCapture(config, defaultSchema(t), discardLogger()); err == nil || !strings.Contains(err.Error(), "重叠") {
			t.Errorf("%s: want an overlap refusal, got %v", directory, err)
		}
	}
	config.UpstreamCapture = UpstreamCaptureConfig{Directory: filepath.Join(root, "capture"), MaxRecords: 10, MaxBytes: 1 << 20}
	recorder, err := newUpstreamCapture(config, defaultSchema(t), discardLogger())
	if err != nil || recorder == nil {
		t.Fatalf("a separate directory must be accepted: %v", err)
	}
	if info, err := os.Stat(config.UpstreamCapture.Directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory must be created 0700: %v", err)
	}
	// A directory that cannot be used stops the start-up: a stack must never
	// look like it records while it does not.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config.UpstreamCapture.Directory = blocker
	if _, err := newUpstreamCapture(config, defaultSchema(t), discardLogger()); err == nil || !strings.Contains(err.Error(), "无法开启") {
		t.Fatalf("an unusable directory must refuse to start: %v", err)
	}
}

// An external Catalog may call its revision anything: the record label is made
// an identifier instead of stopping the start-up.
func TestUpstreamCaptureLabelsTheCatalogRevision(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-13.299.2":       "2026-11-13.299.2",
		"  spring release 4  ":   "spring-release-4",
		"v1 (客户定制) build_7":      "v1-build_7",
		"---":                    "unversioned",
		"":                       "unversioned",
		"unknown":                "unversioned",
		strings.Repeat("a", 300): strings.Repeat("a", 128),
	} {
		if got := identifierLabel(in, "unversioned"); got != want {
			t.Errorf("identifierLabel(%q) = %q, want %q", in, got, want)
		}
	}
	schema := defaultSchema(t)
	schema.Revision = "customer edition 2 (Q4)"
	config := Config{Environment: "production", UpstreamCapture: UpstreamCaptureConfig{
		Directory: filepath.Join(t.TempDir(), "capture"), MaxRecords: 10, MaxBytes: 1 << 20}}
	if recorder, err := newUpstreamCapture(config, schema, discardLogger()); err != nil || recorder == nil {
		t.Fatalf("a free-text catalog revision must not stop the start-up: %v", err)
	}
}

func TestUpstreamCaptureStateReachesTheStartupLog(t *testing.T) {
	var off strings.Builder
	logUpstreamCapture(testLogger(&off), nil)
	if strings.Contains(off.String(), "level=INFO") {
		t.Fatalf("an off recorder is not an INFO event: %q", off.String())
	}
	config := Config{Environment: "test", UpstreamCapture: UpstreamCaptureConfig{
		Directory: filepath.Join(t.TempDir(), "capture"), MaxRecords: 12, MaxBytes: 5 << 20}}
	recorder, err := newUpstreamCapture(config, defaultSchema(t), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	var on strings.Builder
	logUpstreamCapture(testLogger(&on), recorder)
	for _, want := range []string{"level=INFO", "上游调用脱敏录制已开启", "max_records=12", "max_megabytes=5", "directory="} {
		if !strings.Contains(on.String(), want) {
			t.Errorf("startup log lacks %q: %s", want, on.String())
		}
	}
}

// New refuses an unusable capture directory before it opens anything else.
func TestApplicationRefusesToStartWithAnUnusableCaptureDirectory(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		RuntimeSettings: map[string]string{"llm_model": "test-model"}, Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development",
		DevelopmentUser: "owner-1", UpstreamCapture: UpstreamCaptureConfig{Directory: blocker, MaxRecords: 10, MaxBytes: 1 << 20},
	}, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "上游调用脱敏录制无法开启") {
		t.Fatalf("want a refusal naming the capture, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "art-ai.db")); statErr == nil {
		t.Fatal("the database was opened before the capture setting was checked")
	}
}

// Through the assembled application: the start-up log says whether the capture
// is on, and the administrator (only) sees its state on the diagnostics route.
func TestApplicationReportsUpstreamCaptureToAdministrators(t *testing.T) {
	assemble := func(t *testing.T, capture UpstreamCaptureConfig) (http.Handler, *strings.Builder) {
		t.Helper()
		root := t.TempDir()
		var logged strings.Builder
		application, err := New(Config{
			RuntimeSettings: map[string]string{"llm_model": "test-model"}, Environment: "development", Address: "127.0.0.1:0",
			DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
			LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock",
			AuthMode: "session", SessionSecret: strings.Repeat("s", 32), AdminUsername: "schema-admin", AdminPassword: "correct-horse",
			AdminName: "Admin", AdminEmail: "admin@example.test", UpstreamCapture: capture,
		}, testLogger(&logged))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = application.generation.Close() })
		return application.server.Handler, &logged
	}
	diagnostics := func(handler http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/diagnostics", nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		handler.ServeHTTP(response, request)
		return response
	}
	login := func(handler http.Handler) *http.Cookie {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"schema-admin","password":"correct-horse"}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		cookies := response.Result().Cookies()
		if response.Code != http.StatusOK || len(cookies) == 0 {
			t.Fatalf("login: %d %s", response.Code, response.Body)
		}
		return cookies[0]
	}

	dir := filepath.Join(t.TempDir(), "capture")
	on, onLog := assemble(t, UpstreamCaptureConfig{Directory: dir, MaxRecords: 25, MaxBytes: 7 << 20})
	if !strings.Contains(onLog.String(), "上游调用脱敏录制已开启") {
		t.Fatalf("the start-up log must say the capture is on: %s", onLog)
	}
	if response := diagnostics(on, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", response.Code)
	}
	response := diagnostics(on, login(on))
	var view struct {
		UpstreamCapture wirecapture.RecorderStatus `json:"upstream_capture"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("administrator: %d %s", response.Code, response.Body)
	}
	if got := view.UpstreamCapture; !got.Enabled || got.MaxRecords != 25 || got.MaxBytes != 7<<20 || got.Records != 0 || got.Directory == "" {
		t.Fatalf("view = %+v", got)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory must exist with mode 0700: %v", err)
	}

	off, offLog := assemble(t, UpstreamCaptureConfig{})
	if strings.Contains(offLog.String(), "上游调用脱敏录制已开启") {
		t.Fatalf("an off stack logged the capture as on: %s", offLog)
	}
	response = diagnostics(off, login(off))
	view.UpstreamCapture = wirecapture.RecorderStatus{}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil || view.UpstreamCapture.Enabled || view.UpstreamCapture.Directory != "" {
		t.Fatalf("an off stack must report off: %d %s", response.Code, response.Body)
	}
	// Not in /api/version, /api/health or the public Schema.
	for _, path := range []string{"/api/version", "/api/health", "/api/v1/schema"} {
		probe := httptest.NewRecorder()
		on.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.Contains(probe.Body.String(), "upstream_capture") || strings.Contains(probe.Body.String(), dir) {
			t.Errorf("%s exposes the capture state", path)
		}
	}
}

type captureStack struct {
	server   *httptest.Server
	fakeURL  *url.URL
	recorder *wirecapture.UpstreamRecorder
	dir      string
	product  *catalog.Catalog
	settings map[string]string
}

// newCaptureStack serves every fake version on one loopback origin and points
// each Provider and chat variable at it, as the fake upstream shape does.
func newCaptureStack(t *testing.T) *captureStack {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	product, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "capture")
	recorder, err := newUpstreamCapture(Config{Environment: "test", UpstreamCapture: UpstreamCaptureConfig{
		Directory: dir, MaxRecords: 2000, MaxBytes: 64 << 20}}, product.Application(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	key := fakeUpstreamKeyPrefix + "test-key"
	return &captureStack{server: server, fakeURL: fakeURL, recorder: recorder, dir: dir, product: product, settings: map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3", "tripo_api_key": key,
		"meshy_base_url": server.URL + "/meshy", "meshy_api_key": key,
		"hyper3d_base_url": server.URL + "/hyper3d/api/v2", "hyper3d_api_key": key,
		"openai_base_url": server.URL + "/openai/images/v1", "openai_api_key": key,
		"gemini_base_url": server.URL + "/gemini/images/v1", "gemini_api_key": key,
		"jimeng_base_url": server.URL + "/jimeng", "jimeng_api_key": key,
		"matting_base_url": server.URL + "/matting", "matting_api_key": key,
		"llm_base_url": server.URL + "/openai/v1", "llm_api_key": key, "llm_model": "gpt-astra",
	}}
}

func (s *captureStack) transport() func(string, http.RoundTripper) http.RoundTripper {
	return withUpstreamCapture(s.recorder, fakeShapeHTTPTransport(s.fakeURL))
}

// recordedConnections lists the connection of every sanitized exchange and of
// every skip marker in the directory.
func (s *captureStack) recordedConnections(t *testing.T) map[string]int {
	t.Helper()
	names, err := wirecapture.ListUpstreamRecords(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, name := range names {
		record, err := wirecapture.ReadUpstreamRecord(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if record.Exchange != nil {
			counts[record.Exchange.Metadata.Connection]++
		}
	}
	return counts
}

// skipMarkers lists the skip markers of the directory as "connection method
// status reason".
func (s *captureStack) skipMarkers(t *testing.T) []string {
	t.Helper()
	names, err := wirecapture.ListUpstreamRecords(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	var markers []string
	for _, name := range names {
		record, err := wirecapture.ReadUpstreamRecord(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if record.Skipped != "" {
			markers = append(markers, fmt.Sprintf("%s %s %d %s", record.Connection, record.Method, record.Status, record.Skipped))
		}
	}
	return markers
}

// Every Provider connection of the Catalog, and the chat model connection, run
// against the fake stack with capture on, each leaves a sanitized record, and
// the independent privacy scan finds nothing in any of them.
func TestUpstreamCaptureRecordsEveryProviderConnectionAgainstTheFakeStack(t *testing.T) {
	stack := newCaptureStack(t)
	harness := newAcmeHarnessWithTransport(t, stack.product, stack.settings, stack.transport())
	for i, run := range []struct {
		capability generation.Capability
		offering   generation.OfferingID
		input      string
	}{
		{generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d", `{"prompt":"a plain cube","authorize_extra_credits":true}`},
		{generation.TextTo3D, "meshy/meshy-7.1/text-to-3d", `{"prompt":"a plain cube","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`},
		{generation.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d", `{"prompt":"a plain cube","authorize_extra_credits":true}`},
		{generation.TextToImage, "openai/gpt-image-2/text-to-image", `{"prompt":"a fox"}`},
		{generation.TextToImage, "gemini/gemini-3-pro-image/text-to-image", `{"prompt":"a fox"}`},
		{generation.TextToImage, "jimeng/seedream-4-5/text-to-image", `{"prompt":"a fox"}`},
	} {
		accepted := harness.submitApproved(t, run.capability, run.offering, json.RawMessage(run.input), "capture-"+string(rune('a'+i)))
		if task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID); task.State != generation.TaskSucceeded {
			t.Fatalf("%s: state %s, failure %#v", run.offering, task.State, task.Failure)
		}
	}
	runMattingAgainstTheFakeStack(t, stack)
	runChatAgainstTheFakeStack(t, stack)

	recorded := stack.recordedConnections(t)
	// Every live Provider connection the Catalog declares is covered; a new
	// protocol that forgets to take the wrapped client fails here.
	var expected []string
	for _, connection := range stack.product.Application().Runtime.ProviderConnections {
		switch connection.Protocol {
		case "local-model", "local-model-parts", "local-recovery", "openai-compatible", "http-json":
			continue
		}
		expected = append(expected, connection.ID)
	}
	expected = append(expected, "LLM")
	slices.Sort(expected)
	for _, connection := range expected {
		if recorded[connection] == 0 {
			t.Errorf("connection %q left no sanitized record (recorded: %v)", connection, recorded)
		}
	}
	if len(expected) < 8 {
		t.Fatalf("only %d connections expected: %v", len(expected), expected)
	}
	status := stack.recorder.Status()
	// Nothing is skipped (#536): the chat model gpt-astra is served on the
	// Responses API (#401), whose stream carries event: lines and no [DONE];
	// the sanitizer records it as a named stream, complete at its terminal
	// event, and the request itself is served (runChatAgainstTheFakeStack
	// checks the reply).
	if status.Skipped != 0 || status.WriteErrors != 0 || status.Written < int64(len(expected)) {
		t.Fatalf("status = %+v", status)
	}
	if skipped := stack.skipMarkers(t); len(skipped) != 0 {
		t.Fatalf("skip markers = %q", skipped)
	}
	findings, covered, err := privacyscan.ScanRecordDir(stack.dir)
	if err != nil || len(findings) != 0 || covered != int(status.Written) {
		t.Fatalf("privacy scan: covered=%d of %d, findings=%v, err=%v", covered, status.Written, findings, err)
	}
	// The files themselves: private, and nothing the stack sent as a secret.
	if info, err := os.Stat(stack.dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode: %v", err)
	}
	names, _ := wirecapture.ListUpstreamRecords(stack.dir)
	for _, name := range names {
		path := filepath.Join(stack.dir, name)
		if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s must be 0600: %v", name, err)
		}
		raw, _ := os.ReadFile(path)
		for _, secret := range []string{fakeUpstreamKeyPrefix + "test-key", "Authorization", "Bearer", stack.fakeURL.Host, "a plain cube", "a fox"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("%s holds %q", name, secret)
			}
		}
	}
	// Every record is the format fake-upstream-align draft reads.
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(stack.dir)
	if err != nil || skipped != 0 || len(exchanges) != int(status.Written) {
		t.Fatalf("load: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	// The chat stream (the adapter stops at [DONE]) is among them, and the
	// Responses answers of gpt-astra left one JSON record and one named stream
	// record (event: lines, no [DONE]).
	streams, responseJSON, responseStreams := 0, 0, 0
	for _, exchange := range exchanges {
		if exchange.Metadata.Connection != "LLM" {
			continue
		}
		isResponses := strings.HasSuffix(exchange.Request.Path, "/responses")
		switch {
		case isResponses && exchange.Response.Body.Encoding == wirecapture.PayloadJSON:
			responseJSON++
		case isResponses && exchange.Response.Body.Encoding == wirecapture.PayloadSSE:
			responseStreams++
			sse := exchange.Response.Body.SSE
			if !strings.HasPrefix(sse, "event: ") || strings.Contains(sse, "[DONE]") {
				t.Fatalf("a Responses stream is recorded as a named stream without [DONE]: %.200s", sse)
			}
		case exchange.Response.Body.Encoding == wirecapture.PayloadSSE:
			streams++
		case isResponses:
			t.Fatalf("a Responses record is %q", exchange.Response.Body.Encoding)
		}
	}
	if streams != 1 || responseJSON != 1 || responseStreams != 1 {
		t.Fatalf("records: chat streams %d (want 1), Responses JSON %d (want 1), Responses streams %d (want 1)", streams, responseJSON, responseStreams)
	}
}

func runMattingAgainstTheFakeStack(t *testing.T, stack *captureStack) {
	t.Helper()
	root := t.TempDir()
	repository, err := sqlitetest.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	inputs, err := inputassets.New(repository, blobs)
	if err != nil {
		t.Fatal(err)
	}
	providerRegistry := providers.NewRegistry()
	if err := registerProvidersWithTransport(Config{ProviderMode: "live", RuntimeSettings: stack.settings},
		stack.product, providerRegistry, inputs, blobs, discardLogger(), stack.transport()); err != nil {
		t.Fatal(err)
	}
	offering, ok := stack.product.Get("matting/default/background-removal")
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	port, ok := providerRegistry.Provider(offering)
	if !ok {
		t.Fatal("the matting adapter is not registered")
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewGray(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	asset, err := inputs.Upload(context.Background(), "owner", &picture, "image/png", 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"source_assets": []generation.AssetID{asset.ID}})
	started, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "task-capture", AttemptID: "attempt-capture", SubmissionKey: "submission-capture",
		Offering: offering, Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": {asset}},
	})
	if err != nil || started.Job == nil {
		t.Fatalf("matting start: %+v %v", started, err)
	}
	for range 8 {
		observed, err := port.Observe(context.Background(), generation.ObserveRequest{AttemptID: "attempt-capture", Offering: offering, Job: *started.Job})
		if err != nil {
			t.Fatal(err)
		}
		if observed.Phase == generation.UpstreamCompleted || observed.Phase == generation.UpstreamFailed {
			return
		}
	}
}

func runChatAgainstTheFakeStack(t *testing.T, stack *captureStack) {
	t.Helper()
	config := Config{AgentMode: "live", RuntimeSettings: stack.settings}
	adapter, err := buildChatAgentWithTransport(config, stack.product.Application(), stack.transport(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := agents.Request{
		Model:    "deepseek",
		Messages: []agents.Message{{Role: "user", Content: "tell me about the secret castle of alice@example.org"}},
	}
	reply, err := adapter.Complete(context.Background(), request)
	if err != nil || reply.Content == "" {
		t.Fatalf("chat completion: %+v %v", reply, err)
	}
	// The conversation itself is a stream, and the adapter stops reading at
	// [DONE] without waiting for EOF: the stream must still be recorded.
	var streamed strings.Builder
	reply, err = adapter.Stream(context.Background(), request, func(delta agents.Delta) error {
		streamed.WriteString(delta.Content)
		return nil
	})
	if err != nil || reply.Content == "" {
		t.Fatalf("chat stream: %+v %v", reply, err)
	}
	// gpt-astra is served on the Responses API (#401). Its non-stream JSON and
	// its stream (event: lines, no [DONE], recorded as a named stream, #536)
	// are recorded like any other exchange and go through the privacy scan with
	// the rest; the recorder never changes what the application does.
	request.Model = "gpt-astra"
	if reply, err = adapter.Complete(context.Background(), request); err != nil || reply.Content == "" {
		t.Fatalf("responses completion: %+v %v", reply, err)
	}
	streamed.Reset()
	reply, err = adapter.Stream(context.Background(), request, func(delta agents.Delta) error {
		streamed.WriteString(delta.Content)
		return nil
	})
	if err != nil || reply.Content == "" || streamed.String() != reply.Content {
		t.Fatalf("responses stream: %+v %q %v", reply, streamed.String(), err)
	}
}

// Secrets in the request, in the headers and in the upstream's own error text
// never reach a file, whichever connection carries them.
func TestUpstreamCaptureNeverPersistsSecretsFromAdapterTraffic(t *testing.T) {
	const secretKey = "sk-live-0123456789abcdefABCDEF"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","type":"invalid_request_error","message":"Incorrect API key `+secretKey+
			` for alice@example.org; see https://vendor-cloud.com/keys?token=abc"}}`)
	}))
	t.Cleanup(server.Close)
	dir := filepath.Join(t.TempDir(), "capture")
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := newUpstreamCapture(Config{Environment: "production", UpstreamCapture: UpstreamCaptureConfig{Directory: dir, MaxRecords: 100, MaxBytes: 8 << 20}},
		product.Application(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := buildChatAgentWithTransport(Config{AgentMode: "live", RuntimeSettings: map[string]string{
		"llm_base_url": server.URL + "/v1", "llm_api_key": secretKey, "llm_model": "m"}},
		product.Application(), withUpstreamCapture(recorder, nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Complete(context.Background(), agents.Request{Model: "m", Messages: []agents.Message{{
		Role: "user", Content: "my password is hunter2, render https://assets.vendor-cdn.com/u.png?X-Amz-Signature=deadbeefcafebabe"}}})
	if err == nil {
		t.Fatal("the scripted upstream refuses the key")
	}
	if recorder.Status().Written != 1 {
		t.Fatalf("the 401 must be recorded: %+v", recorder.Status())
	}
	names, _ := wirecapture.ListUpstreamRecords(dir)
	for _, name := range names {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		for _, secret := range []string{secretKey, "alice", "example.org", "vendor-cloud", "vendor-cdn", "token=abc", "deadbeefcafebabe", "hunter2", "Incorrect API key", "Authorization", "Bearer"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s holds %q", name, secret)
			}
		}
		// The structure that diagnoses the failure is what stays.
		for _, want := range []string{`"status": 401`, `invalid_api_key`, `invalid_request_error`, "chat/completions"} {
			if !bytes.Contains(raw, []byte(want)) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	if findings, covered, err := privacyscan.ScanRecordDir(dir); err != nil || covered != 1 || len(findings) != 0 {
		t.Fatalf("scan: covered=%d findings=%v err=%v", covered, findings, err)
	}
}
