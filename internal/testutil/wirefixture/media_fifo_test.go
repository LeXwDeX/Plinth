//go:build darwin || linux

package wirefixture_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPrivateSourceRejectsFIFOAndSymlink(t *testing.T) {
	store, dir := privateStore(t)
	source, err := store.Spool("image/png", []byte(mediaCanary))
	if err != nil {
		t.Fatal(err)
	}
	paths := privateFiles(t, dir)
	if len(paths) != 1 {
		t.Fatalf("evidence files = %v", paths)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(paths[0], 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := source.Bytes()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO evidence read succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO evidence read blocked")
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(target, []byte(mediaCanary), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Bytes(); err == nil {
		t.Fatal("symlink evidence read succeeded")
	}
}
