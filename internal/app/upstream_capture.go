package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

// Opt-in capture of upstream calls (#233). When the operator sets the
// directory setting, every Provider and chat model connection's HTTP transport
// is wrapped by wirecapture's recorder, which writes one sanitized record per
// call (structure only: field names, types, status codes, error codes, status
// words) to that directory and rotates old files by count and total size.
// Unset, nothing is wrapped and nothing is written.

// Bounds of the retention settings. The Schema declares the defaults.
const (
	upstreamCaptureMaxRecordsLimit   = 100000
	upstreamCaptureMaxMegabytesLimit = 4096
)

// UpstreamCaptureConfig is the application's recorder wiring: the zero value
// (no directory) is off.
type UpstreamCaptureConfig struct {
	Directory  string
	MaxRecords int
	MaxBytes   int64
}

// Enabled reports whether recording is switched on.
func (c UpstreamCaptureConfig) Enabled() bool { return c.Directory != "" }

// upstreamCaptureConfig reads the three settings. The limits are only read
// (and only validated) when a directory is set: a stack that does not record
// ignores them.
func upstreamCaptureConfig(values map[string]string) (UpstreamCaptureConfig, error) {
	directory := strings.TrimSpace(values["upstream_capture_directory"])
	if directory == "" {
		return UpstreamCaptureConfig{}, nil
	}
	if !filepath.IsAbs(directory) {
		return UpstreamCaptureConfig{}, errors.New("运行设置 upstream_capture_directory 必须是绝对路径")
	}
	bounded := func(name string, maximum int) (int, error) {
		raw := strings.TrimSpace(values[name])
		number, err := strconv.Atoi(raw)
		if err != nil || number < 1 || number > maximum {
			return 0, fmt.Errorf("运行设置 %s 必须是 1 到 %d 的整数: %q", name, maximum, raw)
		}
		return number, nil
	}
	records, err := bounded("upstream_capture_max_records", upstreamCaptureMaxRecordsLimit)
	if err != nil {
		return UpstreamCaptureConfig{}, err
	}
	megabytes, err := bounded("upstream_capture_max_megabytes", upstreamCaptureMaxMegabytesLimit)
	if err != nil {
		return UpstreamCaptureConfig{}, err
	}
	return UpstreamCaptureConfig{Directory: filepath.Clean(directory), MaxRecords: records, MaxBytes: int64(megabytes) << 20}, nil
}

// newUpstreamCapture builds the recorder, or nil when capture is off. It
// refuses a directory that overlaps a tree the application serves or manages:
// records must never become reachable through the web root or be swept up by
// asset maintenance. Any failure stops the start-up, so a stack never looks
// like it records while it does not.
func newUpstreamCapture(config Config, schema catalog.ApplicationSchema, logger *slog.Logger) (*wirecapture.UpstreamRecorder, error) {
	capture := config.UpstreamCapture
	if !capture.Enabled() {
		return nil, nil
	}
	if !filepath.IsAbs(capture.Directory) {
		return nil, errors.New("运行设置 upstream_capture_directory 必须是绝对路径")
	}
	if err := upstreamCaptureOverlap(capture.Directory, map[string]string{
		"storage_root":                 config.StorageRoot,
		"web_assets_root":              config.WebAssetsRoot,
		"legacy_generated_images_root": config.LegacyGeneratedImagesRoot,
		"log_directory":                config.Logging.Directory,
	}); err != nil {
		return nil, err
	}
	revision := runtimeRevision()
	if revision == unknownRevision {
		// An unstamped (local) build still records; the label says so.
		revision = "unstamped"
	}
	environment := strings.TrimSpace(config.Environment)
	if environment == "" {
		environment = "development"
	}
	recorder, err := wirecapture.NewRuntimeRecorder(wirecapture.RuntimeConfig{
		Dir: capture.Directory,
		Defaults: wirecapture.Metadata{
			Environment: environment, AppRevision: revision, CatalogRevision: identifierLabel(schema.Revision, "unversioned"),
		},
		MaxRecords: capture.MaxRecords,
		MaxBytes:   capture.MaxBytes,
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("上游调用脱敏录制无法开启: %w", err)
	}
	return recorder, nil
}

// logUpstreamCapture writes the capture state to the start-up log: INFO when it
// is on (with the directory and limits), DEBUG when it is off.
func logUpstreamCapture(logger *slog.Logger, recorder *wirecapture.UpstreamRecorder) {
	if recorder == nil {
		logger.Debug("上游调用脱敏录制未开启", "enabled", false)
		return
	}
	status := recorder.Status()
	logger.Info("上游调用脱敏录制已开启：只保留结构（字段名、类型、状态码、错误码、状态枚举），不保存密钥、提示词、媒体与用户标识",
		"enabled", true,
		"directory", status.Directory,
		"max_records", status.MaxRecords,
		"max_megabytes", status.MaxBytes>>20,
		"existing_records", status.Records,
	)
}

// identifierLabel makes a free-text revision usable as a record label: letters,
// digits, dots, underscores and hyphens only, starting alphanumeric, at most 128
// characters. An external Catalog may name its revision anything; recording
// must not refuse to start over that.
func identifierLabel(text, fallback string) string {
	var label []byte
	for _, r := range strings.TrimSpace(text) {
		if len(label) >= 128 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			label = append(label, byte(r))
		case len(label) == 0:
			// Skip until the first letter or digit.
		case r == '.' || r == '_' || r == '-':
			label = append(label, byte(r))
		case label[len(label)-1] != '-':
			label = append(label, '-')
		}
	}
	if len(label) == 0 || string(label) == "unknown" {
		return fallback
	}
	return string(label)
}

// upstreamCaptureOverlap refuses a record directory that is, contains or lies
// inside one of the named application trees. Symbolic links are resolved where
// the path exists.
func upstreamCaptureOverlap(directory string, trees map[string]string) error {
	resolved := resolveExisting(directory)
	for name, tree := range trees {
		tree = strings.TrimSpace(tree)
		if tree == "" {
			continue
		}
		other := resolveExisting(tree)
		if wirecapture.PathInside(other, resolved) || wirecapture.PathInside(resolved, other) {
			return fmt.Errorf("运行设置 upstream_capture_directory 不能与 %s 重叠", name)
		}
	}
	return nil
}

// resolveExisting returns the absolute, symlink-resolved form of path, or its
// cleaned absolute form when it does not exist (yet).
func resolveExisting(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	// Resolve the deepest existing ancestor, then append the missing rest.
	rest := ""
	for current := absolute; ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

// withUpstreamCapture composes the recorder onto a connection transport
// decorator. A nil recorder returns the decorator untouched, so capture off
// wraps nothing. The recorder wraps the outside: it sees each request as the
// Adapter made it, whatever routing the inner decorator applies. An inner
// decorator that returns nil keeps its meaning (the connection is refused).
func withUpstreamCapture(
	recorder *wirecapture.UpstreamRecorder,
	decorate func(connectionID string, base http.RoundTripper) http.RoundTripper,
) func(connectionID string, base http.RoundTripper) http.RoundTripper {
	if recorder == nil {
		return decorate
	}
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		inner := base
		if decorate != nil {
			if inner = decorate(connectionID, base); inner == nil {
				return nil
			}
		}
		return recorder.Wrap(connectionID, inner)
	}
}
