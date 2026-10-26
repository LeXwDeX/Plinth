package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"golang.org/x/sys/unix"
)

// RetentionFiles holds a root directory descriptor and the same exclusive
// maintenance lock used by Store. Every component is opened with NOFOLLOW.
type RetentionFiles struct {
	root          *os.File
	lock          *os.File
	Path          string
	Device, Inode uint64
}

func retentionOpen(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("retention path must be an absolute clean path")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		if directory {
			return os.NewFile(uintptr(fd), path), nil
		}
		unix.Close(fd)
		return nil, errors.New("file path is a directory")
	}
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("unsafe or inaccessible retention path component: %w", err)
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		f.Close()
		return nil, e
	}
	if !directory && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
		f.Close()
		return nil, errors.New("retention requires a regular file with one link")
	}
	return f, nil
}

func RetentionPathIdentity(path string, directory bool) (uint64, uint64, error) {
	f, e := retentionOpen(path, directory)
	if e != nil {
		return 0, 0, e
	}
	defer f.Close()
	var st unix.Stat_t
	e = unix.Fstat(int(f.Fd()), &st)
	return uint64(st.Dev), st.Ino, e
}

func OpenRetentionFiles(root string) (*RetentionFiles, error) {
	f, e := retentionOpen(root, true)
	if e != nil {
		return nil, e
	}
	fd, e := unix.Openat(int(f.Fd()), ".maintenance.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		f.Close()
		return nil, e
	}
	lock := os.NewFile(uintptr(fd), ".maintenance.lock")
	fail := func(err error) (*RetentionFiles, error) { lock.Close(); f.Close(); return nil, err }
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return fail(e)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fail(errors.New("unsafe maintenance lock"))
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fail(ErrStorageInUse)
	}
	if e = unix.Fstat(int(f.Fd()), &st); e != nil {
		return fail(e)
	}
	return &RetentionFiles{root: f, lock: lock, Path: root, Device: uint64(st.Dev), Inode: st.Ino}, nil
}
func (s *RetentionFiles) Close() error { return errors.Join(s.lock.Close(), s.root.Close()) }

func (s *RetentionFiles) checkRoot() error {
	d, i, e := RetentionPathIdentity(s.Path, true)
	if e != nil {
		return e
	}
	if d != s.Device || i != s.Inode {
		return generation.ErrRetentionChanged
	}
	return nil
}

func (s *RetentionFiles) parent(key string) (*os.File, string, error) {
	if !validBlobKey(key) || strings.Contains(key, "\\") || filepath.Clean(key) != key || strings.Contains(key, "..") {
		return nil, "", errors.New("invalid retention CAS key")
	}
	if e := s.checkRoot(); e != nil {
		return nil, "", e
	}
	parts := strings.Split(key, "/")
	fd, e := unix.Dup(int(s.root.Fd()))
	if e != nil {
		return nil, "", e
	}
	for _, p := range parts[:len(parts)-1] {
		n, err := unix.Openat(fd, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, "", err
		}
		fd = n
	}
	return os.NewFile(uintptr(fd), key), parts[len(parts)-1], nil
}
func (s *RetentionFiles) open(key string) (*os.File, error) {
	p, name, e := s.parent(key)
	if e != nil {
		return nil, e
	}
	defer p.Close()
	fd, e := unix.Openat(int(p.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), key)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		f.Close()
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("retention CAS entry is not a single-link regular file")
	}
	return f, nil
}
func inspectRetentionFile(ctx context.Context, f *os.File, key string) (generation.RetentionFile, error) {
	var a, b unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &a); e != nil {
		return generation.RetentionFile{}, e
	}
	h := sha256.New()
	buf := make([]byte, 128*1024)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return generation.RetentionFile{}, e
		}
		n, e := f.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > a.Size {
				return generation.RetentionFile{}, generation.ErrRetentionChanged
			}
			h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return generation.RetentionFile{}, e
		}
	}
	if e := unix.Fstat(int(f.Fd()), &b); e != nil {
		return generation.RetentionFile{}, e
	}
	if total != a.Size || a.Dev != b.Dev || a.Ino != b.Ino || a.Size != b.Size || a.Mtim != b.Mtim || b.Nlink != 1 {
		return generation.RetentionFile{}, generation.ErrRetentionChanged
	}
	return generation.RetentionFile{Key: key, Size: a.Size, SHA256: hex.EncodeToString(h.Sum(nil)), Device: uint64(a.Dev), Inode: a.Ino}, nil
}
func (s *RetentionFiles) Inspect(ctx context.Context, want generation.RetentionFile) (generation.RetentionFile, error) {
	f, e := s.open(want.Key)
	if e != nil {
		return generation.RetentionFile{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return generation.RetentionFile{}, e
	}
	if info.Size() != want.Size {
		return generation.RetentionFile{}, generation.ErrRetentionChanged
	}
	got, e := inspectRetentionFile(ctx, f, want.Key)
	if e != nil {
		return got, e
	}
	if got.Size != want.Size || got.SHA256 != want.SHA256 || (want.Inode != 0 && (got.Inode != want.Inode || got.Device != want.Device)) {
		return got, generation.ErrRetentionChanged
	}
	return got, nil
}

// CreateRetentionDirectory never reuses an existing backup/output directory.
// Paths may not overlap source data or alias it through symlinks/hardlinks.
func CreateRetentionDirectory(path string, protected ...string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("backup directory requires an absolute clean path")
	}
	for _, p := range protected {
		if path == p || strings.HasPrefix(path, p+string(os.PathSeparator)) || strings.HasPrefix(p, path+string(os.PathSeparator)) {
			return errors.New("retention output overlaps source data")
		}
	}
	parent, e := retentionOpen(filepath.Dir(path), true)
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = unix.Mkdirat(int(parent.Fd()), filepath.Base(path), 0700); e != nil {
		return e
	}
	return parent.Sync()
}

