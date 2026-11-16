package privacyscan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// The scan's rules are exercised by negative controls in
// internal/testutil/wirefixture (corpus_privacy_test.go). These tests cover
// what the package adds for the application's recorder (#233): single values
// and a whole record directory.

func TestScanTextReportsTheRuleAndThePathNeverTheValue(t *testing.T) {
	for _, tc := range []struct{ key, text, rule string }{
		{"message", "mail alice@corp-mail.com", "email"},
		{"note", "dial 10.1.2.3", "private-ip"},
		{"note", "Bearer abcdefghijklmnop1234", "credential-value"},
		{"api_key", "real-looking-value-123", "credential-field"},
		{"note", "https://example.test/file?X-Amz-Signature=abc", "url-query"},
		{"note", "see https://cdn.vendor.com/file", "url-host"},
	} {
		findings := privacyscan.ScanText("record.json", "$.field", tc.key, tc.text)
		if !privacyscan.HasRule(findings, tc.rule) {
			t.Errorf("%q under %q: rules %v, want %q", tc.text, tc.key, findings, tc.rule)
		}
		for _, finding := range findings {
			if strings.Contains(finding.String(), tc.text) || finding.File != "record.json" || finding.Path != "$.field" {
				t.Errorf("finding leaks or loses its place: %s", finding)
			}
		}
	}
	for _, clean := range []string{"", "SUCCEEDED", "[redacted]", "id-12", "unsupported-body", "meshy-5", "example.test"} {
		if findings := privacyscan.ScanText("f", "$", "status", clean); len(findings) != 0 {
			t.Errorf("clean text %q flagged: %v", clean, findings)
		}
	}
	if privacyscan.HasRule(nil, "email") {
		t.Error("no findings, no rule")
	}
}

func recordDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "records")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeRecord(t *testing.T, dir, name string, record wirecapture.UpstreamRecord, mode os.FileMode) {
	t.Helper()
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+wirecapture.UpstreamRecordSuffix), append(raw, '\n'), mode); err != nil {
		t.Fatal(err)
	}
}

func cleanExchange() wirecapture.Exchange {
	return wirecapture.Exchange{
		Sequence: 1, Complete: true,
		Metadata: wirecapture.Metadata{Scenario: "upstream-record", Connection: "tripo", Protocol: "json", Endpoint: "GET /v2/tasks/id-1",
			APIVersion: "unknown", RequestedModel: "unknown", ReportedModel: "unknown", ServerBuild: "unknown",
			CapturedAt: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), AppRevision: "0123456789abcdef0123456789abcdef01234567",
			CatalogRevision: "2026-11-13.299.2", Environment: "production", Provenance: wirecapture.ProvenanceCaptured},
		Request: wirecapture.Request{Method: "GET", Host: "example.test", Path: "/v2/tasks/id-1", Body: wirecapture.Payload{Encoding: wirecapture.PayloadNone}, Complete: true},
		Response: wirecapture.Response{Status: 200, ContentType: "application/json", Complete: true,
			Body: wirecapture.Payload{Encoding: wirecapture.PayloadJSON, JSON: json.RawMessage(`{"status":"SUCCEEDED"}`)}},
	}
}

func TestScanRecordDirCoversCleanRecordsAndFlagsLeaks(t *testing.T) {
	dir := recordDirectory(t)
	clean := cleanExchange()
	writeRecord(t, dir, "20261116T100000.000000000Z-000001", wirecapture.UpstreamRecord{Version: 1, Exchange: &clean}, 0o600)
	writeRecord(t, dir, "20261116T100001.000000000Z-000002", wirecapture.UpstreamRecord{
		Version: 1, Skipped: "unsupported-body", Connection: "tripo", Method: "GET", Status: 502, Sequence: 2}, 0o600)
	findings, covered, err := privacyscan.ScanRecordDir(dir)
	if err != nil || covered != 1 || len(findings) != 0 {
		t.Fatalf("clean directory: covered=%d findings=%v err=%v", covered, findings, err)
	}

	// An exchange the sanitizer would never have written: private data in a
	// body. The scan names the record, not the value.
	leaky := cleanExchange()
	leaky.Sequence = 3
	leaky.Response.Body.JSON = json.RawMessage(`{"status":"SUCCEEDED","contact":"bob@corp-mail.com","host":"10.0.0.7"}`)
	writeRecord(t, dir, "20261116T100002.000000000Z-000003", wirecapture.UpstreamRecord{Version: 1, Exchange: &leaky}, 0o600)
	// A marker whose fixed fields were tampered with.
	writeRecord(t, dir, "20261116T100003.000000000Z-000004", wirecapture.UpstreamRecord{
		Version: 1, Skipped: "token=abc", Connection: "10.0.0.8", Method: "GET", Status: 99999, Sequence: 4, QueryKeys: []string{"signature=abc"}}, 0o600)
	findings, covered, err = privacyscan.ScanRecordDir(dir)
	if err != nil || covered != 2 {
		t.Fatalf("covered=%d err=%v", covered, err)
	}
	if !privacyscan.HasRule(findings, "exchange-invalid") || !privacyscan.HasRule(findings, "private-ip") ||
		!privacyscan.HasRule(findings, "record-shape") || !privacyscan.HasRule(findings, "signed-url") {
		t.Fatalf("a leak went unreported: %v", findings)
	}
	for _, finding := range findings {
		for _, value := range []string{"bob@corp-mail.com", "10.0.0.7", "10.0.0.8", "token=abc"} {
			if strings.Contains(finding.String(), value) {
				t.Fatalf("finding leaks %q: %s", value, finding)
			}
		}
	}
}

