package local

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestOpenRejectsCorruptionEvenWithMatchingCachedIdentity(t *testing.T) {
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
	path := filepath.Join(store.root, blob.StorageKey)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(blob.Size)), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store.verified.remember(info, asset)
	reader, err := store.Open(t.Context(), asset)
	if err == nil {
		reader.Close()
		t.Fatal("corrupt bytes returned on matching cache identity")
	}
}

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
	if verifiedBytes != uint64(2*blob.Size) {
		t.Fatalf("verified bytes = %d, want %d: every open must hash", verifiedBytes, 2*blob.Size)
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
	if hits, misses, verifiedBytes := store.VerificationStats(); hits != 1 || misses != 0 || verifiedBytes != uint64(asset.Size) {
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
	// Restore modification time; the stored digest must still catch corruption.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if reader, err := store.Open(t.Context(), asset); err == nil {
		_ = reader.Close()
		t.Fatal("a rewrite that restored size and modification time reused the verdict")
	}
	if hits, misses, verifiedBytes := store.VerificationStats(); hits != 0 || misses != 1 || verifiedBytes != uint64(2*blob.Size) {
		t.Fatalf("verification stats = hits %d, misses %d, bytes %d; the tampered file reused a verdict", hits, misses, verifiedBytes)
	}
}

func TestPutPrewarmStillHashesFirstOpen(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	asset, err := store.Put(t.Context(), profileWrite(schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, profile3MFEntries())))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, asset.StorageKey)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(asset.Size)), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store.verified.remember(info, asset) // Simulate an unchanged identity.
	if reader, err := store.Open(t.Context(), asset); err == nil {
		reader.Close()
		t.Fatal("corruption after Put was opened")
	}
}

