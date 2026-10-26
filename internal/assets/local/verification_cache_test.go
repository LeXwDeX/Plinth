package local

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestOpenVerifiesAFileOnceWhileItIsUnchanged(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blob, err := store.PutBlob(t.Context(), bytes.NewReader([]byte("ready asset bytes")), "text/plain", 1024)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	for round := 0; round < 2; round++ {
		reader, err := store.Open(t.Context(), asset)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		if _, err := reader.Read(make([]byte, 1)); err != nil {
			t.Fatalf("read %d: %v", round, err)
		}
		_ = reader.Close()
	}
	hits, misses, verifiedBytes := store.VerificationStats()
	if hits != 1 || misses != 1 {
		t.Fatalf("verification stats = hits %d, misses %d; want one full check then one reuse", hits, misses)
	}
	if verifiedBytes != uint64(blob.Size) {
		t.Fatalf("verified bytes = %d, want %d: the cached open re-read the file", verifiedBytes, blob.Size)
	}
}

func TestPutRemembersTheVerdictOfFreshlyInspectedBytes(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	asset, err := store.Put(t.Context(), profileWrite(
		schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, profile3MFEntries())))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if hits, misses, verifiedBytes := store.VerificationStats(); hits != 1 || misses != 0 || verifiedBytes != 0 {
		t.Fatalf("verification stats = hits %d, misses %d, bytes %d; Put must prewarm the verdict", hits, misses, verifiedBytes)
	}
}

func TestOpenReverifiesAfterAnInPlaceRewriteThatKeepsSizeAndModificationTime(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blob, err := store.PutBlob(t.Context(), bytes.NewReader([]byte("ready asset bytes")), "text/plain", 1024)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	reader, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	path := filepath.Join(root, blob.StorageKey)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(blob.Size)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Put the modification time back by hand: only the change time still
	// distinguishes the rewrite, and the stored digest must catch it.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if reader, err := store.Open(t.Context(), asset); err == nil {
		_ = reader.Close()
		t.Fatal("a rewrite that restored size and modification time reused the verdict")
	}
	if hits, misses, verifiedBytes := store.VerificationStats(); hits != 0 || misses != 2 || verifiedBytes != uint64(2*blob.Size) {
		t.Fatalf("verification stats = hits %d, misses %d, bytes %d; the tampered file reused a verdict", hits, misses, verifiedBytes)
	}
}

func TestVerificationCacheEvictsOldEntries(t *testing.T) {
	cache := newVerificationCache(2)
	base := time.Unix(1700000000, 0)
	info := func(size int64) os.FileInfo {
		return fakeFileInfo{size: size, modified: base}
	}
	for size := int64(1); size <= 3; size++ {
		cache.remember(info(size), generation.Asset{StorageKey: "blobs/aa/x"})
	}
	if cache.hit(info(1), generation.Asset{StorageKey: "blobs/aa/x"}) {
		t.Fatal("oldest verdict survived the bound")
	}
	if !cache.hit(info(3), generation.Asset{StorageKey: "blobs/aa/x"}) {
		t.Fatal("newest verdict was evicted")
	}
}

type fakeFileInfo struct {
	size     int64
	modified time.Time
}

func (f fakeFileInfo) Name() string       { return "fake" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o600 }
func (f fakeFileInfo) ModTime() time.Time { return f.modified }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
