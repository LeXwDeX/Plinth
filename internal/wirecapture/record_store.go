package wirecapture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// recordSlackMinimum is, with a tenth of each limit, how far past its limits the
// store may drift while it cannot remove old files; beyond it new records are
// refused instead of filling the disk.
const recordSlackMinimum = 16

// recordPartialSuffix marks a record still being written; it never ends in
// UpstreamRecordSuffix, so no reader or limit counts it.
const recordPartialSuffix = ".tmp"

var errRecordStoreFull = errors.New("wirecapture: record directory is over its limits and old records cannot be removed")

func refuse(reason string) error {
	return fmt.Errorf("%w: %s", ErrUpstreamRecordRefused, reason)
}

func privateRecordDir(dir, worktree string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) || worktree == "" {
		return "", refuse("directory and worktree must be absolute")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", refuse("directory must exist with mode 0700")
	}
	rootReal, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", refuse("worktree unavailable")
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil || PathInside(rootReal, dirReal) {
		return "", refuse("directory is inside the worktree")
	}
	if same, err := SameFileAncestor(rootReal, dirReal); err != nil || same {
		return "", refuse("directory is inside the worktree")
	}
	return dirReal, nil
}

// prepareRuntimeDir creates the application's record directory (mode 0700),
// tightens an existing one and proves it is writable. A symbolic link or a
// non-directory is refused: records must stay where the operator put them.
func prepareRuntimeDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return "", refuse("directory must be an absolute path")
	}
	dir = filepath.Clean(dir)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", refuse("directory cannot be created")
		}
		info, err = os.Lstat(dir)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", refuse("directory must be a real directory")
	}
	if info.Mode().Perm() != 0o700 {
		// The umask may have narrowed the mode MkdirAll asked for, and an
		// existing directory may be wider than the record files deserve.
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", refuse("directory mode cannot be set to 0700")
		}
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", refuse("directory unavailable")
	}
	probe, err := os.CreateTemp(real, ".probe-*")
	if err != nil {
		return "", refuse("directory is not writable")
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		return "", refuse("directory is not writable")
	}
	return real, nil
}

type recordFile struct {
	name string
	size int64
}

// recordStore indexes the record files of one directory and keeps them within
// the retention limits. Only files with UpstreamRecordSuffix are ever counted
// or removed; names sort in time order.
type recordStore struct {
	dir        string
	maxRecords int
	maxBytes   int64

	mu        sync.Mutex
	files     []recordFile
	bytes     int64
	rotated   int64
	failures  int64
	rotateErr error
	maxSeq    int
}

func openRecordStore(dir string, maxRecords int, maxBytes int64) (*recordStore, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, refuse("directory cannot be read")
	}
	store := &recordStore{dir: dir, maxRecords: maxRecords, maxBytes: maxBytes}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), UpstreamRecordSuffix+recordPartialSuffix) {
			// Left by a crash between writing and publishing a record.
			if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), UpstreamRecordSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		store.files = append(store.files, recordFile{name: entry.Name(), size: info.Size()})
		store.bytes += info.Size()
		if sequence := recordSequence(entry.Name()); sequence > store.maxSeq {
			store.maxSeq = sequence
		}
	}
	store.enforceLocked()
	return store, nil
}

// recordSequence reads the sequence number out of "<time>-<sequence>.exchange.json".
func recordSequence(name string) int {
	stem := strings.TrimSuffix(name, UpstreamRecordSuffix)
	index := strings.LastIndexByte(stem, '-')
	if index < 0 {
		return 0
	}
	sequence, err := strconv.Atoi(stem[index+1:])
	if err != nil || sequence < 0 {
		return 0
	}
	return sequence
}

func (s *recordStore) maxSequence() int { return s.maxSeq }

func (s *recordStore) overLimitsLocked() bool {
	return (s.maxRecords > 0 && len(s.files) > s.maxRecords) || (s.maxBytes > 0 && s.bytes > s.maxBytes)
}

// fullLocked reports a store that stayed over its limits by more than the
// slack, which only happens when old files cannot be removed.
func (s *recordStore) fullLocked() bool {
	if s.maxRecords > 0 && len(s.files) >= s.maxRecords+max(recordSlackMinimum, s.maxRecords/10) {
		return true
	}
	return s.maxBytes > 0 && s.bytes >= s.maxBytes+s.maxBytes/10
}

