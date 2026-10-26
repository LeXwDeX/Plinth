package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanIntegrityReportsDamagedMissingUnknownAndSharedReferences(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("static bytes with expected digest")
	blob, err := store.PutBlob(t.Context(), bytes.NewReader(content), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	root := store.root
	corruptContent := bytes.Repeat([]byte("x"), len(content))
	if err := os.WriteFile(filepath.Join(root, blob.StorageKey), corruptContent, 0o600); err != nil {
		t.Fatal(err)
	}
	missingDigest := sha256.Sum256([]byte("missing blob"))
	missingKey := filepath.ToSlash(filepath.Join("blobs", hex.EncodeToString(missingDigest[:1]), hex.EncodeToString(missingDigest[:])+".png"))
	unknownKey := "legacy/outside.dat"
	references := []AssetReference{
		{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256, SourceKind: "generation_asset", SourceID: "asset-a"},
		{StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256, SourceKind: "input_asset", SourceID: "asset-b"},
		{StorageKey: missingKey, Size: 12, SHA256: hex.EncodeToString(missingDigest[:]), SourceKind: "artifact_claim", SourceID: "attempt:claim"},
		{StorageKey: unknownKey, Size: 1, SourceKind: "input_asset", SourceID: "legacy-id"},
	}
	report, err := ScanIntegrity(context.Background(), root, func(context.Context) ([]AssetReference, error) { return references, nil }, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if !report.ReadOnly || report.ReferenceCount != 4 || report.BlobCount != 3 || report.IssueCount != 3 {
		t.Fatalf("unexpected report summary: %+v", report)
	}
	issues := make(map[string]IntegrityIssue, len(report.Issues))
	for _, issue := range report.Issues {
		issues[issue.StorageKey] = issue
	}
	if issue := issues[blob.StorageKey]; issue.Status != "corrupt" || len(issue.References) != 2 || issue.ActualSHA == blob.SHA256 {
		t.Fatalf("shared same-size corruption not attributed: %+v", issue)
	}
	if issue := issues[missingKey]; issue.Status != "missing" || len(issue.References) != 1 {
		t.Fatalf("missing blob not reported: %+v", issue)
	}
	if issue := issues[unknownKey]; issue.Status != "unknown_key" || len(issue.References) != 1 {
		t.Fatalf("unknown key not reported safely: %+v", issue)
	}
	actual, err := os.ReadFile(filepath.Join(root, blob.StorageKey))
	if err != nil || !bytes.Equal(actual, corruptContent) {
		t.Fatalf("scan mutated bytes: %q err=%v", actual, err)
	}
}

func TestScanIntegrityReportsHealthyAndLegacyDigest(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.PutBlob(t.Context(), bytes.NewBufferString("healthy"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := ScanIntegrity(context.Background(), store.root, func(context.Context) ([]AssetReference, error) {
		return []AssetReference{{StorageKey: blob.StorageKey, Size: blob.Size, SourceKind: "generation_asset", SourceID: "legacy-without-sha"}}, nil
	}, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if report.IssueCount != 0 || report.BlobCount != 1 {
		t.Fatalf("healthy legacy CAS failed scan: %+v", report)
	}
}

func TestScanIntegrityExcludesStoreWritersAndRequiresReferences(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ScanIntegrity(context.Background(), store.root, func(context.Context) ([]AssetReference, error) { return nil, nil }, time.Now().UTC().Format(time.RFC3339Nano)); err != ErrStorageInUse {
		t.Fatalf("scan did not exclude active store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanIntegrity(context.Background(), store.root, nil, time.Now().UTC().Format(time.RFC3339Nano)); err == nil {
		t.Fatal("scan accepted missing reference snapshot")
	}
}
