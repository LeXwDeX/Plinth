package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceDryRunReferenceRecheckAndExclusiveExecution(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := store.PutBlob(t.Context(), bytes.NewBufferString("shared"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := store.PutBlob(t.Context(), bytes.NewBufferString("registration failed"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := store.PutBlob(t.Context(), bytes.NewBufferString("recent in flight"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	for _, key := range []string{reference.StorageKey, orphan.StorageKey} {
		if err := os.Chtimes(filepath.Join(root, key), old, old); err != nil {
			t.Fatal(err)
		}
	}
	refs := map[string]struct{}{reference.StorageKey: {}}
	reads := 0
	options := MaintenanceOptions{Root: root, MinimumAge: 24 * time.Hour, Now: now, References: func(context.Context) (map[string]struct{}, error) { reads++; return refs, nil }, ReferenceBasis: "fixture"}
	report, err := Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.OrphanCount != 1 || report.RecentUnreferencedCount != 1 || report.DeletedCount != 0 || report.CapacityBytes == 0 {
		t.Fatalf("report=%+v", report)
	}
	options.Execute = true
	if _, err := Maintain(t.Context(), options); !errors.Is(err, ErrStorageInUse) {
		t.Fatalf("concurrent cleanup error=%v", err)
	}
	if reads != 1 {
		t.Fatal("execution read references before excluding writers")
	}
	// A reference registered after the dry-run must protect the old orphan.
	refs[orphan.StorageKey] = struct{}{}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	report, err = Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeletedCount != 0 {
		t.Fatalf("late reference deleted: %+v", report)
	}
	delete(refs, orphan.StorageKey)
	report, err = Maintain(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeletedCount != 1 || report.DeletedBytes != orphan.Size {
		t.Fatalf("delete report=%+v", report)
	}
	for _, key := range []string{reference.StorageKey, recent.StorageKey} {
		if _, err := os.Stat(filepath.Join(root, key)); err != nil {
			t.Fatal(err)
		}
	}
	report, err = Maintain(t.Context(), options)
	if err != nil || report.DeletedCount != 0 {
		t.Fatalf("repeat report=%+v err=%v", report, err)
	}
}
func TestMaintenanceDoesNotDeleteWhenReferenceSnapshotFails(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.PutBlob(t.Context(), bytes.NewBufferString("unregistered"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = Maintain(t.Context(), MaintenanceOptions{Root: root, Execute: true, MinimumAge: time.Hour, Now: time.Now().Add(2 * time.Hour), References: func(context.Context) (map[string]struct{}, error) { return nil, errors.New("database busy") }})
	if err == nil {
		t.Fatal("missing database snapshot succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, blob.StorageKey)); err != nil {
		t.Fatal(err)
	}
}
func TestMaintenanceExclusiveLockBlocksNewStore(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = Maintain(t.Context(), MaintenanceOptions{Root: root, Execute: true, MinimumAge: time.Hour, Now: time.Now(), References: func(context.Context) (map[string]struct{}, error) {
		if writer, err := New(root); !errors.Is(err, ErrStorageInUse) {
			if writer != nil {
				writer.Close()
			}
			t.Fatalf("writer entered maintenance: %v", err)
		}
		return map[string]struct{}{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// #321 A: Provider Adapters keep private upload copies under tmp/provider-*.
// They are removed as soon as the upload ends; one a crash left behind is an
// old unreferenced candidate like an interrupted asset write. Other files in
// tmp stay untouched.
func TestMaintenanceListsOldProviderScratchFiles(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	for _, name := range []string{"provider-tripo-model-1", "provider-upload-2", "art-ai-model-archive-3.zip"} {
		path := filepath.Join(root, "tmp", name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Maintain(t.Context(), MaintenanceOptions{Root: root, MinimumAge: 24 * time.Hour, Now: now,
		References: func(context.Context) (map[string]struct{}, error) { return map[string]struct{}{}, nil }, ReferenceBasis: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, candidate := range report.Candidates {
		keys = append(keys, candidate.StorageKey)
	}
	if len(keys) != 2 || keys[0] != "tmp/provider-tripo-model-1" || keys[1] != "tmp/provider-upload-2" {
		t.Fatalf("candidates = %v", keys)
	}
}
