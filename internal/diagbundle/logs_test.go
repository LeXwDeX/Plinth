package diagbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

var (
	rangeSince = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	rangeUntil = rangeSince.Add(24 * time.Hour)
)

// logLine renders a line the way the service's JSON handler does: time, level
// and message first, then the attributes.
func logLine(at time.Time, level, message string, attributes ...any) string {
	var line strings.Builder
	fmt.Fprintf(&line, `{"time":%q,"level":%q,"msg":%s`, at.Format(time.RFC3339Nano), level, mustJSON(message))
	for i := 0; i+1 < len(attributes); i += 2 {
		fmt.Fprintf(&line, ",%s:%s", mustJSON(attributes[i]), mustJSON(attributes[i+1]))
	}
	line.WriteByte('}')
	return line.String()
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func writeLogFile(t *testing.T, directory, name string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, directory string, budget int64, scrubber *Scrubber) logResult {
	t.Helper()
	result, err := collectLogs(context.Background(), Sources{LogDirectory: directory}, rangeSince, rangeUntil, budget, scrubber)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Every line is rewritten value by value; what the application itself already
// redacted stays redacted, and what the export knows to be sensitive goes.
func TestLogLinesAreScrubbedValueByValue(t *testing.T) {
	directory := t.TempDir()
	at := rangeSince.Add(time.Hour)
	writeLogFile(t, directory, logging.FileName,
		logLine(at, "ERROR", "HTTP request",
			"request_id", "req_ab12cd34ef567890", "method", "POST", "path", "/api/v1/tasks", "status", 402, "duration_ms", 12,
			"session_id", "sess_AbCdEfGhIjKlMnOp", "owner_id", "owner-secret-id-1", "error_code", "insufficient_credits",
			"authorization", "Bearer abcdefghijklmnop1234",
			"cause", "upstream 402: Authorization: Bearer zzzzzzzzzzzzzzzz9999 refused alice@corp-mail.example at https://api.vendor.example/v1/x?X-Amz-Signature=deadbeef from 10.1.2.3 using sk-abcdefghijklmnopqrstuvwx"),
		logLine(at.Add(time.Second), "DEBUG", "login failed", "username", "alice", "error", "no such user; prompt was: a tiny dragon wearing a red hat",
			"prompt", "a tiny dragon wearing a red hat", "nested", map[string]any{"token": "tok-12345678", "peer": "[fd00::1]:443"}),
		logLine(at.Add(2*time.Second), "INFO", "任务完成", "task_id", "task_AbCdEfGhIjKlMnOp", "offering_id", "meshy/meshy-7/text-to-3d", "elapsed", "1.5s"),
	)
	scrubber := NewScrubber()
	scrubber.AddPrivate("a tiny dragon wearing a red hat")
	result := collect(t, directory, 1<<20, scrubber)
	if result.status != StatusIncluded || result.entries != 3 || result.available != 3 || result.truncated || result.dropped != nil {
		t.Fatalf("result = %+v", result)
	}
	text := string(result.data)
	for _, secret := range []string{
		"abcdefghijklmnop1234", "zzzzzzzzzzzzzzzz9999", "alice", "corp-mail.example", "api.vendor.example", "deadbeef", "10.1.2.3",
		"sk-abcdefghijklmnopqrstuvwx", "sess_AbCdEfGhIjKlMnOp", "owner-secret-id-1", "a tiny dragon wearing a red hat", "tok-12345678", "fd00::1",
	} {
		if strings.Contains(text, secret) {
			t.Errorf("%q survived: %s", secret, text)
		}
	}
	for _, kept := range []string{
		"req_ab12cd34ef567890", `"status":402`, "insufficient_credits", "task_AbCdEfGhIjKlMnOp", "meshy/meshy-7/text-to-3d", "任务完成", `"elapsed":"1.5s"`,
		`"owner_id":"id-1"`, `"username":"id-2"`, `"prompt":"[redacted]"`, `"authorization":"[redacted]"`, `"session_id":"[redacted]"`,
	} {
		if !strings.Contains(text, kept) {
			t.Errorf("%q lost: %s", kept, text)
		}
	}
	for number, line := range bytes.Split(bytes.TrimSpace(result.data), []byte("\n")) {
		if findings := privacyscan.ScanJSON(fmt.Sprintf("line%d", number), line); len(findings) != 0 {
			t.Errorf("line %d trips the scan: %v", number, findings)
		}
		var decoded map[string]any
		if err := json.Unmarshal(line, &decoded); err != nil || decoded["time"] == nil || decoded["level"] == nil || decoded["msg"] == nil {
			t.Errorf("line %d lost its shape: %v %s", number, err, line)
		}
	}
	// time, level and message come first, in that order.
	if !strings.HasPrefix(text, `{"time":"2026-11-16T01:00:00Z","level":"ERROR","msg":"HTTP request",`) {
		t.Errorf("unexpected line start: %.120s", text)
	}
}

func TestLogLinesOutsideTheRangeAreLeftOut(t *testing.T) {
	directory := t.TempDir()
	line := func(at time.Time, id string) string { return logLine(at, "INFO", "event", "request_id", id) }
	writeLogFile(t, directory, "art-ai-20261115T200000.000000000Z.log", // closed before the range began: never read
		line(rangeSince.Add(time.Hour), "req_0000000000000001"))
	writeLogFile(t, directory, "art-ai-20261116T120000.000000000Z.log",
		line(rangeSince.Add(-time.Second), "req_0000000000000002"), line(rangeSince, "req_0000000000000003"), line(rangeSince.Add(11*time.Hour), "req_0000000000000004"))
	writeLogFile(t, directory, logging.FileName,
		line(rangeSince.Add(13*time.Hour), "req_0000000000000005"), line(rangeUntil, "req_0000000000000006"), line(rangeUntil.Add(time.Nanosecond), "req_0000000000000007"))
	result := collect(t, directory, 1<<20, NewScrubber())
	var got []string
	for _, id := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		if strings.Contains(string(result.data), "req_000000000000000"+id) {
			got = append(got, id)
		}
	}
	if strings.Join(got, "") != "3456" || result.entries != 4 || result.available != 4 {
		t.Fatalf("kept %v (entries %d, available %d), want 3 4 5 6 in time order\n%s", got, result.entries, result.available, result.data)
	}
	if strings.Index(string(result.data), "0003") > strings.Index(string(result.data), "0006") {
		t.Fatal("lines are not in time order")
	}
}

// A line that cannot be made safe is dropped and counted, never shipped.
func TestLogLinesThatCannotBeShippedAreDroppedAndCounted(t *testing.T) {
	directory := t.TempDir()
	at := rangeSince.Add(time.Hour)
	huge := strings.Repeat("x", maxLogLineBytes+10)
	writeLogFile(t, directory, logging.FileName,
		logLine(at, "INFO", "kept"),
		`not json at all`,
		`{"level":"INFO","msg":"no time"}`,
		fmt.Sprintf(`{"time":%q,"level":"LOUD","msg":"unknown level"}`, at.Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"time":%q,"level":"INFO","msg":"%s"}`, at.Format(time.RFC3339Nano), huge),
		// An address written percent-encoded is still an address to the scan.
		logLine(at, "INFO", "contact alice%40corp-mail.example for help"),
		logLine(at, "INFO", "kept again"),
		fmt.Sprintf(`{"time":%q,"level":"INFO","msg":"half a line`, at.Format(time.RFC3339Nano)),
	)
	result := collect(t, directory, 1<<20, NewScrubber())
	if result.entries != 2 || !strings.Contains(string(result.data), "kept again") || strings.Contains(string(result.data), "corp-mail") {
		t.Fatalf("result = %+v\n%s", result, result.data)
	}
	want := map[string]int{"unparsable": 4, "too_long": 1, "email": 1}
	for reason, count := range want {
		if result.dropped[reason] != count {
			t.Errorf("dropped[%s] = %d, want %d (all: %v)", reason, result.dropped[reason], count, result.dropped)
		}
	}
}

func TestLogLinesBeyondTheBudgetKeepTheNewest(t *testing.T) {
	directory := t.TempDir()
	var lines []string
	for i := range 2000 {
		lines = append(lines, logLine(rangeSince.Add(time.Duration(i)*time.Second), "DEBUG", fmt.Sprintf("step %04d of a long running flow", i), "request_id", "req_ab12cd34ef567890"))
	}
	writeLogFile(t, directory, logging.FileName, lines...)
	const budget = 40 << 10
	result := collect(t, directory, budget, NewScrubber())
	if !result.truncated || result.available != 2000 || result.entries >= 2000 || result.entries < 100 {
		t.Fatalf("entries=%d available=%d truncated=%v", result.entries, result.available, result.truncated)
	}
	if int64(len(result.data)) > budget {
		t.Fatalf("%d bytes exceed the budget %d", len(result.data), budget)
	}
	text := string(result.data)
	if !strings.Contains(text, "step 1999 ") || strings.Contains(text, "step 0000 ") {
		t.Fatal("the newest lines are kept and the oldest are the ones left out")
	}
	if bytes.Count(result.data, []byte("\n")) != result.entries {
		t.Fatal("one line per entry")
	}
}

func TestLogSourceThatIsMissingIsReportedWithAFixedReason(t *testing.T) {
	for name, tc := range map[string]struct {
		directory, reason string
		status            string
	}{
		"not configured": {"", ReasonNotConfigured, StatusUnavailable},
		"missing":        {filepath.Join(t.TempDir(), "missing"), ReasonNotFound, StatusUnavailable},
		"empty":          {t.TempDir(), "", StatusEmpty},
	} {
		result, err := collectLogs(context.Background(), Sources{LogDirectory: tc.directory}, rangeSince, rangeUntil, 1<<20, NewScrubber())
		if err != nil || result.status != tc.status || result.reason != tc.reason || result.entries != 0 {
			t.Errorf("%s: %+v, %v", name, result, err)
		}
	}
	// A file that is not a regular file is not read.
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, logging.FileName), 0o750); err != nil {
		t.Fatal(err)
	}
	if result := collect(t, directory, 1<<20, NewScrubber()); result.entries != 0 || result.status != StatusEmpty {
		t.Fatalf("a directory named like the log: %+v", result)
	}
}

