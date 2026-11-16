package diagbundle

import (
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

const (
	// ManifestFormat is the version of manifest.json. A reader that does not
	// know it should say so instead of guessing.
	ManifestFormat = 1

	// DefaultMaxBytes, MinMaxBytes and MaxMaxBytes bound the uncompressed size
	// of everything a bundle holds (the file is smaller: it is gzip-compressed).
	DefaultMaxBytes = 32 << 20
	MinMaxBytes     = 64 << 10
	MaxMaxBytes     = 1 << 30

	// DefaultSince is the time range of a bundle when none is asked for, in the
	// syntax of ParseSince; DefaultMaxBytesText is DefaultMaxBytes in the syntax
	// of ParseByteSize. They are the defaults of the command's flags.
	DefaultSince        = "24h"
	DefaultMaxBytesText = "32m"

	// failedTaskLimit bounds how many failed tasks one bundle summarizes.
	failedTaskLimit = 200
	// excerptRunes bounds the upstream error excerpt kept per failed task (the
	// kernel stores up to 400).
	excerptRunes = 240
	// manifestReserve is set aside for manifest.json, which is written last.
	manifestReserve = 16 << 10
	// maxCaptureFileBytes skips a record file larger than any the recorder
	// writes (its body limit is 1 MiB per body).
	maxCaptureFileBytes = 4 << 20
	// maxLogLineBytes drops a log line longer than any the service writes.
	maxLogLineBytes = 1 << 20
	// maxWarnings bounds the warnings a manifest carries.
	maxWarnings = 32
)

// Category names, as they appear in the manifest.
const (
	CategoryRuntime  = "runtime"
	CategorySettings = "settings"
	CategoryFailures = "failures"
	CategoryLogs     = "logs"
	CategoryCaptures = "captures"
)

// Status of a category in the manifest.
const (
	StatusIncluded    = "included"
	StatusEmpty       = "empty"
	StatusUnavailable = "unavailable"
)

// Reasons a category is unavailable. They are fixed words: a reason never
// carries a path, an error message or any value from the stack.
const (
	ReasonNotConfigured = "not_configured"
	ReasonNotFound      = "not_found"
	ReasonUnreadable    = "unreadable"
)

// Setting is one runtime setting the Catalog declares: its name, the
// environment variables that can supply it and whether its value is a secret.
type Setting struct {
	Name        string
	Environment []EnvironmentName
	Secret      bool
}

// EnvironmentName is one environment variable a setting reads. Deprecated marks
// a historical alias.
type EnvironmentName struct {
	Name       string
	Deprecated bool
}

// Sources says what the stack is and where it keeps what the bundle collects.
// An empty path means the stack has none configured. The export reads from
// these and writes nothing to them.
type Sources struct {
	// Revision is the commit linked into the binary, "unknown" for a build
	// without one. CatalogRevision is the Catalog's own revision label and
	// CatalogSHA256 the digest of the Catalog document in use. Environment is
	// the stack's APP_ENV label.
	Revision        string
	CatalogRevision string
	CatalogSHA256   string
	Environment     string

	// LogDirectory holds the rotating log files written under LogFileName
	// (internal/logging); CaptureDirectory the upstream capture records
	// (internal/wirecapture); DatabasePath the SQLite file.
	LogDirectory     string
	LogFileName      string
	CaptureDirectory string
	DatabasePath     string

	// Settings and RetiredEnvironment are the Catalog's declarations; Lookup
	// reads the process environment. Only whether a variable is set is ever
	// taken from it.
	Settings           []Setting
	RetiredEnvironment []string
	Lookup             func(string) string

	// Warnings are notes about sources the caller could not resolve. They
	// must be fixed words without values, like the reasons above.
	Warnings []string
}

// Options bounds one export.
type Options struct {
	// Since is the start of the time range; it ends when the export starts.
	Since time.Time
	// MaxBytes bounds the uncompressed size of all members.
	MaxBytes int64
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Member is one file of the bundle.
type Member struct {
	// Path is relative to the bundle's top-level directory.
	Path     string
	Category string
	Data     []byte
}

// Manifest is manifest.json: what the bundle holds and under which limits.
type Manifest struct {
	Format          int              `json:"format"`
	GeneratedAt     string           `json:"generated_at"`
	Range           TimeRange        `json:"range"`
	MaxBytes        int64            `json:"max_bytes"`
	Revision        string           `json:"revision"`
	CatalogRevision string           `json:"catalog_revision"`
	CatalogSHA256   string           `json:"catalog_sha256"`
	Categories      []CategoryReport `json:"categories"`
	Privacy         PrivacyReport    `json:"privacy"`
	Warnings        []string         `json:"warnings,omitempty"`
}

// TimeRange is the span the time-bound categories were read for.
type TimeRange struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// CategoryReport describes one category of a bundle.
type CategoryReport struct {
	Category string `json:"category"`
	Status   string `json:"status"`
	// Reason is why a category is unavailable.
	Reason string `json:"reason,omitempty"`
	// Path is the member (or, for captures, the directory) in the bundle.
	Path string `json:"path,omitempty"`
	// Entries is how many entries the bundle holds; Available how many the
	// stack had in the time range; Truncated says the size limit cut the
	// difference (the oldest were left out).
	Entries   int   `json:"entries"`
	Available int   `json:"available"`
	Truncated bool  `json:"truncated"`
	Bytes     int64 `json:"bytes"`
	// SHA256 is the digest of the member of a single-file category.
	SHA256 string `json:"sha256,omitempty"`
	// Dropped counts what was left out because it could not be shipped safely,
	// by reason: a log line that could not be parsed or that still tripped a
	// privacy rule after scrubbing, a capture file that could not be read.
	Dropped map[string]int `json:"dropped,omitempty"`
}

// PrivacyReport records the final scan of the members as they were written.
type PrivacyReport struct {
	Scanner      string `json:"scanner"`
	FilesScanned int    `json:"files_scanned"`
	Findings     int    `json:"findings"`
}

// RefusedError reports that the export stopped because a member of the bundle
// did not pass the privacy scan. It names where and which rule, never a value.
type RefusedError struct {
	Findings []privacyscan.Finding
}

func (e *RefusedError) Error() string {
	const shown = 20
	var lines []string
	for i, finding := range e.Findings {
		if i == shown {
			lines = append(lines, fmt.Sprintf("… 另有 %d 处", len(e.Findings)-shown))
			break
		}
		lines = append(lines, "  "+finding.String())
	}
	return fmt.Sprintf("隐私扫描命中 %d 处，诊断包未写出：\n%s", len(e.Findings), strings.Join(lines, "\n"))
}

// formatStamp renders an instant as the compact UTC stamp used in names.
func formatStamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

func formatTimestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// formatSecond is the precision of the times a reader of the manifest needs.
func formatSecond(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
