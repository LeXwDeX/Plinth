package local

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #293: the CAS key is the content's SHA-256 plus the extension of its final media
// type. Two slots of one task that carry the same bytes under the same type (Tripo's
// GLTF conversion fulfils both the model slot and converted.gltf) are therefore two
// asset records over one stored file, not two copies of the bytes.

func sharedSlotBytes() []byte {
	return glbWithJSON(`{"asset":{"version":"2.0"},"extras":{"label":"shared slot"}}`)
}

func slotWrite(assetID generation.AssetID, role, variant string, ordinal int, mediaType string, body []byte) generation.AssetWrite {
	return generation.AssetWrite{
		AssetID: assetID, OwnerID: "owner-test", TaskID: "task-test", AttemptID: "attempt-test",
		Role: role, Variant: variant, Ordinal: ordinal, Required: true,
		MediaType: mediaType, MaxBytes: 1 << 20, Body: bytes.NewReader(body),
	}
}

// putSharedSlots stores the same GLB for the model slot and for converted.gltf.
func putSharedSlots(t *testing.T, store *Store, body []byte) (model, converted generation.Asset) {
	t.Helper()
	model, err := store.Put(t.Context(), slotWrite("asset-model", "model", "", 1, "model/gltf-binary", body))
	if err != nil {
		t.Fatal(err)
	}
	converted, err = store.Put(t.Context(), slotWrite("asset-converted-gltf", "converted_model", "converted.gltf", 1, "model/gltf-binary", body))
	if err != nil {
		t.Fatal(err)
	}
	return model, converted
}

// blobFiles lists the regular files under blobs/, relative to the storage root.
func blobFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "blobs"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func readAsset(t *testing.T, store *Store, asset generation.Asset) []byte {
	t.Helper()
	reader, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatalf("open %s: %v", asset.ID, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", asset.ID, err)
	}
	return data
}

func TestIdenticalBytesInTwoSlotsShareOneBlob(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	body := sharedSlotBytes()

	model, converted := putSharedSlots(t, store, body)

	// One stored file: the records agree on key, digest and size.
	if model.StorageKey != converted.StorageKey || model.SHA256 != converted.SHA256 ||
		model.Size != converted.Size || model.Size != int64(len(body)) {
		t.Fatalf("slots did not share a blob: model=%+v converted=%+v", model, converted)
	}
	if files := blobFiles(t, root); len(files) != 1 || files[0] != model.StorageKey {
		t.Fatalf("blob files = %v, want only %s", files, model.StorageKey)
	}
	// The second write dropped its temporary copy.
	if entries, err := os.ReadDir(filepath.Join(root, "tmp")); err != nil || len(entries) != 0 {
		t.Fatalf("temporary files left behind: %v (err=%v)", entries, err)
	}

	// Two records, each with its own slot identity.
	if model.ID == converted.ID {
		t.Fatalf("slots share an asset ID %q", model.ID)
	}
	if model.Role != "model" || model.Variant != "" || converted.Role != "converted_model" || converted.Variant != "converted.gltf" {
		t.Fatalf("slot identity lost: model=%+v converted=%+v", model, converted)
	}
	if model.MediaType != "model/gltf-binary" || converted.MediaType != "model/gltf-binary" {
		t.Fatalf("media types = %q, %q", model.MediaType, converted.MediaType)
	}
	// Each record is readable on its own.
	for _, asset := range []generation.Asset{model, converted} {
		if got := readAsset(t, store, asset); !bytes.Equal(got, body) {
			t.Fatalf("asset %s read %d bytes, want the shared %d", asset.ID, len(got), len(body))
		}
	}
}

func TestSharedBlobStaysWhileAnyAssetReferencesIt(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	body := sharedSlotBytes()
	model, converted := putSharedSlots(t, store, body)

	// A failed materialization removes the assets it wrote through Delete. For a
	// content-addressed blob that must leave the bytes: the other record still reads.
	if err := store.Delete(t.Context(), model); err != nil {
		t.Fatal(err)
	}
	if got := readAsset(t, store, converted); !bytes.Equal(got, body) {
		t.Fatalf("the remaining slot lost its bytes: %d", len(got))
	}

	// Maintenance decides by storage key, so a key two records share is protected
	// while either of them is still referenced.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, model.StorageKey), old, old); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	references := map[string]struct{}{converted.StorageKey: {}}
	options := MaintenanceOptions{
		Root: root, Execute: true, MinimumAge: 24 * time.Hour, Now: time.Now(), ReferenceBasis: "fixture",
		References: func(context.Context) (map[string]struct{}, error) { return references, nil },
	}
	report, err := Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.BlobCount != 1 || report.OrphanCount != 0 || report.DeletedCount != 0 {
		t.Fatalf("a referenced shared blob was listed or deleted: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, model.StorageKey)); err != nil {
		t.Fatal(err)
	}

	// Once no record references the key, the one file is the one orphan.
	delete(references, converted.StorageKey)
	report, err = Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphanCount != 1 || report.DeletedCount != 1 || report.DeletedBytes != model.Size {
		t.Fatalf("an unreferenced shared blob was not collected once: %+v", report)
	}
	if files := blobFiles(t, root); len(files) != 0 {
		t.Fatalf("blob files left after the last reference went: %v", files)
	}
}

// The key carries the extension of the final media type, so the same bytes stored
// under another final type are a second file with the same digest. This pins that
// boundary (docs/architecture.md, "内容寻址与共用的字节"); no product flow stores one
// file under two types today, and the key format is shared with maintenance,
// integrity scans and retention, so it is not changed here.
func TestIdenticalBytesUnderDifferentFinalMediaTypesAreKeptApart(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	body := sharedSlotBytes()

	glb, err := store.Put(t.Context(), slotWrite("asset-glb", "model", "", 1, "model/gltf-binary", body))
	if err != nil {
		t.Fatal(err)
	}
	opaque, err := store.Put(t.Context(), slotWrite("asset-opaque", "converted_model", "converted.gltf", 1, "application/octet-stream", body))
	if err != nil {
		t.Fatal(err)
	}

	if glb.SHA256 != opaque.SHA256 || glb.StorageKey == opaque.StorageKey {
		t.Fatalf("same bytes under two final types: glb=%+v opaque=%+v", glb, opaque)
	}
	if !strings.HasSuffix(glb.StorageKey, ".glb") || !strings.HasSuffix(opaque.StorageKey, ".bin") {
		t.Fatalf("keys = %q, %q", glb.StorageKey, opaque.StorageKey)
	}
	if files := blobFiles(t, root); len(files) != 2 {
		t.Fatalf("blob files = %v, want one per final media type", files)
	}
}