// enforceLocked removes the oldest records until the limits hold. A removal
// that fails stops the round (and is reported once through takeRotateError);
// fullLocked then keeps the directory from growing without bound.
func (s *recordStore) enforceLocked() {
	for len(s.files) > 0 && s.overLimitsLocked() {
		oldest := s.files[0]
		if err := os.Remove(filepath.Join(s.dir, oldest.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			if s.rotateErr == nil {
				s.rotateErr = fmt.Errorf("wirecapture: remove old upstream record: %w", err)
			}
			return
		}
		s.files = s.files[1:]
		s.bytes -= oldest.size
		s.rotated++
	}
}

// create writes one record file: exclusive, no symbolic links, mode 0600. The
// name is chosen under the lock so names (and so rotation order) follow write
// order even with concurrent writers.
func (s *recordStore) create(nameOf func() string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fullLocked() {
		return errRecordStoreFull
	}
	name := nameOf()
	path := filepath.Join(s.dir, name)
	// The record appears under its final name only once complete, so a crash
	// never leaves a truncated file for the readers to trip over.
	partial := path + recordPartialSuffix
	file, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return fmt.Errorf("wirecapture: create upstream record: %w", err)
	}
	// The umask can only narrow the mode; readers insist on exactly 0600.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(partial)
		return fmt.Errorf("wirecapture: set upstream record mode: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(partial)
		return fmt.Errorf("wirecapture: write upstream record: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("wirecapture: close upstream record: %w", err)
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("wirecapture: publish upstream record: %w", err)
	}
	s.files = append(s.files, recordFile{name: name, size: int64(len(data))})
	s.bytes += int64(len(data))
	s.enforceLocked()
	return nil
}

// failed counts one failed write and returns the running total.
func (s *recordStore) failed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	return s.failures
}

// takeRotateError returns the pending rotation failure, once.
func (s *recordStore) takeRotateError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.rotateErr
	s.rotateErr = nil
	return err
}

func (s *recordStore) status() RecorderStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RecorderStatus{MaxRecords: s.maxRecords, MaxBytes: s.maxBytes, Records: len(s.files), Bytes: s.bytes,
		Rotated: s.rotated, WriteErrors: s.failures}
}

// ListUpstreamRecords returns the record file names in dir (UpstreamRecordSuffix
// only), oldest first: names start with a fixed-width UTC time, so name order is
// time order. Nothing is read.
func ListUpstreamRecords(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), UpstreamRecordSuffix) {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// ReadUpstreamRecord reads one record file: a 0600 regular file in the current
// format. Records are indented for reading; the JSON bodies of a recorded
// exchange come back compact, which is the canonical form Validate expects.
func ReadUpstreamRecord(path string) (UpstreamRecord, error) {
	name := filepath.Base(path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return UpstreamRecord{}, fmt.Errorf("%w: record %s must be a 0600 regular file", ErrUnsafePublicValue, name)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return UpstreamRecord{}, err
	}
	var record UpstreamRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || record.Version != upstreamRecordVersion {
		return UpstreamRecord{}, fmt.Errorf("%w: record %s", ErrInvalidExchange, name)
	}
	if record.Exchange == nil {
		return record, nil
	}
	exchange := *record.Exchange
	for _, body := range []*json.RawMessage{&exchange.Request.Body.JSON, &exchange.Response.Body.JSON} {
		if len(*body) == 0 {
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, *body); err != nil {
			return UpstreamRecord{}, fmt.Errorf("%w: record %s body", ErrInvalidExchange, name)
		}
		*body = compact.Bytes()
	}
	record.Exchange = &exchange
	return record, nil
}

// LoadUpstreamRecords reads every record file in dir (0600 regular files
// only) in name order and returns the recorded exchanges plus the number of
// skipped records.
func LoadUpstreamRecords(dir string) ([]Exchange, int, error) {
	names, err := ListUpstreamRecords(dir)
	if err != nil {
		return nil, 0, err
	}
	var exchanges []Exchange
	skipped := 0
	for _, name := range names {
		record, err := ReadUpstreamRecord(filepath.Join(dir, name))
		if err != nil {
			return nil, 0, err
		}
		if record.Exchange == nil {
			skipped++
			continue
		}
		exchanges = append(exchanges, *record.Exchange)
	}
	return exchanges, skipped, nil
}
