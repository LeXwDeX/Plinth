//go:build darwin

package local

import (
	"os"
	"syscall"
)

// fileIdentityOf captures attributes that can detect some file changes; equal
// identities do not prove that the bytes were unchanged.
func fileIdentityOf(info os.FileInfo) fileIdentity {
	identity := fileIdentity{size: info.Size(), modified: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		identity.changed = stat.Ctimespec.Nano()
		identity.device = uint64(stat.Dev)
		identity.inode = stat.Ino
	}
	return identity
}