// ScanRecord is the per-record rule set of ScanRecordDir, for callers that read
// the bytes themselves (the diagnostics export, #234): same findings, and it
// says whether the record held an exchange.
func TestScanRecordMatchesTheDirectoryScanRecordByRecord(t *testing.T) {
	dir := recordDirectory(t)
	clean := cleanExchange()
	leaky := cleanExchange()
	leaky.Response.Body.JSON = json.RawMessage(`{"status":"SUCCEEDED","contact":"bob@corp-mail.com","host":"10.0.0.7"}`)
	marker := wirecapture.UpstreamRecord{Version: 1, Skipped: "token=abc", Connection: "tripo", Method: "GET", Status: 99999, QueryKeys: []string{"signature=abc"}}
	records := map[string]wirecapture.UpstreamRecord{
		"20261116T100000.000000000Z-000001": {Version: 1, Exchange: &clean},
		"20261116T100001.000000000Z-000002": {Version: 1, Exchange: &leaky},
		"20261116T100002.000000000Z-000003": marker,
	}
	for name, record := range records {
		writeRecord(t, dir, name, record, 0o600)
	}
	var want []privacyscan.Finding
	for _, name := range []string{"20261116T100000.000000000Z-000001", "20261116T100001.000000000Z-000002", "20261116T100002.000000000Z-000003"} {
		file := name + wirecapture.UpstreamRecordSuffix
		read, err := wirecapture.ReadUpstreamRecord(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		findings, hasExchange := privacyscan.ScanRecord(file, read)
		if hasExchange != (read.Exchange != nil) {
			t.Errorf("%s: hasExchange=%v for exchange=%v", file, hasExchange, read.Exchange != nil)
		}
		want = append(want, findings...)
	}
	got, covered, err := privacyscan.ScanRecordDir(dir)
	if err != nil || covered != 2 || len(want) == 0 {
		t.Fatalf("covered=%d err=%v want=%v", covered, err, want)
	}
	if len(got) != len(want) {
		t.Fatalf("directory scan reported %d findings, per-record scan %d: %v vs %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("finding %d: directory %v, record %v", i, got[i], want[i])
		}
	}
	// A record with no exchange and clean fields is clean and covers nothing.
	if findings, hasExchange := privacyscan.ScanRecord("m.exchange.json", wirecapture.UpstreamRecord{
		Version: 1, Skipped: "unsupported-body", Connection: "tripo", Method: "GET", Status: 502}); len(findings) != 0 || hasExchange {
		t.Fatalf("clean marker: %v %v", findings, hasExchange)
	}
}

// A file the reader refuses is an error, never a quiet pass.
func TestScanRecordDirRefusesUnsafeFiles(t *testing.T) {
	dir := recordDirectory(t)
	clean := cleanExchange()
	writeRecord(t, dir, "20261116T100000.000000000Z-000001", wirecapture.UpstreamRecord{Version: 1, Exchange: &clean}, 0o644)
	if _, _, err := privacyscan.ScanRecordDir(dir); err == nil {
		t.Fatal("a world-readable record must not be scanned as if it were fine")
	}
	other := recordDirectory(t)
	if err := os.WriteFile(filepath.Join(other, "x"+wirecapture.UpstreamRecordSuffix), []byte(`{"version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := privacyscan.ScanRecordDir(other); err == nil {
		t.Fatal("an unknown field must be refused")
	}
	if _, _, err := privacyscan.ScanRecordDir(filepath.Join(other, "missing")); err == nil {
		t.Fatal("a missing directory must be an error")
	}
	empty := recordDirectory(t)
	if findings, covered, err := privacyscan.ScanRecordDir(empty); err != nil || covered != 0 || len(findings) != 0 {
		t.Fatalf("an empty directory covers nothing: %d %v %v", covered, findings, err)
	}
}
