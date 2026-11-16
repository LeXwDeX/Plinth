package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/diagbundle"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// `art-ai diagnose` (#234) against the fake stack: a real failed task, real
// capture records and a log directory, exported by the same entry point the
// command line uses.

// diagnoseClockAfter fixes the export's clock after everything the fake stack
// wrote: the stack's task clock is in September 2026 and its capture records
// carry the time the test runs.
func diagnoseClockAfter(t *testing.T) {
	t.Helper()
	original := diagnoseClock
	t.Cleanup(func() { diagnoseClock = original })
	floor := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	diagnoseClock = func() time.Time {
		if now := time.Now(); now.After(floor) {
			return now
		}
		return floor
	}
}

type diagnoseRun struct {
	stack    *captureStack
	harness  *acmeHarness
	logs     string
	lookup   func(string) string
	failedID generation.TaskID
	okID     generation.TaskID
}

const (
	diagnoseSecretKey = "fake-insufficient-balance-key"
	diagnosePrompt    = "a plain cube with a very private inscription"
	diagnoseOwner     = "owner-acme"
)

// newDiagnoseRun runs one task that succeeds and one that fails with the
// upstream's "not enough credit" answer, with capture on, and gives the stack a
// log directory holding both what the service writes and what a leak would look like.
func newDiagnoseRun(t *testing.T) *diagnoseRun {
	t.Helper()
	diagnoseClockAfter(t)
	stack := newCaptureStack(t)
	stack.settings["meshy_api_key"] = diagnoseSecretKey
	harness := newAcmeHarnessWithTransport(t, stack.product, stack.settings, stack.transport())
	good := harness.submitApproved(t, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		json.RawMessage(`{"prompt":"`+diagnosePrompt+`","authorize_extra_credits":true}`), "diag-good")
	if task := harness.runUntilTerminal(t, diagnoseOwner, good.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("the healthy task: %s %#v", task.State, task.Failure)
	}
	bad := harness.submitApproved(t, generation.TextTo3D, "meshy/meshy-7.1/text-to-3d",
		json.RawMessage(`{"prompt":"`+diagnosePrompt+`","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`), "diag-bad")
	failed := harness.runUntilTerminal(t, diagnoseOwner, bad.TaskID)
	if failed.State != generation.TaskFailed || failed.Failure == nil {
		t.Fatalf("the failing task: %s %#v", failed.State, failed.Failure)
	}

	logs := filepath.Join(t.TempDir(), "logs")
	file, err := logging.OpenRotatingFile(logging.RotateConfig{Directory: logs, FileName: logging.FileName, MaxBytes: 1 << 20, MaxFiles: 3, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.New(file, slog.LevelDebug)
	logger.Info("HTTP request", "request_id", "req_ab12cd34ef567890", "method", "POST", "route", "POST /api/v1/tasks", "status", 402,
		"session_id", "sess_AbCdEfGhIjKlMnOp", "owner_id", diagnoseOwner, "error_code", "insufficient_credits",
		"cause", "upstream 402 for "+string(bad.TaskID))
	logger.Debug("任务结束", "task_id", string(bad.TaskID), "offering_id", "meshy/meshy-7.1/text-to-3d")
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// Lines the service would never write, as an older build or a missed
	// redaction might have: the export must not trust the file.
	leak := func(at time.Time, level, message, attributes string) string {
		return fmt.Sprintf(`{"time":%q,"level":%q,"msg":%q%s}`, at.Format(time.RFC3339Nano), level, message, attributes)
	}
	appendFile(t, filepath.Join(logs, logging.FileName), strings.Join([]string{
		leak(time.Date(2026, 9, 18, 12, 5, 0, 0, time.UTC), "ERROR", "upstream call failed",
			`,"authorization":"Bearer abcdefghijklmnop1234","cause":"POST https://api.vendor.example/v2/x?X-Amz-Signature=deadbeef failed for carol@corp-mail.example with sk-abcdefghijklmnopqrstuvwx from 10.1.2.3: `+diagnosePrompt+`"`),
		leak(time.Date(2026, 9, 18, 12, 6, 0, 0, time.UTC), "WARN", "login failed", `,"username":"carol-longname","error":"no such user carol-longname"`),
	}, "\n")+"\n")

	env := map[string]string{
		"APP_ENV": "test", "DATABASE_PATH": harness.databasePath, "LOG_DIR": logs, "UPSTREAM_CAPTURE_DIR": stack.dir,
		"MESHY_API_KEY": diagnoseSecretKey, "SESSION_SECRET": "session-secret-value-0123456789abcdefghij", "ADMIN_PASSWORD": "hunter2-admin-pass",
		"TRIPO_API_KEY": "fake-test-key", "OPENAI_BASE_URL": stack.server.URL,
	}
	return &diagnoseRun{stack: stack, harness: harness, logs: logs, failedID: bad.TaskID, okID: good.TaskID,
		lookup: func(name string) string { return env[name] }}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func (r *diagnoseRun) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = RunDiagnose(context.Background(), args, r.lookup, &out, &errOut)
	return code, out.String(), errOut.String()
}

func unpack(t *testing.T, file string) (files map[string][]byte, modes map[string]os.FileMode) {
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
	files, modes = map[string][]byte{}, map[string]os.FileMode{}
	archive := tar.NewReader(unzipped)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return files, modes
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[header.Name] = os.FileMode(header.Mode)
		if header.Typeflag == tar.TypeReg {
			if files[header.Name], err = io.ReadAll(archive); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%x", len(data), sha256.Sum256(data))
}

// The whole path: fake upstream, failed task, capture records, logs, then the
// bundle the command writes. Content and manifest agree, the scan passes, and
// nothing a stack keeps private is in it.
func TestDiagnoseExportsAVerifiedBundleFromTheFakeStack(t *testing.T) {
	run := newDiagnoseRun(t)
	out := t.TempDir()
	databaseBefore, walBefore := fileDigest(t, run.harness.databasePath), fileDigest(t, run.harness.databasePath+"-wal")

	code, stdout, stderr := run.run(t, "--out", out, "--since", "2026-09-01T00:00:00Z", "--max-bytes", "8m")
	if code != DiagnoseExitOK {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("exactly one file is written: %v %v", entries, err)
	}
	archivePath := filepath.Join(out, entries[0].Name())
	if info, err := os.Stat(archivePath); err != nil || info.Mode().Perm() != 0o600 || !strings.HasPrefix(entries[0].Name(), "plinth-diagnose-") || !strings.HasSuffix(entries[0].Name(), ".tar.gz") {
		t.Fatalf("the archive: %v %v", info, err)
	}
	for _, want := range []string{archivePath, "failures", "logs", "captures", "runtime", "settings"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the summary lacks %q:\n%s", want, stdout)
		}
	}
	// The stack is as it was.
	if databaseBefore != fileDigest(t, run.harness.databasePath) || walBefore != fileDigest(t, run.harness.databasePath+"-wal") {
		t.Fatal("the export changed the database")
	}

	files, modes := unpack(t, archivePath)
	root := strings.TrimSuffix(entries[0].Name(), ".tar.gz")
	var manifest diagbundle.Manifest
	if err := json.Unmarshal(files[root+"/manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	categories := map[string]diagbundle.CategoryReport{}
	for _, report := range manifest.Categories {
		categories[report.Category] = report
	}
	if manifest.Revision != "unknown" || manifest.CatalogSHA256 != diagbundle.DocumentSHA256(catalog.DefaultDocument()) || manifest.Range.Since != "2026-09-01T00:00:00Z" ||
		manifest.MaxBytes != 8<<20 || manifest.Privacy.Findings != 0 || manifest.Privacy.FilesScanned != len(files) {
		t.Fatalf("manifest = %+v with %d files", manifest, len(files))
	}

	// failures: the failed task, with what the upstream said, and not the good one
	var failures struct {
		Tasks []struct {
			TaskID     string `json:"task_id"`
			Owner      string `json:"owner"`
			OfferingID string `json:"offering_id"`
			Failure    struct {
				Class          string `json:"class"`
				UpstreamStatus int    `json:"upstream_status"`
			} `json:"failure"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(files[root+"/failures.json"], &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures.Tasks) != 1 || failures.Tasks[0].TaskID != string(run.failedID) || failures.Tasks[0].OfferingID != "meshy/meshy-7.1/text-to-3d" ||
		failures.Tasks[0].Failure.Class == "" || failures.Tasks[0].Failure.UpstreamStatus == 0 || !strings.HasPrefix(failures.Tasks[0].Owner, "id-") {
		t.Fatalf("failures = %+v", failures)
	}
	if categories[diagbundle.CategoryFailures].Entries != 1 || strings.Contains(string(files[root+"/failures.json"]), string(run.okID)) {
		t.Fatalf("only the failed task is summarized: %+v", categories[diagbundle.CategoryFailures])
	}

	// captures: every record of the stack, as the recorder wrote it
	names, err := wirecapture.ListUpstreamRecords(run.stack.dir)
	if err != nil || len(names) == 0 || categories[diagbundle.CategoryCaptures].Entries != len(names) {
		t.Fatalf("captures: %d records on disk, manifest %+v (%v)", len(names), categories[diagbundle.CategoryCaptures], err)
	}
	for _, name := range names {
		onDisk, _ := os.ReadFile(filepath.Join(run.stack.dir, name))
		if !bytes.Equal(files[root+"/captures/"+name], onDisk) {
			t.Errorf("capture %s differs from the recorder's file", name)
		}
		if modes[root+"/captures/"+name].Perm() != 0o600 {
			t.Errorf("capture %s must unpack as 0600: %v", name, modes[root+"/captures/"+name])
		}
	}
	if modes[root+"/captures/"].Perm() != 0o700 {
		t.Errorf("captures/ must unpack as 0700: %v", modes[root+"/captures/"])
	}

	// logs: the lines of the range, scrubbed
	logData := string(files[root+"/logs/art-ai.jsonl"])
	logReport := categories[diagbundle.CategoryLogs]
	if logReport.Entries != 4 || bytes.Count([]byte(logData), []byte("\n")) != 4 || logReport.Truncated || logReport.Dropped != nil {
		t.Fatalf("logs = %+v\n%s", logReport, logData)
	}
	for _, kept := range []string{"req_ab12cd34ef567890", "insufficient_credits", string(run.failedID), "meshy/meshy-7.1/text-to-3d"} {
		if !strings.Contains(logData, kept) {
			t.Errorf("log lost %q", kept)
		}
	}

	// settings: the Catalog's names, set or not, and no value
	var settings struct {
		Settings []struct {
			Name string `json:"name"`
			Set  bool   `json:"set"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(files[root+"/settings.json"], &settings); err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, setting := range settings.Settings {
		set[setting.Name] = setting.Set
	}
	if !set["meshy_api_key"] || !set["session_secret"] || !set["database_path"] || set["gemini_api_key"] || len(settings.Settings) != categories[diagbundle.CategorySettings].Entries {
		t.Fatalf("settings: %v", set)
	}

	// The acceptance list: no key, prompt, media, address or user identifier.
	everything := ""
	for name, data := range files {
		everything += name + "\n" + string(data) + "\n"
	}
	for what, value := range map[string]string{
		"upstream key": diagnoseSecretKey, "session secret": "session-secret-value-0123456789abcdefghij", "admin password": "hunter2-admin-pass",
		"prompt": diagnosePrompt, "owner": diagnoseOwner, "user name": "carol-longname", "e-mail": "carol@corp-mail.example",
		"bearer": "abcdefghijklmnop1234", "signature": "deadbeef", "private address": "10.1.2.3", "provider key": "sk-abcdefghijklmnopqrstuvwx",
		"session id": "sess_AbCdEfGhIjKlMnOp", "fake upstream address": run.stack.fakeURL.Host, "authorization header": "Authorization", "bearer scheme": "Bearer ",
		"database path": run.harness.databasePath, "log path": run.logs, "capture path": run.stack.dir, "glb": "glTF", "png": "\x89PNG",
	} {
		if strings.Contains(everything, value) {
			t.Errorf("the bundle holds the %s (%q)", what, value)
		}
	}
	// And the independent scan agrees on each member, as the documentation
	// asks the receiver to check.
	for name, data := range files {
		switch {
		case strings.Contains(name, "/captures/") && strings.HasSuffix(name, wirecapture.UpstreamRecordSuffix):
			record, err := wirecapture.ParseUpstreamRecord(name, data)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if findings, _ := privacyscan.ScanRecord(name, record); len(findings) != 0 {
				t.Errorf("%s: %v", name, findings)
			}
		case strings.HasSuffix(name, ".jsonl"):
			for number, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
				if findings := privacyscan.ScanJSON(fmt.Sprintf("%s:%d", name, number+1), line); len(findings) != 0 {
					t.Errorf("%v", findings)
				}
			}
		case strings.HasSuffix(name, ".json"):
			if findings := privacyscan.ScanJSON(name, data); len(findings) != 0 {
				t.Errorf("%v", findings)
			}
		}
	}

	// The next step of the documented handling: the unpacked captures are what
	// `fake-upstream-align draft` reads.
	unpacked := t.TempDir()
	for name, data := range files {
		if strings.HasSuffix(name, "/") {
			continue
		}
		target := filepath.Join(unpacked, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, modes[name].Perm()); err != nil {
			t.Fatal(err)
		}
	}
	recordsDir := filepath.Join(unpacked, root, "captures")
	if err := os.Chmod(recordsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exchanges, skipped, err := wirecapture.LoadUpstreamRecords(recordsDir)
	if err != nil || len(exchanges)+skipped != len(names) || len(exchanges) == 0 {
		t.Fatalf("the unpacked captures: %d exchanges, %d skipped, %v", len(exchanges), skipped, err)
	}
}

func TestDiagnoseRefusesWhenACaptureRecordWouldLeak(t *testing.T) {
	run := newDiagnoseRun(t)
	names, err := wirecapture.ListUpstreamRecords(run.stack.dir)
	if err != nil || len(names) == 0 {
		t.Fatalf("records: %v %v", names, err)
	}
	// A record as a damaged recorder or a hand edit might leave it.
	record, err := wirecapture.ReadUpstreamRecord(filepath.Join(run.stack.dir, names[len(names)-1]))
	if err != nil || record.Exchange == nil {
		t.Fatalf("a record with an exchange is needed: %v", err)
	}
	record.Exchange.Response.Body.JSON = json.RawMessage(`{"status":"FAILED","contact":"dave@corp-mail.example","host":"10.0.0.7"}`)
	leaking := "99990101T000000.000000000Z-999999" + wirecapture.UpstreamRecordSuffix
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.stack.dir, leaking), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(run.stack.dir, leaking)) })
	diagnoseClock = func() time.Time { return time.Date(9999, 6, 1, 0, 0, 0, 0, time.UTC) }

	out := t.TempDir()
	code, stdout, stderr := run.run(t, "--out", out, "--since", "2026-09-01T00:00:00Z")
	if code != DiagnoseExitRefused || stdout != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, leaking) || !strings.Contains(stderr, "exchange-invalid") || strings.Contains(stderr, "dave@") || strings.Contains(stderr, "10.0.0.7") {
		t.Fatalf("the refusal must name the file and the rule, never the value:\n%s", stderr)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a refused export wrote %v", entries)
	}
}

func TestDiagnoseRejectsBadArgumentsAndNeverOverwrites(t *testing.T) {
	run := newDiagnoseRun(t)
	out := t.TempDir()
	for name, args := range map[string][]string{
		"no output":       {},
		"positional":      {"--out", out, "extra"},
		"bad range":       {"--out", out, "--since", "yesterday"},
		"future range":    {"--out", out, "--since", "9999-01-01T00:00:00Z"},
		"zero range":      {"--out", out, "--since", "0s"},
		"bad size":        {"--out", out, "--max-bytes", "lots"},
		"unknown flag":    {"--out", out, "--bogus"},
		"size below min":  {"--out", out, "--max-bytes", "1k"},
		"size over limit": {"--out", out, "--max-bytes", "2g"},
	} {
		code, stdout, stderr := run.run(t, args...)
		wantCode := DiagnoseExitUsage
		if name == "size below min" {
			wantCode = DiagnoseExitFailed // a valid number, refused by the export
		}
		if code != wantCode || stdout != "" || stderr == "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("bad arguments wrote %v", entries)
	}
	if code, _, stderr := run.run(t, "--help"); code != DiagnoseExitOK || !strings.Contains(stderr, "--out") || !strings.Contains(stderr, "退出码") {
		t.Errorf("--help: exit %d, %q", code, stderr)
	}
	file := filepath.Join(out, "bundle.tar.gz")
	if code, _, stderr := run.run(t, "--out", file, "--since", "2026-09-01T00:00:00Z"); code != DiagnoseExitOK {
		t.Fatalf("first export: %d %s", code, stderr)
	}
	before := fileDigest(t, file)
	if code, _, stderr := run.run(t, "--out", file, "--since", "2026-09-01T00:00:00Z"); code != DiagnoseExitFailed || !strings.Contains(stderr, "不会覆盖") {
		t.Fatalf("second export over the same file: %d %s", code, stderr)
	}
	if fileDigest(t, file) != before {
		t.Fatal("the existing file was overwritten")
	}
}

// A stack that refuses to start over one bad setting is the one that needs a
// bundle: the export reads what it can and says what it could not.
func TestDiagnoseSourcesToleratesSettingsThatWouldStopAStart(t *testing.T) {
	diagnoseClockAfter(t)
	dir := t.TempDir()
	env := map[string]string{
		"APP_ENV": "staging-typo", "DATABASE_PATH": filepath.Join(dir, "art-ai.db"), "LOG_DIR": filepath.Join(dir, "logs"),
		"PUBLIC_BASE_URL": "not a url", "SESSION_SECRET": "short", "PROVIDER_MODE": "weird", "LOG_LEVEL": "loud",
	}
	lookup := func(name string) string { return env[name] }
	sources, err := diagnoseSources(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if sources.DatabasePath != env["DATABASE_PATH"] || sources.LogDirectory != env["LOG_DIR"] || sources.CaptureDirectory != "" || sources.LogFileName != logging.FileName {
		t.Fatalf("sources = %+v", sources)
	}
	if len(sources.Warnings) != 1 || sources.Warnings[0] != "setting_unreadable:application_environment" {
		t.Fatalf("an environment label the Catalog does not allow is noted, not fatal: %v", sources.Warnings)
	}
	if _, err := loadConfigFromSchema(defaultSchema(t), lookup); err == nil {
		t.Fatal("the same settings must stop a real start, or this test proves nothing")
	}
	// Defaults: where the service would put its files.
	if bare, err := diagnoseSources(func(string) string { return "" }); err != nil || bare.DatabasePath != "./data/art-ai-go.db" || bare.LogDirectory != "" || bare.Revision != "unknown" ||
		bare.CatalogSHA256 != diagbundle.DocumentSHA256(catalog.DefaultDocument()) || len(bare.Settings) < 50 || len(bare.Warnings) != 0 {
		t.Fatalf("defaults = %+v, %v", bare, err)
	}
}

// The Catalog the stack runs is the file it names: its digest, its revision and
// its settings are what the bundle describes.
func TestDiagnoseSourcesDescribeTheConfiguredCatalogFile(t *testing.T) {
	dir := t.TempDir()
	document := catalog.DefaultDocument()
	custom := bytes.Replace(document, []byte(`"revision": "`), []byte(`"revision": "customer-edition-`), 1)
	if bytes.Equal(custom, document) {
		t.Fatal("the default Catalog's revision could not be changed for the test")
	}
	file := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(file, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	sources, err := diagnoseSources(func(name string) string {
		if name == "CATALOG_FILE" {
			return file
		}
		return ""
	})
	if err != nil || len(sources.Warnings) != 0 || !strings.HasPrefix(sources.CatalogRevision, "customer-edition-") || sources.CatalogSHA256 != diagbundle.DocumentSHA256(custom) ||
		sources.CatalogSHA256 == diagbundle.DocumentSHA256(document) {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	// A file that cannot be read or parsed is noted; the built-in declarations
	// stand in, and no digest is claimed for a file that was not read.
	missing, err := diagnoseSources(func(name string) string {
		if name == "CATALOG_FILE" {
			return filepath.Join(dir, "missing.json")
		}
		return ""
	})
	if err != nil || len(missing.Warnings) != 1 || missing.Warnings[0] != "catalog_file_unreadable" || missing.CatalogSHA256 != "" || len(missing.Settings) < 50 {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"version": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid, err := diagnoseSources(func(name string) string {
		if name == "CATALOG_FILE" {
			return broken
		}
		return ""
	})
	if err != nil || len(invalid.Warnings) != 1 || invalid.Warnings[0] != "catalog_file_invalid" || invalid.CatalogSHA256 != diagbundle.DocumentSHA256([]byte(`{"version": 1}`)) {
		t.Fatalf("invalid = %+v, %v", invalid, err)
	}
}
