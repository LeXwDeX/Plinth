package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

// A Range response materializes the whole ZIP. The container's /tmp is a small
// tmpfs, so the assembly must point this at the storage volume; a regression
// here only shows up as a 500 on large archives, not on the streaming path.
func TestArchiveTempFileUsesTheConfiguredDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	server := &Server{archiveTempDir: directory}
	file, err := server.createArchiveTempFile()
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	t.Cleanup(func() { _ = file.Close(); _ = os.Remove(name) })
	if got := filepath.Dir(name); got != directory {
		t.Fatalf("temp file directory = %q, want %q", got, directory)
	}
}

func TestArchiveTempFileFallsBackToTheProcessTempDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	server := &Server{}
	file, err := server.createArchiveTempFile()
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	t.Cleanup(func() { _ = file.Close(); _ = os.Remove(name) })
	if got := filepath.Dir(name); got != directory {
		t.Fatalf("temp file directory = %q, want %q", got, directory)
	}
}
