package local

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// OpenReadOnly opens an existing asset store without creating directories,
// lock files or temp files. A command dry-run uses it to verify stored bytes
// without changing the store. When the runtime's maintenance lock exists, the
// read-only process takes a shared lock so garbage collection cannot run under
// it; a missing lock is accepted because no writer has initialized this root.
func OpenReadOnly(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("资产根目录不是现有普通目录")
	}
	blobs, err := os.Lstat(filepath.Join(absolute, "blobs"))
	if err != nil {
		return nil, err
	}
	if !blobs.IsDir() || blobs.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("资产根目录缺少 blobs 目录")
	}
	var lock *os.File
	lockPath := filepath.Join(absolute, ".maintenance.lock")
	if _, err := os.Lstat(lockPath); err == nil {
		lock, err = os.OpenFile(lockPath, os.O_RDONLY, 0)
		if err != nil {
			return nil, err
		}
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
			_ = lock.Close()
			if errors.Is(err, unix.EWOULDBLOCK) {
				return nil, ErrStorageInUse
			}
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Store{root: absolute, lock: lock, verified: newVerificationCache(0), readOnly: true}, nil
}
