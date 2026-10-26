package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// #291: asset previews are cached by their original's SHA-256 and go with the blob they were made from.
func TestMaintenanceRemovesPreviewsNoReferencedBlobNeeds(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := store.PutBlob(t.Context(), bytes.NewBufferString("still referenced"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	keptDigest := filepath.Base(kept.StorageKey)[:64]
	goneDigest := strings.Repeat("ab", 32)
	recentDigest := strings.Repeat("cd", 32)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	write := func(key string, modified time.Time) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("preview"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	current := "asset-previews/" + keptDigest[:2] + "/" + keptDigest + "-512.png"
	otherSize := "asset-previews/" + keptDigest[:2] + "/" + keptDigest + "-256.png"
	leftover := "asset-previews/" + keptDigest[:2] + "/" + keptDigest + "-512.png.tmp-123456"
	orphan := "asset-previews/ab/" + goneDigest + "-512.png"
	recent := "asset-previews/cd/" + recentDigest + "-512.png"
	unknown := "asset-previews/ab/notes.txt"
	wrongShard := "asset-previews/ff/" + goneDigest + "-512.png"
	for _, key := range []string{current, otherSize, leftover, orphan, unknown, wrongShard} {
		write(key, old)
	}
	write(recent, now)
	if err := os.Chtimes(filepath.Join(root, kept.StorageKey), old, old); err != nil {
		t.Fatal(err)
	}
	options := MaintenanceOptions{
		Root: root, MinimumAge: 24 * time.Hour, Now: now, ReferenceBasis: "fixture",
		References: func(context.Context) (map[string]struct{}, error) {
			return map[string]struct{}{kept.StorageKey: {}}, nil
		},
		Previews: &PreviewMaintenance{Subdirectory: "asset-previews", MaximumDimension: 512},
	}
	report, err := Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{leftover, otherSize, orphan}
	sort.Strings(want)
	var got []string
	for _, candidate := range report.Candidates {
		got = append(got, candidate.StorageKey)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") || report.PreviewCount != 5 || report.PreviewOrphanCount != 3 ||
		report.PreviewRecentUnreferencedCount != 1 || report.OrphanCount != 0 || report.DeletedCount != 0 {
		t.Fatalf("candidates=%v report=%+v", got, report)
	}
	options.Execute = true
	report, err = Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeletedCount != 3 {
		t.Fatalf("execute report=%+v", report)
	}
	for _, key := range want {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still present: %v", key, err)
		}
	}
	for _, key := range []string{current, recent, unknown, wrongShard, kept.StorageKey} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); err != nil {
			t.Fatalf("%s removed: %v", key, err)
		}
	}
	// The blob's preview goes too once the blob is no longer referenced.
	options.References = func(context.Context) (map[string]struct{}, error) { return map[string]struct{}{}, nil }
	report, err = Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.PreviewOrphanCount != 1 || report.OrphanCount != 1 || report.DeletedCount != 2 {
		t.Fatalf("unreferenced report=%+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(current))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview of an unreferenced blob kept: %v", err)
	}
}

func TestMaintenanceRejectsAPreviewFolderOutsideItsOwn(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	none := func(context.Context) (map[string]struct{}, error) { return map[string]struct{}{}, nil }
	for _, folder := range []string{"", ".", "..", "blobs", "tmp", "a/b", "../previews"} {
		_, err := Maintain(t.Context(), MaintenanceOptions{
			Root: root, MinimumAge: time.Hour, Now: time.Now(), References: none,
			Previews: &PreviewMaintenance{Subdirectory: folder, MaximumDimension: 512},
		})
		if err == nil {
			t.Fatalf("folder %q accepted", folder)
		}
	}
	// No preview folder yet: nothing to do, no error.
	report, err := Maintain(t.Context(), MaintenanceOptions{
		Root: root, MinimumAge: time.Hour, Now: time.Now(), References: none,
		Previews: &PreviewMaintenance{Subdirectory: "asset-previews", MaximumDimension: 512},
	})
	if err != nil || report.PreviewCount != 0 || report.PreviewSubdirectory != "asset-previews" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
