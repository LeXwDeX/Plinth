//go:build unix

package wirecapture_test

import (
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

// The umask can only narrow a file's mode. The records are read back only as
// 0600 regular files, so the recorder sets the mode itself instead of trusting
// the process's umask.
func TestRuntimeRecorderWritesExactlyMode0600UnderAnyUmask(t *testing.T) {
	root := t.TempDir() // before the umask changes: the test directory itself needs write access
	previous := syscall.Umask(0o277)
	t.Cleanup(func() { syscall.Umask(previous) })
	dir := filepath.Join(root, "capture")
	recorder, err := wirecapture.NewRuntimeRecorder(runtimeConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	send(t, &http.Client{Transport: recorder.Wrap("meshy", scriptedUpstream{})}, http.MethodPost, vendorBase+"/text-to-3d", "application/json", []byte(`{"model":"meshy-5"}`))
	files := recordFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want one record, got %d (%v)", len(files), recorder.WriteError())
	}
	info, err := os.Lstat(filepath.Join(dir, files[0].Name()))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, %v", info.Mode().Perm(), err)
	}
	if _, _, err := wirecapture.LoadUpstreamRecords(dir); err != nil {
		t.Fatalf("a record written under a strict umask must still load: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, %v", info.Mode().Perm(), err)
	}
}