func (s *RetentionFiles) Backup(ctx context.Context, files []generation.RetentionFile, directory string) error {
	// directory is newly created by the caller; copies are independent inodes.
	for _, want := range files {
		if _, e := s.Inspect(ctx, want); e != nil {
			return e
		}
		src, e := s.open(want.Key)
		if e != nil {
			return e
		}
		dest := filepath.Join(directory, filepath.FromSlash(want.Key))
		parts := strings.Split(want.Key, "/")
		root, e := retentionOpen(directory, true)
		if e != nil {
			src.Close()
			return e
		}
		fd := int(root.Fd())
		for _, part := range parts[:len(parts)-1] {
			if e = unix.Mkdirat(fd, part, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
				break
			}
			if e = unix.Fsync(fd); e != nil {
				break
			}
			next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				e = err
				break
			}
			if fd != int(root.Fd()) {
				unix.Close(fd)
			}
			fd = next
			e = nil
		}
		if e != nil {
			if fd != int(root.Fd()) {
				unix.Close(fd)
			}
			root.Close()
			src.Close()
			return e
		}
		outfd, e := unix.Openat(fd, parts[len(parts)-1], unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e == nil {
			if syncErr := unix.Fsync(fd); syncErr != nil {
				unix.Close(outfd)
				e = syncErr
			}
		}
		if fd != int(root.Fd()) {
			unix.Close(fd)
		}
		root.Close()
		if e != nil {
			src.Close()
			return e
		}
		out := os.NewFile(uintptr(outfd), dest)
		_, copyErr := io.Copy(out, src)
		syncErr := out.Sync()
		closeErr := out.Close()
		src.Close()
		if e = errors.Join(copyErr, syncErr, closeErr); e != nil {
			return e
		}
		restored, e := retentionOpen(dest, false)
		if e != nil {
			return e
		}
		got, e := inspectRetentionFile(ctx, restored, want.Key)
		restored.Close()
		if e != nil {
			return e
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 || got.Inode == want.Inode && got.Device == want.Device {
			return errors.New("independent blob backup verification failed")
		}
	}
	return nil
}

// Remove only unlinks an approved, freshly rehashed inode through its parent
// descriptor. Missing files are reported to the caller, never silently skipped.
func (s *RetentionFiles) Remove(ctx context.Context, want generation.RetentionFile) error {
	if _, e := s.Inspect(ctx, want); e != nil {
		return e
	}
	p, name, e := s.parent(want.Key)
	if e != nil {
		return e
	}
	defer p.Close()
	var st unix.Stat_t
	if e = unix.Fstatat(int(p.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || uint64(st.Dev) != want.Device || st.Ino != want.Inode || st.Size != want.Size {
		return generation.ErrRetentionChanged
	}
	if e = unix.Unlinkat(int(p.Fd()), name, 0); e != nil {
		return e
	}
	return p.Sync()
}

func (s *RetentionFiles) PrepareBackup(ctx context.Context, directory, database string, files []generation.RetentionFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkRoot(); err != nil {
		return err
	}
	db, err := retentionOpen(database, false)
	if err != nil {
		return err
	}
	info, err := db.Stat()
	db.Close()
	if err != nil {
		return err
	}
	required := uint64(info.Size()) * 2
	// WAL may contain pages not yet present in the main database. Its complete
	// size is a conservative bound for both independent database copies.
	wal, walErr := retentionOpen(database+"-wal", false)
	if walErr == nil {
		walInfo, statErr := wal.Stat()
		wal.Close()
		if statErr != nil {
			return statErr
		}
		required += uint64(walInfo.Size()) * 2
	} else if !errors.Is(walErr, os.ErrNotExist) {
		return walErr
	}
	for _, file := range files {
		if file.Size < 0 || uint64(file.Size) > ^uint64(0)-required {
			return errors.New("backup size overflow")
		}
		required += uint64(file.Size)
	}
	parent, err := retentionOpen(filepath.Dir(directory), true)
	if err != nil {
		return err
	}
	var capacity unix.Statfs_t
	err = unix.Fstatfs(int(parent.Fd()), &capacity)
	parent.Close()
	if err != nil {
		return err
	}
	// The source DB and a separately restored DB are both retained. A small
	// fixed overhead is an allocation guard, never a retention age/default.
	if required > uint64(capacity.Bavail)*uint64(capacity.Bsize) || uint64(capacity.Bavail)*uint64(capacity.Bsize)-required < 16<<20 {
		return errors.New("insufficient capacity for independent backup and restore verification")
	}
	return CreateRetentionDirectory(directory, s.Path, database, database+"-wal", database+"-shm", database+"-journal")
}

// WriteRetentionArtifact writes once, fsyncs both bytes and the directory, and
// removes write permissions. This is not an OS immutable flag or authorization.
func WriteRetentionArtifact(path string, data []byte) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("artifact requires absolute clean path")
	}
	parent, e := retentionOpen(filepath.Dir(path), true)
	if e != nil {
		return e
	}
	defer parent.Close()
	fd, e := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0400)
	if e != nil {
		return e
	}
	file := os.NewFile(uintptr(fd), path)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if e = errors.Join(writeErr, syncErr, closeErr); e != nil {
		return e
	}
	return parent.Sync()
}
func ReadRetentionArtifact(path string) ([]byte, error) {
	f, e := retentionOpen(path, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 64<<20))
	if e != nil {
		return nil, e
	}
	if len(b) == 64<<20 {
		return nil, errors.New("retention manifest exceeds bounded size")
	}
	return b, nil
}
func (s *RetentionFiles) WriteCleanupAttempt(ctx context.Context, directory string, approval generation.RetentionApproval) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if len(approval.Nonce) != 32 {
		return errors.New("invalid cleanup nonce")
	}
	for _, c := range approval.Nonce {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("invalid cleanup nonce")
		}
	}
	b, e := json.Marshal(approval)
	if e != nil {
		return e
	}
	return WriteRetentionArtifact(filepath.Join(directory, "cleanup-approval-"+approval.Nonce+".json"), b)
}