func TestOpenDigestSourcesAndLegacy(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content := []byte("ordinary local content")
	blob, err := store.PutBlob(t.Context(), bytes.NewReader(content), "text/plain", 1024)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	for _, digest := range []string{blob.SHA256, ""} {
		asset.SHA256 = digest
		reader, err := store.Open(t.Context(), asset)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := reader.(io.ReaderAt); !ok {
			t.Fatal("reader lost ReaderAt")
		}
		reader.Close()
	}
	asset.SHA256 = "invalid"
	if reader, err := store.Open(t.Context(), asset); err == nil {
		reader.Close()
		t.Fatal("invalid record digest fell back to CAS")
	}
	legacy := generation.Asset{StorageKey: "blobs/legacy", Size: int64(len(content))}
	path := filepath.Join(store.root, legacy.StorageKey)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reader, err := store.Open(t.Context(), legacy)
		if err != nil {
			t.Fatal(err)
		}
		reader.Close()
	}
	if err := os.WriteFile(path, []byte("http://evil.example/"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy.Size = 0
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store.verified.remember(info, legacy)
	if reader, err := store.Open(t.Context(), legacy); err == nil {
		reader.Close()
		t.Fatal("legacy isolation skipped")
	}
	hits, misses, count := store.VerificationStats()
	if hits != 1 || misses != 4 || count != uint64(3*len(content)) {
		t.Fatalf("stats = %d %d %d", hits, misses, count)
	}
}

func TestPutRejectsInvalidContentForFinalMIME(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	write := profileWrite("", "", printreport.MediaType+"; charset=utf-8", []byte("not a print report"))
	asset, err := store.Put(t.Context(), write)
	if err == nil || asset != (generation.Asset{}) {
		t.Fatalf("invalid final MIME accepted: asset=%+v err=%v", asset, err)
	}
	if err := filepath.WalkDir(filepath.Join(store.root, "blobs"), func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Error("rejected Put left a CAS file")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPutChecksBothDeclaredAndFinalMIME(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	report, err := printreport.Marshal(printreport.Report{Format: printreport.Format, Kind: "repair", Repair: &printreport.Repair{DetailsStatus: "not_reported"}})
	if err != nil {
		t.Fatal(err)
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, declared, final string
		body                  []byte
	}{
		{"parameterized report", printreport.MediaType + "; charset=utf-8", printreport.MediaType, report},
		{"corrected image", "image/webp", "image/png", imageBytes.Bytes()},
		{"identical MIME", "image/png", "image/png", imageBytes.Bytes()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset, err := store.Put(t.Context(), profileWrite("", "", tc.declared, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if asset.MediaType != tc.final {
				t.Fatalf("final MIME = %q", asset.MediaType)
			}
			beforeHits, _, beforeBytes := store.VerificationStats()
			reader, err := store.Open(t.Context(), asset)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || !bytes.Equal(actual, tc.body) {
				t.Fatalf("open bytes or error: %v", err)
			}
			hits, _, hashed := store.VerificationStats()
			if hits != beforeHits+1 || hashed != beforeBytes+uint64(len(tc.body)) {
				t.Fatalf("prewarm did not retain parse-only verdict: hits=%d bytes=%d", hits-beforeHits, hashed-beforeBytes)
			}
		})
	}
	// Normalization must not relax the profile's exact declared MIME contract.
	write := profileWrite(schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf; charset=utf-8", profileZIP(t, profile3MFEntries()))
	if asset, err := store.Put(t.Context(), write); err == nil || asset != (generation.Asset{}) {
		t.Fatalf("parameterized strict profile accepted: %+v %v", asset, err)
	}
}

func TestVerificationCacheIdentityIncludesDeliveryContract(t *testing.T) {
	cache := newVerificationCache(8)
	info := fakeFileInfo{size: 5, modified: time.Unix(1, 0)}
	base := generation.Asset{StorageKey: "blobs/x", SHA256: "digest", Size: 5, DeliveryProfile: "profile", Role: "model", Variant: "fbx", MediaType: "model/fbx"}
	cache.remember(info, base)
	variants := []generation.Asset{base, base, base, base, base}
	variants[0].DeliveryProfile = "other"
	variants[1].Role = "other"
	variants[2].Variant = "other"
	variants[3].MediaType = "other"
	variants[4].Size++
	for _, asset := range variants {
		if cache.hit(info, asset) {
			t.Fatalf("wrong contract hit: %+v", asset)
		}
	}
	if !cache.hit(info, base) {
		t.Fatal("exact contract missed")
	}
}

type partialErrorReader struct{ err error }

func (r partialErrorReader) Read(p []byte) (int, error) { return copy(p, "partial"), r.err }

type cancelAfterRead struct{ cancel context.CancelFunc }

func (r cancelAfterRead) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, "partial"), nil
}

func TestVerifyDigestCountsReadFailuresAndCancellation(t *testing.T) {
	store := &Store{}
	readErr := errors.New("read failure")
	if err := store.verifyDigest(t.Context(), partialErrorReader{readErr}, "", 7); !errors.Is(err, readErr) {
		t.Fatalf("read error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.verifyDigest(ctx, bytes.NewReader([]byte("ignored")), "", 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	if err := store.verifyDigest(ctx, cancelAfterRead{cancel}, "", 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-read cancel: %v", err)
	}
	if _, _, count := store.VerificationStats(); count != 14 {
		t.Fatalf("actual bytes = %d", count)
	}
}

func TestOpenConcurrentAndCanceled(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blob, err := store.PutBlob(t.Context(), bytes.NewReader([]byte("concurrent data")), "text/plain", 1024)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if reader, err := store.Open(ctx, asset); !errors.Is(err, context.Canceled) {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("cancel: %v", err)
	}
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.Open(t.Context(), asset)
			if err != nil {
				t.Error(err)
				return
			}
			r.Close()
		}()
	}
	wg.Wait()
	if _, _, count := store.VerificationStats(); count != uint64(n)*uint64(blob.Size) {
		t.Fatalf("hashed bytes = %d", count)
	}
}

func TestCheckFileIdentityFailsOnChangeAndStatError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("changed size"); err != nil {
		t.Fatal(err)
	}
	if err := checkFileIdentity(file, before); err == nil {
		t.Fatal("identity change accepted")
	}
	file.Close()
	if err := checkFileIdentity(file, before); err == nil {
		t.Fatal("stat error accepted")
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
