package logging

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLevelDefaultsToDebugAndRejectsUnknown(t *testing.T) {
	for value, want := range map[string]slog.Level{"": slog.LevelDebug, "debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(value); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", value, got, err)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("unknown level accepted")
	}
}

func TestLoggerRedactsCredentialsCookiesSignedURLsAndPasswords(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, slog.LevelDebug)
	logger.Debug("upstream call",
		"api_key", "sk-live-1234567890abcdef",
		"authorization", "Bearer abcdefghijklmnop",
		"cookie", "art_ai_formal_session=secretvalue",
		"password", "hunter2hunter2",
		"session_id", "session_keep_me",
		"url", "https://assets.example.test/obj.glb?X-Amz-Signature=deadbeef&X-Amz-Credential=abc",
		"error", errors.New(`call https://api.meshy.ai/v2/x?key=abc failed: Authorization: Bearer abcdefghij123 token=xyz789long password="p@ss" msy-ABCDEFGHIJKLMNOP`),
	)
	line := out.String()
	for _, secret := range []string{"sk-live-1234567890abcdef", "abcdefghijklmnop", "secretvalue", "hunter2hunter2", "deadbeef", "X-Amz-Credential", "key=abc", "abcdefghij123", "xyz789long", "p@ss", "msy-ABCDEFGHIJKLMNOP"} {
		if strings.Contains(line, secret) {
			t.Errorf("log leaked %q: %s", secret, line)
		}
	}
	for _, kept := range []string{"session_keep_me", "https://assets.example.test/obj.glb", "https://api.meshy.ai/v2/x", "upstream call"} {
		if !strings.Contains(line, kept) {
			t.Errorf("log lost %q: %s", kept, line)
		}
	}
}

func TestLevelFiltersDebugWhenRaised(t *testing.T) {
	var out bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	New(&out, level).Debug("hidden")
	if out.Len() != 0 {
		t.Fatalf("debug written at info level: %s", out.String())
	}
	level.Set(slog.LevelDebug)
	New(&out, level).Debug("shown")
	if !strings.Contains(out.String(), "shown") {
		t.Fatal("debug not written at debug level")
	}
}

func TestRotatingFileRotatesAndPrunesBySizeCountAndAge(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	stale := filepath.Join(directory, "art-ai-20260901T000000.000000000Z.log")
	if err := os.WriteFile(stale, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, now.AddDate(0, 0, -40), now.AddDate(0, 0, -40)); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRotatingFile(RotateConfig{Directory: directory, FileName: "art-ai.log", MaxBytes: 32, MaxFiles: 2, RetentionDays: 30, Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("file older than retention was kept")
	}
	for range 10 {
		if _, err := writer.Write([]byte("0123456789abcdef0123\n")); err != nil {
			t.Fatal(err)
		}
	}
	rotated, _ := filepath.Glob(filepath.Join(directory, "art-ai-*.log"))
	if len(rotated) != 2 {
		t.Fatalf("rotated files = %v, want 2 kept", rotated)
	}
	info, err := os.Stat(filepath.Join(directory, "art-ai.log"))
	if err != nil || info.Size() > 32 {
		t.Fatalf("active file %v %v", info, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestTeeKeepsStdoutWhenTheFileFails(t *testing.T) {
	var primary, report bytes.Buffer
	tee := &Tee{Primary: &primary, Secondary: failingWriter{}, Report: &report}
	for range 2 {
		if _, err := tee.Write([]byte("line\n")); err != nil {
			t.Fatal(err)
		}
	}
	if primary.String() != "line\nline\n" || strings.Count(report.String(), "disk full") != 1 {
		t.Fatalf("primary=%q report=%q", primary.String(), report.String())
	}
}

type detailError struct {
	detail string
	cause  error
}

func (e *detailError) Error() string { return e.detail }
func (e *detailError) Unwrap() error { return e.cause }

func TestErrChainAppendsCausesHiddenByADetailOnce(t *testing.T) {
	// #274: the public detail alone hid the size limit that rejected the upload.
	hidden := &detailError{detail: "上传文件为空、过大或不可读取", cause: errors.New("资产超过大小上限 10485760 字节")}
	if got, want := ErrChain(hidden), "上传文件为空、过大或不可读取: 资产超过大小上限 10485760 字节"; got != want {
		t.Fatalf("ErrChain = %q, want %q", got, want)
	}
	wrapped := fmt.Errorf("写入资产内容: %w", errors.New("disk full"))
	if got, want := ErrChain(wrapped), "写入资产内容: disk full"; got != want {
		t.Fatalf("ErrChain = %q, want %q (a %%w cause is already in the text)", got, want)
	}
	joined := &detailError{detail: "失败", cause: errors.Join(errors.New("a"), errors.New("token=abcdefgh123"))}
	if got := ErrChain(joined); !strings.HasPrefix(got, "失败: a") || !strings.Contains(got, "token=[redacted]") || strings.Contains(got, "abcdefgh123") {
		t.Fatalf("ErrChain = %q, want the joined causes appended and redacted", got)
	}
}
