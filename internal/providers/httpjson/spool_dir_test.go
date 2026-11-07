package httpjson

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// #321 A: a multipart submission is spooled to the configured directory (the
// storage volume in production), not to the process temp dir, which in the
// container is a 64 MB tmpfs; nothing is left behind.
func TestMultipartSpoolUsesTheConfiguredDirectory(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", broken)
	payload := bytes.Repeat([]byte("glb"), 4096)
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	spool := t.TempDir()
	client, err := NewWithConfig(Config{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client(), SpoolDir: spool})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Do(context.Background(), http.MethodPost, "files", bytes.NewReader(payload),
		"multipart/form-data; boundary=x", "", nil, true); err != nil {
		t.Fatalf("spooled submission failed: %v", err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("received %d bytes, want %d", len(received), len(payload))
	}
	if entries, _ := os.ReadDir(spool); len(entries) != 0 {
		t.Fatalf("spool files left behind: %v", entries)
	}
}

type spoolObservation struct {
	entries int
	body    []byte
	length  int64
}

func observeOnce(spool string) (<-chan spoolObservation, http.HandlerFunc) {
	observed := make(chan spoolObservation, 1)
	return observed, func(w http.ResponseWriter, r *http.Request) {
		entries, _ := os.ReadDir(spool)
		body, _ := io.ReadAll(r.Body)
		observed <- spoolObservation{entries: len(entries), body: body, length: r.ContentLength}
		_, _ = w.Write([]byte(`{}`))
	}
}

// A provider that already spooled the finished multipart body (Hyper3D) hands
// the regular file over: httpjson sends it as-is, with its stat size as
// Content-Length, and leaves close/remove to the caller.
func TestMultipartSpoolReusesACallerSpooledFile(t *testing.T) {
	payload := bytes.Repeat([]byte("glb"), 4096)
	spool := t.TempDir()
	file, err := os.CreateTemp(spool, ScratchFilePrefix+"hyper3d-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	observed, handler := observeOnce(spool)
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewWithConfig(Config{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client(), SpoolDir: spool})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Do(context.Background(), http.MethodPost, "tasks", file,
		"multipart/form-data; boundary=x", "", nil, true); err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	seen := <-observed
	if seen.entries != 1 {
		t.Fatalf("scratch files during the request = %d, want only the caller's file", seen.entries)
	}
	if seen.length != int64(len(payload)) {
		t.Fatalf("Content-Length = %d, want %d", seen.length, len(payload))
	}
	if !bytes.Equal(seen.body, payload) {
		t.Fatalf("received %d bytes, want %d", len(seen.body), len(payload))
	}
	if _, err := file.Stat(); err != nil {
		t.Fatalf("httpjson closed the caller's file: %v", err)
	}
	if entries, _ := os.ReadDir(spool); len(entries) != 1 {
		t.Fatalf("caller's file removed by httpjson: %v", entries)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatal(err)
	}
}

// Reusing the caller's file must keep the non-idempotent guard: a 307 redirect
// cannot resend the paid submission body.
func TestMultipartSpoolReuseDoesNotReplayOnRedirect(t *testing.T) {
	var submits, redirects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/submit":
			submits.Add(1)
			http.Redirect(w, r, "/submit-target", http.StatusTemporaryRedirect)
		case "/submit-target":
			redirects.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	spool := t.TempDir()
	file, err := os.CreateTemp(spool, ScratchFilePrefix+"hyper3d-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("--x\r\n\r\nbody\r\n--x--\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	client, err := NewWithConfig(Config{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client(), SpoolDir: spool})
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Do(context.Background(), http.MethodPost, "submit", file,
		"multipart/form-data; boundary=x", "", nil, true)
	if count := submits.Load(); count != 1 {
		t.Fatalf("submit endpoint hits = %d, want 1", count)
	}
	if count := redirects.Load(); count != 0 {
		t.Fatalf("redirect target hits = %d, want 0 (the body must not be resent)", count)
	}
}

type closingReader struct {
	io.Reader
	closed atomic.Bool
}

func (c *closingReader) Close() error {
	c.closed.Store(true)
	return nil
}

// Pipe-like multipart bodies (Meshy, Tripo, the image editors and matting pass
// the imagewire pipe) still spool into SpoolDir, send the stat size, and get
// closed by httpjson after the submission.
func TestMultipartSpoolCopiesAndClosesNonFileBodies(t *testing.T) {
	payload := bytes.Repeat([]byte("png"), 2048)
	body := &closingReader{Reader: bytes.NewReader(payload)}
	spool := t.TempDir()
	observed, handler := observeOnce(spool)
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewWithConfig(Config{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client(), SpoolDir: spool})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Do(context.Background(), http.MethodPost, "tasks", body,
		"multipart/form-data; boundary=x", "", nil, true); err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	seen := <-observed
	if seen.entries != 1 {
		t.Fatalf("scratch files during the request = %d, want 1", seen.entries)
	}
	if seen.length != int64(len(payload)) {
		t.Fatalf("Content-Length = %d, want %d", seen.length, len(payload))
	}
	if !bytes.Equal(seen.body, payload) {
		t.Fatalf("received %d bytes, want %d", len(seen.body), len(payload))
	}
	if !body.closed.Load() {
		t.Fatal("pipe body was not closed after the submission")
	}
	if entries, _ := os.ReadDir(spool); len(entries) != 0 {
		t.Fatalf("spool files left behind: %v", entries)
	}
}
