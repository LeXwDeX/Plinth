package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
)

func TestRunCheckEmitsReadOnlyIntegrityReport(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.PutBlob(t.Context(), bytes.NewBufferString("expected bytes"), "image/png", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, blob.StorageKey), []byte("corrupt bytes!"), 0o600); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "refs.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE generation_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_input_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_artifact_claims (attempt_id TEXT, claim_id TEXT, stored_asset_json TEXT)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO generation_assets VALUES (?, ?, ?, ?)`, "output", blob.StorageKey, blob.Size, blob.SHA256); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--storage", root, "--database", databasePath, "--check"}, &output); err != nil {
		t.Fatal(err)
	}
	var report local.IntegrityReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", output.String(), err)
	}
	if !report.ReadOnly || report.ReferenceCount != 1 || report.IssueCount != 1 || len(report.Issues) != 1 || report.Issues[0].Status != "corrupt" || report.Issues[0].References[0].SourceID != "output" {
		t.Fatalf("unexpected integrity output: %+v", report)
	}
	if err := os.WriteFile(filepath.Join(root, blob.StorageKey), []byte("corrupt bytes!"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #291: the dry run reports the asset previews of the default Schema's cache folder with the blobs.
func TestRunDryRunReportsAssetPreviewsOfTheSchemaFolder(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("ab", 32)
	preview := filepath.Join(root, "asset-previews", "ab", digest+"-512.png")
	if err := os.MkdirAll(filepath.Dir(preview), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preview, []byte("preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(preview, old, old); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "refs.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE generation_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_input_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_artifact_claims (attempt_id TEXT, claim_id TEXT, stored_asset_json TEXT)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--storage", root, "--database", databasePath}, &output); err != nil {
		t.Fatal(err)
	}
	var report local.MaintenanceReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON report %q: %v", output.String(), err)
	}
	if !report.DryRun || report.PreviewSubdirectory != "asset-previews" || report.PreviewOrphanCount != 1 || len(report.Candidates) != 1 ||
		report.Candidates[0].StorageKey != "asset-previews/ab/"+digest+"-512.png" || report.DeletedCount != 0 {
		t.Fatalf("unexpected maintenance output: %+v", report)
	}
	if _, err := os.Stat(preview); err != nil {
		t.Fatalf("dry run removed the preview: %v", err)
	}
}