func SyncRetentionDirectory(path string) error {
	f, e := retentionOpen(path, true)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (s *RetentionFiles) SaveManifest(ctx context.Context, directory string, m generation.RetentionManifest) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	return WriteRetentionArtifact(filepath.Join(directory, "manifest.json"), b)
}

// VerifyBackup is required again before resuming destructive cleanup: a receipt
// proves an earlier backup existed, not that its recovery bytes still exist.
func (s *RetentionFiles) VerifyBackup(ctx context.Context, directory string, m generation.RetentionManifest, databaseHash string) error {
	if e := s.checkRoot(); e != nil {
		return e
	}
	for _, source := range []string{s.Path, m.Database, m.Database + "-wal", m.Database + "-shm", m.Database + "-journal"} {
		if directory == source || strings.HasPrefix(directory, source+"/") || strings.HasPrefix(source, directory+"/") {
			return errors.New("backup overlaps source data")
		}
	}
	canonical, e := json.Marshal(m)
	if e != nil {
		return e
	}
	stored, e := ReadRetentionArtifact(filepath.Join(directory, "manifest.json"))
	if e != nil {
		return e
	}
	if !bytes.Equal(canonical, stored) {
		return errors.New("backup manifest was modified")
	}
	dbPath := filepath.Join(directory, "database.sqlite")
	db, e := retentionOpen(dbPath, false)
	if e != nil {
		return e
	}
	info, e := db.Stat()
	if e != nil {
		db.Close()
		return e
	}
	if info.Size() >= 1<<31 {
		db.Close()
		return errors.New("backup database exceeds verification bound")
	}
	got, e := inspectRetentionFile(ctx, db, "database.sqlite")
	db.Close()
	if e != nil {
		return e
	}
	if len(databaseHash) != 64 || got.SHA256 != databaseHash || got.Device == m.DatabaseDevice && got.Inode == m.DatabaseInode {
		return errors.New("backup database identity or hash changed")
	}
	// The verified copy must be independently restorable from this database
	// file. A live/nonempty journal beside it requires a separate investigation.
	for _, suffix := range []string{"-wal", "-journal"} {
		sidecar, err := retentionOpen(dbPath+suffix, false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		st, err := sidecar.Stat()
		sidecar.Close()
		if err != nil {
			return err
		}
		if st.Size() != 0 {
			return errors.New("backup has unexpected recovery sidecar")
		}
	}
	for _, want := range m.BackupFiles {
		if !validBlobKey(want.Key) || strings.Contains(want.Key, "\\") || filepath.Clean(want.Key) != want.Key {
			return errors.New("invalid backup CAS key")
		}
		f, e := retentionOpen(filepath.Join(directory, filepath.FromSlash(want.Key)), false)
		if e != nil {
			return e
		}
		st, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			return statErr
		}
		if st.Size() != want.Size {
			f.Close()
			return generation.ErrRetentionChanged
		}
		got, e := inspectRetentionFile(ctx, f, want.Key)
		f.Close()
		if e != nil {
			return e
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 || got.Device == want.Device && got.Inode == want.Inode {
			return errors.New("independent CAS backup is missing or changed")
		}
	}
	return nil
}
