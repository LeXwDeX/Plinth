package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RotateConfig bounds the persistent log directory of one stack.
type RotateConfig struct {
	Directory     string
	FileName      string
	MaxBytes      int64
	MaxFiles      int
	RetentionDays int
	Now           func() time.Time
}

// RotatingFile appends JSON lines to Directory/FileName, renames it with a
// UTC timestamp once it reaches MaxBytes, and deletes rotated files older than
// RetentionDays or beyond MaxFiles. A write error never blocks the caller's
// other outputs; see Tee.
type RotatingFile struct {
	config RotateConfig
	mu     sync.Mutex
	file   *os.File
	size   int64
}

func OpenRotatingFile(config RotateConfig) (*RotatingFile, error) {
	if config.Directory == "" || config.FileName == "" || strings.ContainsAny(config.FileName, `/\`) ||
		config.MaxBytes <= 0 || config.MaxFiles <= 0 || config.RetentionDays <= 0 {
		return nil, fmt.Errorf("log rotation config is incomplete")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(config.Directory, 0o750); err != nil {
		return nil, err
	}
	writer := &RotatingFile{config: config}
	if err := writer.open(); err != nil {
		return nil, err
	}
	writer.prune()
	return writer, nil
}

func (r *RotatingFile) path() string { return filepath.Join(r.config.Directory, r.config.FileName) }

func (r *RotatingFile) open() error {
	file, err := os.OpenFile(r.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	r.file, r.size = file, info.Size()
	return nil
}

func (r *RotatingFile) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.size > 0 && r.size+int64(len(data)) > r.config.MaxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(data)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	r.file = nil
	stamp := r.config.Now().UTC().Format("20060102T150405.000000000Z")
	extension := filepath.Ext(r.config.FileName)
	base := strings.TrimSuffix(r.config.FileName, extension)
	if err := os.Rename(r.path(), filepath.Join(r.config.Directory, base+"-"+stamp+extension)); err != nil {
		return err
	}
	r.prune()
	return r.open()
}

// prune keeps at most MaxFiles rotated files and none older than RetentionDays.
func (r *RotatingFile) prune() {
	extension := filepath.Ext(r.config.FileName)
	base := strings.TrimSuffix(r.config.FileName, extension)
	matches, err := filepath.Glob(filepath.Join(r.config.Directory, base+"-*"+extension))
	if err != nil {
		return
	}
	sort.Strings(matches) // UTC timestamps sort chronologically
	cutoff := r.config.Now().Add(-time.Duration(r.config.RetentionDays) * 24 * time.Hour)
	keep := len(matches)
	for _, name := range matches {
		info, statErr := os.Stat(name)
		tooMany := keep > r.config.MaxFiles
		if statErr == nil && (tooMany || info.ModTime().Before(cutoff)) {
			if os.Remove(name) == nil {
				keep--
			}
		}
	}
}

func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// Tee writes to the primary output (stdout) and, best effort, to the file. A
// failing file never drops stdout; the first failure is reported once.
type Tee struct {
	Primary   io.Writer
	Secondary io.Writer
	Report    io.Writer
	once      sync.Once
}

func (t *Tee) Write(data []byte) (int, error) {
	if t.Secondary != nil {
		if _, err := t.Secondary.Write(data); err != nil && t.Report != nil {
			t.once.Do(func() { _, _ = fmt.Fprintf(t.Report, "log file write failed: %v\n", err) })
		}
	}
	return t.Primary.Write(data)
}
