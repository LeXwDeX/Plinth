package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestStoredAssetReadsRejectSymlinkAndOutsideRoot(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(store.root, "blobs", "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"blobs/linked", "../private.txt"} {
		if body, err := store.OpenBlob(context.Background(), key); err == nil {
			_ = body.Close()
			t.Fatalf("OpenBlob accepted %q", key)
		}
		if body, err := store.Open(context.Background(), generation.Asset{StorageKey: key}); err == nil {
			_ = body.Close()
			t.Fatalf("Open accepted %q", key)
		}
	}
}

func TestPutBlobValidCASHit(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("valid content addressed blob")
	first, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, first.StorageKey)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !os.SameFile(before, after) {
		t.Fatalf("valid CAS hit was replaced: first=%+v second=%+v", first, second)
	}
}

func TestPutBlobRepairsCorruptCASHit(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("original correct blob")
	first, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, first.StorageKey)
	// Same-length corruption must fail the digest check, not merely the size check.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !bytes.Equal(actual, content) {
		t.Fatalf("corrupt CAS hit not repaired: first=%+v second=%+v bytes=%q", first, second, actual)
	}
	if entries, err := os.ReadDir(filepath.Join(store.root, "tmp")); err != nil || len(entries) != 0 {
		t.Fatalf("temporary files left after repair: entries=%v err=%v", entries, err)
	}
}

func TestOpenRejectsCorruptReadyAssetBeforeServing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("ready asset bytes")
	blob, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{
		StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256,
	}
	reader, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("healthy asset read = %q, %v", actual, err)
	}
	if err := os.WriteFile(filepath.Join(store.root, blob.StorageKey), bytes.Repeat([]byte("x"), len(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	if reader, err := store.Open(t.Context(), asset); err == nil {
		_ = reader.Close()
		t.Fatal("corrupt READY asset was opened")
	}
	if reader, err := store.OpenBlob(t.Context(), blob.StorageKey); err == nil {
		_ = reader.Close()
		t.Fatal("corrupt input blob was opened")
	}
	asset.SHA256 = "" // historical record can still derive integrity from CAS key
	if reader, err := store.Open(t.Context(), asset); err == nil {
		_ = reader.Close()
		t.Fatal("corrupt historical CAS asset was opened")
	}
}

func TestPutBlobConcurrentRepair(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := bytes.Repeat([]byte("concurrent content"), 4096)
	first, err := store.PutBlob(t.Context(), bytes.NewReader(content), "application/octet-stream", int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, first.StorageKey)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	const writers = 12
	start := make(chan struct{})
	results := make(chan error, writers)
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			blob, err := store.PutBlob(t.Context(), bytes.NewReader(content), "application/octet-stream", int64(len(content)))
			if err == nil && blob != first {
				err = os.ErrInvalid
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent CAS repair failed: %v", err)
		}
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	got := sha256.Sum256(actual)
	if len(actual) != len(content) || hex.EncodeToString(got[:]) != hex.EncodeToString(want[:]) {
		t.Fatalf("concurrent CAS repair produced wrong bytes: size=%d digest=%x", len(actual), got)
	}
}

func TestPutBlobRejectsSymlinkCASHit(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("protected CAS bytes")
	digest := sha256.Sum256(content)
	hexDigest := hex.EncodeToString(digest[:])
	directory := filepath.Join(store.root, "blobs", hexDigest[:2])
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(store.root, "outside")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, hexDigest+".png")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024); err == nil {
		t.Fatal("symlink CAS hit succeeded")
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("symlink target changed: bytes=%q err=%v", actual, err)
	}
}

func TestPutBlobRejectsSymlinkCASDirectory(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("directory escape attempt")
	digest := sha256.Sum256(content)
	digestHex := hex.EncodeToString(digest[:])
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(store.root, "blobs", digestHex[:2])); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024); err == nil {
		t.Fatal("symlink CAS directory succeeded")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: entries=%v err=%v", entries, err)
	}
}
