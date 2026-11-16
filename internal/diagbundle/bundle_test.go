package diagbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// Values a real stack would hold and a bundle must never carry.
const (
	secretSessionValue = "session-secret-value-0123456789abcdefghij"
	secretKeyValue     = "msy-realistic-provider-key-ABCDEF123456"
	secretOwnerID      = "7d9c1e5b8a2f4c3d9e0b1a2c3d4e5f60"
	secretOwnerName    = "carol-longname"
	secretEmail        = "carol.private@corp-mail.example"
	secretPrompt       = "a tiny dragon wearing a red hat and golden boots"
	bearerValue        = "zzzzBearerValue0123456789"
)

var (
	defaultEpoch = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	settingsEnv  = map[string]string{
		"SESSION_SECRET": secretSessionValue, "MESHY_API_KEY": secretKeyValue, "LOG_DIR": "/app/logs", "MESHY_API_BASE_URL": "https://api.vendor.example",
		"SSO_APP_SECRET": "retired-secret-value", "LLM_MODEL": "gpt-astra",
	}
)

type testStack struct {
	root, logs, captures, database string
	sources                        Sources
}

// newTestStack builds the directories of a stack: a migrated database, a log
// directory and a capture directory, all empty.
func newTestStack(t *testing.T) *testStack {
	t.Helper()
	root := t.TempDir()
	stack := &testStack{root: root, logs: filepath.Join(root, "logs"), captures: filepath.Join(root, "capture"), database: filepath.Join(root, "data", "art-ai.db")}
	for directory, mode := range map[string]os.FileMode{stack.logs: 0o750, stack.captures: 0o700, filepath.Dir(stack.database): 0o750} {
		if err := os.MkdirAll(directory, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, mode); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := sqlitetest.Open(stack.database)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	settings, retired := SettingsFromCatalog(product.Application())
	stack.sources = Sources{
		Revision: strings.Repeat("ab", 20), CatalogRevision: product.Application().Revision, CatalogSHA256: DocumentSHA256(catalog.DefaultDocument()),
		Environment: "production", LogDirectory: stack.logs, LogFileName: logging.FileName, CaptureDirectory: stack.captures, DatabasePath: stack.database,
		Settings: settings, RetiredEnvironment: retired, Lookup: func(name string) string { return settingsEnv[name] },
	}
	return stack
}

func (s *testStack) options() Options {
	return Options{Since: defaultEpoch.Add(-24 * time.Hour), MaxBytes: DefaultMaxBytes, Now: func() time.Time { return defaultEpoch }}
}

type seedTask struct {
	id, owner, state, failure string
	finished                  time.Time
	input                     string
}

const seedOffering = `{"id":"meshy/meshy-7/text-to-3d","provider_id":"meshy","capability":"text_to_3d",` +
	`"parameters":[{"name":"prompt","type":"text","semantic":"prompt"}]}`

// seed writes tasks into the migrated database through a second connection.
func (s *testStack) seed(t *testing.T, tasks ...seedTask) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+s.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, task := range tasks {
		input := task.input
		if input == "" {
			input = `{"prompt":"` + secretPrompt + `"}`
		}
		var failure any
		if task.failure != "" {
			failure = task.failure
		}
		stamp := task.finished.UTC().Format(time.RFC3339Nano)
		if _, err := db.Exec(`
			INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state, version,
				created_at, updated_at, finished_at, failure_json)
			VALUES (?, ?, 'text_to_3d', ?, ?, 'hash', ?, 1, ?, ?, ?, ?)`,
			task.id, task.owner, seedOffering, input, task.state, stamp, stamp, stamp, failure); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *testStack) seedAttempt(t *testing.T, taskID string, ordinal int, state, failure string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+s.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := defaultEpoch.Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key, upstream_provider_id, upstream_job_id,
			failure_json, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'meshy', ?, ?, ?, ?)`,
		fmt.Sprintf("%s-a%d", taskID, ordinal), taskID, ordinal, seedOffering, state, fmt.Sprintf("%s-k%d", taskID, ordinal),
		fmt.Sprintf("job-%s-%d", taskID, ordinal), failure, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func cleanExchange() wirecapture.Exchange {
	return wirecapture.Exchange{
		Sequence: 1, Complete: true,
		Metadata: wirecapture.Metadata{Scenario: "upstream-record", Connection: "meshy", Protocol: "json", Endpoint: "GET /v2/tasks/id-1",
			APIVersion: "unknown", RequestedModel: "unknown", ReportedModel: "unknown", ServerBuild: "unknown",
			CapturedAt: defaultEpoch, AppRevision: strings.Repeat("ab", 20), CatalogRevision: "2026-11-13.299.2", Environment: "production",
			Provenance: wirecapture.ProvenanceCaptured},
		Request: wirecapture.Request{Method: "GET", Host: "example.test", Path: "/v2/tasks/id-1", Body: wirecapture.Payload{Encoding: wirecapture.PayloadNone}, Complete: true},
		Response: wirecapture.Response{Status: 200, ContentType: "application/json", Complete: true,
			Body: wirecapture.Payload{Encoding: wirecapture.PayloadJSON, JSON: json.RawMessage(`{"status":"SUCCEEDED"}`)}},
	}
}

// writeRecord writes a record file of the recorder's format (0600) named for the
// instant it was recorded.
func (s *testStack) writeRecord(t *testing.T, at time.Time, sequence int, mutate func(*wirecapture.Exchange)) string {
	t.Helper()
	exchange := cleanExchange()
	exchange.Sequence = sequence
	exchange.Metadata.CapturedAt = at
	if mutate != nil {
		mutate(&exchange)
	}
	raw, err := json.MarshalIndent(wirecapture.UpstreamRecord{Version: 1, Exchange: &exchange}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s-%06d%s", at.UTC().Format(recordStampLayout), sequence, wirecapture.UpstreamRecordSuffix)
	if err := os.WriteFile(filepath.Join(s.captures, name), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func (s *testStack) appendLog(t *testing.T, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(s.logs, logging.FileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// fill gives the stack one of everything, with the sensitive values a real one
// would hold around them.
func (s *testStack) fill(t *testing.T) {
	t.Helper()
	failed := `{"class":"rejected","retryable":false,"detail":"upstream refused","upstream_status":402,"upstream_code":"no_credit",` +
		`"upstream_excerpt":"not enough credit for ` + secretEmail + ` prompt ` + secretPrompt + ` see https://api.vendor.example/pay?token=abc123"}`
	s.seed(t,
		seedTask{id: "task_failed01", owner: secretOwnerID, state: "FAILED", failure: failed, finished: defaultEpoch.Add(-time.Hour)},
		seedTask{id: "task_failed02", owner: "owner-short", state: "FAILED", failure: `{"class":"rate_limited","retryable":true,"upstream_status":429}`, finished: defaultEpoch.Add(-2 * time.Hour)},
		seedTask{id: "task_failedold", owner: secretOwnerID, state: "FAILED", failure: failed, finished: defaultEpoch.Add(-72 * time.Hour)},
		seedTask{id: "task_fine", owner: secretOwnerID, state: "SUCCEEDED", finished: defaultEpoch.Add(-time.Hour)},
	)
	s.seedAttempt(t, "task_failed01", 1, "FAILED", `{"class":"rate_limited","upstream_status":429,"upstream_code":"rate_limit"}`)
	s.seedAttempt(t, "task_failed01", 2, "FAILED", failed)
	at := defaultEpoch.Add(-3 * time.Hour)
	s.appendLog(t,
		logLine(at, "INFO", "服务已启动", "address", "0.0.0.0:13333", "revision", strings.Repeat("ab", 20)),
		logLine(at.Add(time.Minute), "ERROR", "HTTP request", "request_id", "req_ab12cd34ef567890", "status", 402, "route", "POST /api/v1/tasks",
			"session_id", "sess_AbCdEfGhIjKlMnOp", "owner_id", secretOwnerID, "error_code", "insufficient_credits",
			"cause", "upstream 402: Authorization: Bearer "+bearerValue+" for "+secretEmail+" at https://api.vendor.example/v1/x?X-Amz-Signature=deadbeef from 10.1.2.3 with sk-abcdefghijklmnopqrstuvwx; "+secretPrompt),
		logLine(at.Add(2*time.Minute), "WARN", "login failed", "username", secretOwnerName, "error", "no such user "+secretOwnerName),
		logLine(at.Add(3*time.Minute), "DEBUG", "任务结束", "task_id", "task_failed01", "offering_id", "meshy/meshy-7/text-to-3d", "elapsed_ms", 1234),
	)
	s.writeRecord(t, defaultEpoch.Add(-2*time.Hour), 1, nil)
	s.writeRecord(t, defaultEpoch.Add(-time.Hour), 2, nil)
	s.writeRecord(t, defaultEpoch.Add(-48*time.Hour), 3, nil) // older than the range
}

func (s *testStack) build(t *testing.T) *Bundle {
	t.Helper()
	bundle, err := Build(context.Background(), s.sources, s.options())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return bundle
}

func (b *Bundle) member(t *testing.T, name string) []byte {
	t.Helper()
	for _, member := range b.Members {
		if member.Path == name {
			return member.Data
		}
	}
	t.Fatalf("no member %q in %v", name, b.paths())
	return nil
}

func (b *Bundle) paths() []string {
	var paths []string
	for _, member := range b.Members {
		paths = append(paths, member.Path)
	}
	return paths
}

func (b *Bundle) category(t *testing.T, name string) CategoryReport {
	t.Helper()
	for _, report := range b.Manifest.Categories {
		if report.Category == name {
			return report
		}
	}
	t.Fatalf("no category %q", name)
	return CategoryReport{}
}

func (b *Bundle) size() int64 {
	var total int64
	for _, member := range b.Members {
		total += int64(len(member.Data))
	}
	return total
}

func (b *Bundle) everything() string {
	var all strings.Builder
	for _, member := range b.Members {
		all.WriteString(member.Path)
		all.WriteByte('\n')
		all.Write(member.Data)
		all.WriteByte('\n')
	}
	return all.String()
}

// The manifest says what the bundle holds, and it is right: counts, sizes,
// digests, the revision, the Catalog and the time range.
func TestBundleHoldsEveryCategoryAndTheManifestMatchesIt(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	bundle := stack.build(t)
	manifest := bundle.Manifest
	if manifest.Format != ManifestFormat || manifest.Revision != strings.Repeat("ab", 20) || manifest.CatalogSHA256 != DocumentSHA256(catalog.DefaultDocument()) ||
		manifest.CatalogRevision == "" || manifest.MaxBytes != DefaultMaxBytes || manifest.GeneratedAt != "2026-11-16T12:00:00Z" ||
		manifest.Range.Since != "2026-11-15T12:00:00Z" || manifest.Range.Until != "2026-11-16T12:00:00Z" {
		t.Fatalf("manifest = %+v", manifest)
	}
	wantOrder := []string{CategoryRuntime, CategorySettings, CategoryFailures, CategoryLogs, CategoryCaptures}
	for i, report := range manifest.Categories {
		if report.Category != wantOrder[i] {
			t.Fatalf("category %d is %s, want %s", i, report.Category, wantOrder[i])
		}
	}
	if bundle.Members[0].Path != "manifest.json" {
		t.Fatalf("manifest.json comes first: %v", bundle.paths())
	}
	// Single-file categories: digest and size are those of the member.
	for _, name := range []string{CategoryRuntime, CategorySettings, CategoryFailures, CategoryLogs} {
		report := bundle.category(t, name)
		data := bundle.member(t, report.Path)
		sum := sha256.Sum256(data)
		if report.Status != StatusIncluded || report.Bytes != int64(len(data)) || report.SHA256 != hex.EncodeToString(sum[:]) || report.Truncated {
			t.Errorf("%s: %+v does not describe its %d bytes", name, report, len(data))
		}
	}
	// runtime
	var runtimeDoc runtimeDocument
	if err := json.Unmarshal(bundle.member(t, "runtime.json"), &runtimeDoc); err != nil || runtimeDoc.Revision != manifest.Revision ||
		runtimeDoc.CatalogSHA256 != manifest.CatalogSHA256 || runtimeDoc.Environment != "production" || runtimeDoc.GoVersion == "" {
		t.Fatalf("runtime = %+v, %v", runtimeDoc, err)
	}
	// settings: the Catalog's list, set flags and no value
	var settingsDoc settingsDocument
	if err := json.Unmarshal(bundle.member(t, "settings.json"), &settingsDoc); err != nil || len(settingsDoc.Settings) != bundle.category(t, CategorySettings).Entries {
		t.Fatalf("settings: %v", err)
	}
	product, _ := catalog.LoadDefault()
	if declared := len(product.Application().Runtime.Configuration.Settings); declared != len(settingsDoc.Settings) || declared < 50 {
		t.Fatalf("the list must be the Catalog's: %d declared, %d listed", declared, len(settingsDoc.Settings))
	}
	entries := map[string]settingEntry{}
	for _, entry := range settingsDoc.Settings {
		entries[entry.Name] = entry
	}
	if got := entries["session_secret"]; !got.Set || !got.Secret || got.Source != "SESSION_SECRET" || got.DeprecatedSource {
		t.Errorf("session_secret = %+v", got)
	}
	if got := entries["meshy_base_url"]; !got.Set || got.Source != "MESHY_API_BASE_URL" || !got.DeprecatedSource || len(got.Environment) != 2 {
		t.Errorf("a setting supplied by a deprecated alias says so: %+v", got)
	}
	if got := entries["tripo_api_key"]; got.Set || got.Source != "" || !got.Secret {
		t.Errorf("tripo_api_key is not set: %+v", got)
	}
	if !sort.SliceIsSorted(settingsDoc.Settings, func(i, j int) bool { return settingsDoc.Settings[i].Name < settingsDoc.Settings[j].Name }) {
		t.Error("settings are listed by name")
	}
	if len(settingsDoc.RetiredEnvironmentSet) != 1 || settingsDoc.RetiredEnvironmentSet[0] != "SSO_APP_SECRET" {
		t.Errorf("retired variables still set: %v", settingsDoc.RetiredEnvironmentSet)
	}
	// failures: the two failed tasks of the range, newest first, no success, nothing old
	var failures failuresDocument
	if err := json.Unmarshal(bundle.member(t, "failures.json"), &failures); err != nil {
		t.Fatal(err)
	}
	failuresReport := bundle.category(t, CategoryFailures)
	if len(failures.Tasks) != 2 || failuresReport.Entries != 2 || failuresReport.Available != 2 {
		t.Fatalf("failures = %+v report %+v", failures.Tasks, failuresReport)
	}
	first := failures.Tasks[0]
	if first.TaskID != "task_failed01" || first.Failure.Class != "rejected" || first.Failure.UpstreamStatus != 402 || first.Failure.UpstreamCode != "no_credit" ||
		first.OfferingID != "meshy/meshy-7/text-to-3d" || len(first.Attempts) != 2 || first.Attempts[0].Class != "rate_limited" || first.Attempts[0].UpstreamStatus != 429 {
		t.Fatalf("first failure = %+v", first)
	}
	if failures.Summary.ByClass["rejected"] != 1 || failures.Summary.ByClass["rate_limited"] != 1 || failures.Summary.ByUpstreamStatus["402"] != 1 || failures.Summary.ByUpstreamCode["no_credit"] != 1 {
		t.Errorf("summary = %+v", failures.Summary)
	}
	if !strings.Contains(first.Failure.UpstreamExcerpt, "not enough credit") || !strings.Contains(first.Failure.UpstreamExcerpt, "[email]") || !strings.Contains(first.Failure.UpstreamExcerpt, "[input]") {
		t.Errorf("excerpt = %q", first.Failure.UpstreamExcerpt)
	}
	// logs: the four lines of the range, one entry per line
	logReport := bundle.category(t, CategoryLogs)
	logData := bundle.member(t, logsMemberPath)
	if logReport.Entries != 4 || logReport.Available != 4 || bytes.Count(logData, []byte("\n")) != 4 {
		t.Fatalf("logs = %+v with %d lines", logReport, bytes.Count(logData, []byte("\n")))
	}
	// captures: the two records of the range, byte for byte
	captureReport := bundle.category(t, CategoryCaptures)
	var captureBytes int64
	captureCount := 0
	for _, member := range bundle.Members {
		if !strings.HasPrefix(member.Path, "captures/") {
			continue
		}
		captureCount++
		captureBytes += int64(len(member.Data))
		onDisk, err := os.ReadFile(filepath.Join(stack.captures, path.Base(member.Path)))
		if err != nil || !bytes.Equal(onDisk, member.Data) {
			t.Errorf("%s is not the recorder's file: %v", member.Path, err)
		}
	}
	if captureCount != 2 || captureReport.Entries != 2 || captureReport.Available != 2 || captureReport.Bytes != captureBytes || captureReport.Path != "captures/" || captureReport.Status != StatusIncluded {
		t.Fatalf("captures = %+v, %d files", captureReport, captureCount)
	}
	// The scan covered every member, the manifest included, and found nothing.
	if manifest.Privacy.FilesScanned != len(bundle.Members) || manifest.Privacy.Findings != 0 || manifest.Privacy.Scanner != "privacyscan" {
		t.Errorf("privacy = %+v for %d members", manifest.Privacy, len(bundle.Members))
	}
	var onDisk Manifest
	if err := json.Unmarshal(bundle.member(t, "manifest.json"), &onDisk); err != nil || fmt.Sprint(onDisk) != fmt.Sprint(manifest) {
		t.Errorf("manifest.json differs from the manifest: %v", err)
	}
	if bundle.size() > manifest.MaxBytes {
		t.Errorf("bundle holds %d bytes, over its limit %d", bundle.size(), manifest.MaxBytes)
	}
}

// The acceptance list of #234: no secret, no prompt, no media, no user identifier
// anywhere in the bundle, however the stack held them.
func TestBundleHoldsNoSecretPromptMediaOrUserIdentifier(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	// Media and identifiers in places a careless writer might have put them.
	stack.appendLog(t, logLine(defaultEpoch.Add(-time.Hour), "DEBUG", "asset stored", "preview", "data:image/png;base64,"+strings.Repeat("iVBORw0KGgo", 40),
		"magic", "\x89PNG\r\n\x1a\n", "blob", strings.Repeat("QUJD", 120), "uuid", "123e4567-e89b-12d3-a456-426614174000"),
		// An address the scrubbing cannot recognize (percent-encoded) still trips the scan: the line is dropped and counted.
		logLine(defaultEpoch.Add(-time.Hour), "WARN", "contact bob%40corp-mail.example"))
	bundle := stack.build(t)
	if report := bundle.category(t, CategoryLogs); report.Dropped["email"] != 1 || report.Entries != 5 || report.Available != 6 {
		t.Errorf("the line that cannot be shipped is dropped and counted: %+v", report)
	}
	everything := bundle.everything()
	for what, value := range map[string]string{
		"session secret": secretSessionValue, "provider key": secretKeyValue, "owner id": secretOwnerID, "user name": secretOwnerName,
		"e-mail address": secretEmail, "prompt": secretPrompt, "bearer token": bearerValue, "signature": "deadbeef", "private address": "10.1.2.3",
		"provider key shape": "sk-abcdefghijklmnopqrstuvwx", "vendor host": "api.vendor.example", "session id": "sess_AbCdEfGhIjKlMnOp",
		"png bytes": "\x89PNG", "png base64": "iVBORw0KGgo", "glb magic": "glTF", "long base64": strings.Repeat("QUJD", 20),
		"uuid": "123e4567-e89b", "token": "token=abc123", "short owner": "owner-short",
	} {
		if strings.Contains(everything, value) {
			t.Errorf("the bundle holds the %s", what)
		}
	}
	// What diagnosis needs is still there.
	for _, kept := range []string{"req_ab12cd34ef567890", "insufficient_credits", "task_failed01", "no_credit", "meshy/meshy-7/text-to-3d", "服务已启动", "SESSION_SECRET"} {
		if !strings.Contains(everything, kept) {
			t.Errorf("%q is missing", kept)
		}
	}
	// One user, one placeholder: the owner of the failed tasks and the owner in
	// the log line are the same id-N.
	var failures failuresDocument
	if err := json.Unmarshal(bundle.member(t, "failures.json"), &failures); err != nil {
		t.Fatal(err)
	}
	ownerPlaceholder := failures.Tasks[0].Owner
	if !strings.HasPrefix(ownerPlaceholder, "id-") || !strings.Contains(string(bundle.member(t, logsMemberPath)), `"owner_id":"`+ownerPlaceholder+`"`) {
		t.Errorf("the failed task's owner %q and the logged owner differ:\n%s", ownerPlaceholder, bundle.member(t, logsMemberPath))
	}
	// And the independent scan agrees on every member.
	if findings := verifyMembers(bundle.Members, NewScrubber()); len(findings) != 0 {
		t.Fatalf("the scan finds %v", findings)
	}
}

// A capture record is never rewritten, so a record that would leak refuses the
// whole export: naming the file and the rule, not the value, and writing nothing.
func TestBundleIsRefusedWhenACaptureRecordLeaks(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	leaky := stack.writeRecord(t, defaultEpoch.Add(-30*time.Minute), 9, func(exchange *wirecapture.Exchange) {
		exchange.Response.Body.JSON = json.RawMessage(`{"status":"SUCCEEDED","contact":"bob@corp-mail.example","host":"10.0.0.7"}`)
	})
	_, err := Build(context.Background(), stack.sources, stack.options())
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want a refusal, got %v", err)
	}
	mentioned := false
	for _, finding := range refused.Findings {
		if strings.Contains(finding.File, leaky) {
			mentioned = true
		}
		if strings.Contains(finding.String(), "bob@corp-mail.example") || strings.Contains(finding.String(), "10.0.0.7") {
			t.Fatalf("a finding names a value: %s", finding)
		}
	}
	if !mentioned || strings.Contains(err.Error(), "bob@") || strings.Contains(err.Error(), "10.0.0.7") || !strings.Contains(err.Error(), leaky) {
		t.Fatalf("the refusal must name the file, never the value: %v", err)
	}
	// Export writes no file at all, not even a temporary one.
	out := filepath.Join(t.TempDir(), "diagnose.tar.gz")
	if _, _, err := Export(context.Background(), stack.sources, stack.options(), out); !errors.As(err, &refused) {
		t.Fatalf("export: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 0 {
		t.Fatalf("a refused export left files behind: %v", entries)
	}
	// Taking the leaking record away lets the export through again.
	if err := os.Remove(filepath.Join(stack.captures, leaky)); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), stack.sources, stack.options()); err != nil {
		t.Fatalf("after the record is removed: %v", err)
	}
}

// The final gate scans what will be written, not what was meant to be written.
func TestVerifyMembersFlagsEachKindOfLeak(t *testing.T) {
	scrubber := NewScrubber()
	scrubber.AddIdentity("user-accounting-77")
	scrubber.AddPrivate("a tiny dragon wearing a red hat")
	members := []Member{
		{Path: "settings.json", Category: CategorySettings, Data: []byte(`{"contact":"bob@corp-mail.example"}`)},
		{Path: logsMemberPath, Category: CategoryLogs, Data: []byte(`{"time":"2026-11-16T00:00:00Z","level":"INFO","msg":"ok"}` + "\n" +
			`{"time":"2026-11-16T00:00:01Z","level":"INFO","msg":"x","authorization":"Bearer abcdefghijklmnop"}` + "\n" +
			`{"time":"2026-11-16T00:00:02Z","level":"INFO","msg":"typed a tiny dragon wearing a red hat"}` + "\n")},
		{Path: "failures.json", Category: CategoryFailures, Data: []byte(`{"owner":"user-accounting-77"}`)},
		{Path: "captures/x.exchange.json", Category: CategoryCaptures, Data: []byte(`{"version":1,"exchange":{"unknown":true}}`)},
		{Path: "captures/y.exchange.json", Category: CategoryCaptures, Data: []byte(`not json`)},
	}
	got := map[string]bool{}
	for _, finding := range verifyMembers(members, scrubber) {
		got[finding.File+" "+finding.Rule] = true
	}
	for _, want := range []string{
		"settings.json email", logsMemberPath + ":2 credential-field", logsMemberPath + ":2 credential-value", logsMemberPath + " known-private-text",
		"failures.json known-identity", "failures.json identity-field", "captures/x.exchange.json record-shape", "captures/y.exchange.json record-shape",
	} {
		if !got[want] {
			t.Errorf("no finding %q in %v", want, got)
		}
	}
	if got[logsMemberPath+":1 email"] || len(got) != 8 {
		t.Errorf("unexpected findings: %v", got)
	}
}

func TestBundleBeyondItsSizeLimitIsCutPerCategoryAndSaysSo(t *testing.T) {
	stack := newTestStack(t)
	var lines []string
	for i := range 3000 {
		lines = append(lines, logLine(defaultEpoch.Add(-time.Duration(3000-i)*time.Second), "DEBUG", fmt.Sprintf("step %04d of a long running flow", i), "request_id", "req_ab12cd34ef567890"))
	}
	stack.appendLog(t, lines...)
	for i := range 60 {
		stack.writeRecord(t, defaultEpoch.Add(-time.Duration(60-i)*time.Minute), i+1, nil)
	}
	var tasks []seedTask
	for i := range 80 {
		tasks = append(tasks, seedTask{id: fmt.Sprintf("task_%04d", i), owner: "owner-1", state: "FAILED", finished: defaultEpoch.Add(-time.Duration(80-i) * time.Minute),
			failure: `{"class":"rejected","detail":"refused","upstream_status":402,"upstream_code":"no_credit","upstream_excerpt":"not enough credit on the account, please top it up and retry the request"}`})
	}
	stack.seed(t, tasks...)
	options := stack.options()
	options.MaxBytes = 100 << 10
	bundle, err := Build(context.Background(), stack.sources, options)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.size() > options.MaxBytes {
		t.Fatalf("the bundle holds %d bytes, over the limit %d", bundle.size(), options.MaxBytes)
	}
	for _, name := range []string{CategoryFailures, CategoryLogs, CategoryCaptures} {
		report := bundle.category(t, name)
		if !report.Truncated || report.Entries >= report.Available || report.Entries == 0 {
			t.Errorf("%s: %+v, want a cut that still holds something", name, report)
		}
	}
	// The newest are the ones kept.
	logs := string(bundle.member(t, logsMemberPath))
	if !strings.Contains(logs, "step 2999 ") || strings.Contains(logs, "step 0000 ") {
		t.Error("the log keeps its newest lines")
	}
	var failures failuresDocument
	if err := json.Unmarshal(bundle.member(t, "failures.json"), &failures); err != nil || failures.Tasks[0].TaskID != "task_0079" {
		t.Errorf("failures keep the newest first: %v", err)
	}
	last := ""
	for _, member := range bundle.Members {
		if strings.HasPrefix(member.Path, "captures/") {
			last = member.Path
		}
	}
	if !strings.Contains(last, "T115900.") { // the newest record, one minute before the epoch
		t.Errorf("the newest capture record is kept: %s", last)
	}
	// The fixed parts are never cut, and a limit too small for them is refused.
	small := stack.options()
	small.MaxBytes = MinMaxBytes
	if _, err := Build(context.Background(), stack.sources, small); err != nil {
		t.Fatalf("the smallest allowed limit still builds: %v", err)
	}
	for _, bad := range []int64{0, MinMaxBytes - 1, MaxMaxBytes + 1} {
		options.MaxBytes = bad
		if _, err := Build(context.Background(), stack.sources, options); err == nil {
			t.Errorf("limit %d accepted", bad)
		}
	}
}

func TestSourcesThatAreMissingAreReportedWithFixedReasons(t *testing.T) {
	stack := newTestStack(t)
	stack.sources.LogDirectory = ""
	stack.sources.CaptureDirectory = filepath.Join(stack.root, "missing")
	stack.sources.DatabasePath = filepath.Join(stack.root, "missing.db")
	stack.sources.Warnings = []string{"setting_unreadable:database_path"}
	bundle := stack.build(t)
	for name, want := range map[string][2]string{
		CategoryLogs: {StatusUnavailable, ReasonNotConfigured}, CategoryCaptures: {StatusUnavailable, ReasonNotFound}, CategoryFailures: {StatusUnavailable, ReasonNotFound},
	} {
		if report := bundle.category(t, name); report.Status != want[0] || report.Reason != want[1] || report.Entries != 0 || report.Path != "" {
			t.Errorf("%s = %+v, want %v", name, report, want)
		}
	}
	if len(bundle.Manifest.Warnings) != 1 || bundle.Manifest.Warnings[0] != "setting_unreadable:database_path" {
		t.Errorf("warnings = %v", bundle.Manifest.Warnings)
	}
	// The runtime and settings are still there: a stack that cannot start is the case that needs them.
	if len(bundle.Members) != 3 || bundle.Members[1].Path != "runtime.json" || bundle.Members[2].Path != "settings.json" {
		t.Errorf("members = %v", bundle.paths())
	}
	// No path or error text reaches the manifest.
	if text := string(bundle.member(t, "manifest.json")); strings.Contains(text, stack.root) || strings.Contains(text, "no such file") {
		t.Errorf("the manifest names the stack's paths: %s", text)
	}
	// Nothing configured at all is not an error either.
	empty := Sources{Revision: "unknown"}
	if _, err := Build(context.Background(), empty, stack.options()); err != nil {
		t.Fatalf("empty sources: %v", err)
	}
	// A database that is not one is unreadable, not fatal.
	notDatabase := filepath.Join(stack.root, "not.db")
	if err := os.WriteFile(notDatabase, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	stack.sources.DatabasePath = notDatabase
	if report := stack.build(t).category(t, CategoryFailures); report.Status != StatusUnavailable || report.Reason != ReasonUnreadable {
		t.Errorf("failures = %+v", report)
	}
}

func TestBuildRejectsATimeRangeThatIsNotInThePast(t *testing.T) {
	stack := newTestStack(t)
	options := stack.options()
	options.Since = defaultEpoch
	if _, err := Build(context.Background(), stack.sources, options); err == nil {
		t.Fatal("a range that ends where it begins is refused")
	}
	options.Since = defaultEpoch.Add(time.Hour)
	if _, err := Build(context.Background(), stack.sources, options); err == nil {
		t.Fatal("a range that begins in the future is refused")
	}
}

type archiveFile struct {
	data []byte
	mode os.FileMode
}

// readArchive unpacks a bundle file into memory, checking it is the tar.gz the
// documentation promises.
func readArchive(t *testing.T, file string) (files map[string]archiveFile, directories map[string]os.FileMode) {
	t.Helper()
	raw, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	unzipped, err := gzip.NewReader(raw)
	if err != nil {
		t.Fatal(err)
	}
	files, directories = map[string]archiveFile{}, map[string]os.FileMode{}
	archive := tar.NewReader(unzipped)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return files, directories
		}
		if err != nil {
			t.Fatal(err)
		}
		if path.IsAbs(header.Name) || strings.Contains(header.Name, "..") {
			t.Fatalf("unsafe member name %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			directories[strings.TrimSuffix(header.Name, "/")] = os.FileMode(header.Mode)
		case tar.TypeReg:
			data, err := io.ReadAll(archive)
			if err != nil {
				t.Fatal(err)
			}
			files[header.Name] = archiveFile{data: data, mode: os.FileMode(header.Mode)}
		default:
			t.Fatalf("member %q is neither a file nor a directory", header.Name)
		}
	}
}

func TestExportWritesOneNewPrivateArchiveAndNothingElse(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "diagnose.tar.gz")
	bundle, written, err := Export(context.Background(), stack.sources, stack.options(), out)
	if err != nil || written != out {
		t.Fatalf("export: %q %v", written, err)
	}
	info, err := os.Lstat(out)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("the archive must be a regular 0600 file: %v %v", info, err)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 1 {
		t.Fatalf("only the archive may be there: %v", entries)
	}
	files, directories := readArchive(t, out)
	root := bundle.Directory()
	if bundle.Directory() != "plinth-diagnose-20261116T120000Z" || bundle.DefaultFileName() != root+".tar.gz" {
		t.Fatalf("directory %q", bundle.Directory())
	}
	// One top-level directory, private directories, private files.
	for directory, mode := range directories {
		if mode.Perm() != 0o700 || (directory != root && !strings.HasPrefix(directory, root+"/")) {
			t.Errorf("directory %q mode %v", directory, mode)
		}
	}
	for _, directory := range []string{root, root + "/logs", root + "/captures"} {
		if _, found := directories[directory]; !found {
			t.Errorf("no directory entry for %s: %v", directory, directories)
		}
	}
	if len(files) != len(bundle.Members) {
		t.Fatalf("%d files in the archive, %d members", len(files), len(bundle.Members))
	}
	for _, member := range bundle.Members {
		got, found := files[root+"/"+member.Path]
		if !found || !bytes.Equal(got.data, member.Data) || got.mode.Perm() != 0o600 {
			t.Errorf("%s: found=%v mode=%v", member.Path, found, got.mode)
		}
	}
	// What the archive says about itself is what it holds, file by file.
	var manifest Manifest
	if err := json.Unmarshal(files[root+"/manifest.json"].data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, report := range manifest.Categories {
		switch report.Category {
		case CategoryLogs:
			lines := bytes.Count(files[root+"/"+report.Path].data, []byte("\n"))
			if lines != report.Entries {
				t.Errorf("logs: %d lines, manifest says %d", lines, report.Entries)
			}
		case CategoryCaptures:
			count, size := 0, int64(0)
			for name, file := range files {
				if strings.HasPrefix(name, root+"/captures/") {
					count++
					size += int64(len(file.data))
				}
			}
			if count != report.Entries || size != report.Bytes {
				t.Errorf("captures: %d files %d bytes, manifest says %d and %d", count, size, report.Entries, report.Bytes)
			}
		case CategoryFailures:
			var document failuresDocument
			if err := json.Unmarshal(files[root+"/"+report.Path].data, &document); err != nil || len(document.Tasks) != report.Entries {
				t.Errorf("failures: %v", err)
			}
		}
	}
	// An existing file is never replaced.
	before, _ := os.ReadFile(out)
	if _, _, err := Export(context.Background(), stack.sources, stack.options(), out); err == nil {
		t.Fatal("overwrote an existing file")
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(before, after) {
		t.Fatal("the existing file changed")
	}
	// A directory gets the default name; a directory that does not exist is an error.
	_, written, err = Export(context.Background(), stack.sources, stack.options(), outDir)
	if err != nil || written != filepath.Join(outDir, root+".tar.gz") {
		t.Fatalf("export into a directory: %q %v", written, err)
	}
	if _, _, err := Export(context.Background(), stack.sources, stack.options(), filepath.Join(outDir, "missing", "x.tar.gz")); err == nil {
		t.Fatal("exported into a directory that does not exist")
	}
	if _, _, err := Export(context.Background(), stack.sources, stack.options(), ""); err == nil {
		t.Fatal("exported without an output path")
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 2 {
		t.Fatalf("refused exports left files behind: %v", entries)
	}
}

// An interrupted export (the command catches SIGINT and SIGTERM) leaves nothing
// behind.
func TestExportStopsWhenCanceledAndWritesNothing(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	out := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Export(ctx, stack.sources, stack.options(), filepath.Join(out, "d.tar.gz")); !errors.Is(err, context.Canceled) {
		t.Fatalf("export: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a canceled export left %v", entries)
	}
}

// The export runs beside a live stack: it must leave every file it reads as it
// found it.
func TestExportLeavesTheStackExactlyAsItWas(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	snapshot := func() map[string]string {
		state := map[string]string{}
		for _, root := range []string{stack.logs, stack.captures, filepath.Dir(stack.database)} {
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), "-shm") {
					continue // the shared-memory index of a WAL database changes with every reader
				}
				full := filepath.Join(root, entry.Name())
				data, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(full)
				sum := sha256.Sum256(data)
				state[full] = fmt.Sprintf("%x %v %v", sum, info.Mode(), info.ModTime().UnixNano())
			}
		}
		return state
	}
	before := snapshot()
	if _, _, err := Export(context.Background(), stack.sources, stack.options(), filepath.Join(t.TempDir(), "d.tar.gz")); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("files appeared or vanished: %v -> %v", before, after)
	}
	for file, state := range before {
		if after[file] != state {
			t.Errorf("%s changed", file)
		}
	}
}

// A running service keeps the database open in WAL mode, and its newest
// failures may exist only in the WAL: the export reads them there, and leaves
// the database and the WAL as it found them.
func TestExportBesideARunningServiceReadsTheWALAndLeavesItAlone(t *testing.T) {
	stack := newTestStack(t)
	live, err := sql.Open("sqlite", "file:"+stack.database)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	stamp := defaultEpoch.Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := live.Exec(`
		INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state, version, created_at, updated_at, finished_at, failure_json)
		VALUES ('task_live', 'owner-live-identity', 'text_to_3d', ?, '{}', 'hash', 'FAILED', 1, ?, ?, ?, '{"class":"timed_out","retryable":true,"upstream_status":504}')`,
		seedOffering, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	digest := func(suffix string) string {
		data, err := os.ReadFile(stack.database + suffix)
		if err != nil {
			t.Fatalf("%s: %v", suffix, err)
		}
		sum := sha256.Sum256(data)
		return fmt.Sprintf("%d:%x", len(data), sum)
	}
	if info, err := os.Stat(stack.database + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the fixture must hold the task in the WAL: %v", err)
	}
	mainBefore, walBefore := digest(""), digest("-wal")
	bundle := stack.build(t)
	var failures failuresDocument
	if err := json.Unmarshal(bundle.member(t, "failures.json"), &failures); err != nil || len(failures.Tasks) != 1 || failures.Tasks[0].TaskID != "task_live" ||
		failures.Tasks[0].Failure.UpstreamStatus != 504 {
		t.Fatalf("the failure that lives in the WAL was not read: %+v %v", failures, err)
	}
	if mainBefore != digest("") || walBefore != digest("-wal") {
		t.Fatal("the export changed the database or its WAL")
	}
}

// Unpacking the archive gives the layout `fake-upstream-align draft` reads: a
// 0700 directory of 0600 record files. This is the first two steps of the
// documented handling of a bundle.
func TestUnpackedCapturesFeedTheSampleDraftWorkflow(t *testing.T) {
	stack := newTestStack(t)
	stack.fill(t)
	out := filepath.Join(t.TempDir(), "diagnose.tar.gz")
	bundle, _, err := Export(context.Background(), stack.sources, stack.options(), out)
	if err != nil {
		t.Fatal(err)
	}
	files, directories := readArchive(t, out)
	unpacked := t.TempDir()
	for name, mode := range directories {
		if err := os.MkdirAll(filepath.Join(unpacked, name), mode.Perm()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(unpacked, name), mode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	for name, file := range files {
		if err := os.WriteFile(filepath.Join(unpacked, name), file.data, file.mode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	records := filepath.Join(unpacked, bundle.Directory(), "captures")
	if info, err := os.Stat(records); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the captures directory must unpack as 0700: %v", err)
	}
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(records)
	if err != nil || len(exchanges) != 2 || skipped != 0 {
		t.Fatalf("load: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
	draftDir := filepath.Join(t.TempDir(), "draft")
	if err := os.Mkdir(draftDir, 0o700); err != nil {
		t.Fatal(err)
	}
	draft, err := wirecapture.WriteDraftCandidate(t.TempDir(), draftDir, "from-bundle", "", exchanges)
	if err != nil || draft.Exchanges != 2 {
		t.Fatalf("draft: %+v %v", draft, err)
	}
	// The scan the documentation recommends before sending agrees.
	if findings, covered, err := privacyscan.ScanRecordDir(records); err != nil || covered != 2 || len(findings) != 0 {
		t.Fatalf("scan: %d %v %v", covered, findings, err)
	}
}

func TestParseSinceAndByteSize(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Time{
		"24h": now.Add(-24 * time.Hour), "90m": now.Add(-90 * time.Minute), "7d": now.AddDate(0, 0, -7), " 1d ": now.AddDate(0, 0, -1),
		"2026-11-15T00:00:00Z": time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), "2026-11-15T08:00:00+08:00": time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
	} {
		if got, err := ParseSince(value, now); err != nil || !got.Equal(want) {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, bad := range []string{"", "0s", "-5m", "0d", "yesterday", "2026-11-17T00:00:00Z", "12", "3650000d", "1h30"} {
		if got, err := ParseSince(bad, now); err == nil {
			t.Errorf("ParseSince(%q) accepted: %v", bad, got)
		}
	}
	for value, want := range map[string]int64{"1048576": 1 << 20, "64k": 64 << 10, "64KiB": 64 << 10, "32m": 32 << 20, "32MB": 32 << 20, "1g": 1 << 30, " 8 M ": 8 << 20} {
		if got, err := ParseByteSize(value); err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", value, got, err, want)
		}
	}
	// The flag defaults say what the limits are.
	if got, err := ParseByteSize(DefaultMaxBytesText); err != nil || got != DefaultMaxBytes {
		t.Errorf("DefaultMaxBytesText = %d, %v; want %d", got, err, DefaultMaxBytes)
	}
	if got, err := ParseSince(DefaultSince, now); err != nil || !got.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("DefaultSince = %v, %v", got, err)
	}
	for _, bad := range []string{"", "m", "-1", "1.5m", "12x", "2g", "99999999999999999999", "9999999999g"} {
		if got, err := ParseByteSize(bad); err == nil {
			t.Errorf("ParseByteSize(%q) accepted: %d", bad, got)
		}
	}
}
