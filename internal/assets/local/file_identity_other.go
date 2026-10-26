//go:build !linux && !darwin

package local

import "os"

// fileIdentityOf falls back to size and modification time where no portable
// change time is available. Plinth only runs on Linux and macOS; this keeps
// the package buildable elsewhere without silently claiming a stronger check.
func fileIdentityOf(info os.FileInfo) fileIdentity {
	return fileIdentity{size: info.Size(), modified: info.ModTime().UnixNano()}
}
