package httpjson

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