// An identity that appears in free text before the line that names it under its
// key is replaced too: identities are collected before any line is rewritten.
func TestLogIdentitiesAreCollectedBeforeTheTextIsScrubbed(t *testing.T) {
	directory := t.TempDir()
	at := rangeSince.Add(time.Hour)
	writeLogFile(t, directory, logging.FileName,
		logLine(at, "WARN", "quota exceeded for carol-longname-7", "error", "owner carol-longname-7 has no credit"),
		logLine(at.Add(time.Second), "INFO", "HTTP request", "owner_id", "carol-longname-7"),
	)
	result := collect(t, directory, 1<<20, NewScrubber())
	if strings.Contains(string(result.data), "carol") {
		t.Fatalf("the identity survived: %s", result.data)
	}
	if got := strings.Count(string(result.data), "id-1"); got != 3 {
		t.Fatalf("one identity, one placeholder, in all 3 places; found %d: %s", got, result.data)
	}
}

func TestLineTimeReadsTheFastPathAndTheGeneralPath(t *testing.T) {
	want := time.Date(2026, 11, 16, 1, 2, 3, 456_000_000, time.UTC)
	for name, line := range map[string]string{
		"prefix":      `{"time":"2026-11-16T01:02:03.456Z","level":"INFO"}`,
		"offset":      `{"time":"2026-11-16T09:02:03.456+08:00","level":"INFO"}`,
		"reordered":   `{"level":"INFO","time":"2026-11-16T01:02:03.456Z"}`,
		"escaped":     `{"time":"2026-11-16T01:02:03.456Z"}`,
		"nanoseconds": `{"time":"2026-11-16T01:02:03.456000000Z"}`,
	} {
		if got, ok := lineTime([]byte(line)); !ok || !got.Equal(want) {
			t.Errorf("%s: %v %v", name, got, ok)
		}
	}
	for _, line := range []string{`{"level":"INFO"}`, `{"time":5}`, `{"time":"yesterday"}`, `[]`, `x`, `{"time":"2026-11-16T01:02:03`} {
		if _, ok := lineTime([]byte(line)); ok {
			t.Errorf("accepted %q", line)
		}
	}
}
