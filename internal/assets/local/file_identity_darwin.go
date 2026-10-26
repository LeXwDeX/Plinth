//go:build darwin

package local

import (
	"os"
	"syscall"
)

// fileIdentityOf captures the attributes that change when a stored file is
// rewritten or replaced. The change time is the part a caller cannot reset
// through utimes, so an in-place rewrite never reuses a verified verdict even
// when the size and the modification time were put back by hand.
func fileIdentityOf(info os.FileInfo) fileIdentity {
	identity := fileIdentity{size: info.Size(), modified: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		identity.changed = stat.Ctimespec.Nano()
		identity.device = uint64(stat.Dev)
		identity.inode = stat.Ino
	}
	return identity
}
