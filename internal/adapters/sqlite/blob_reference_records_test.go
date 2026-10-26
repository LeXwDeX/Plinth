package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestBlobReferenceRecordsKeepsEveryReferenceSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE generation_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_input_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_artifact_claims (attempt_id TEXT, claim_id TEXT, stored_asset_json TEXT)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	assetJSON, err := json.Marshal(map[string]any{"storage_key": "blobs/ab/abcdef.png", "size": 23, "sha256": "abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO generation_assets VALUES (?, ?, ?, ?)`, []any{"output-1", "blobs/ab/abcdef.png", 23, "abcdef"}},
		{`INSERT INTO generation_input_assets VALUES (?, ?, ?, ?)`, []any{"input-1", "blobs/ab/abcdef.png", 23, "abcdef"}},
		{`INSERT INTO generation_artifact_claims VALUES (?, ?, ?)`, []any{"attempt-1", "claim-1", string(assetJSON)}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	records, err := BlobReferenceRecords(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d references, want all 3: %+v", len(records), records)
	}
	want := map[string]BlobReferenceRecord{
		"generation_asset:output-1":        {StorageKey: "blobs/ab/abcdef.png", Size: 23, SHA256: "abcdef", SourceKind: "generation_asset", SourceID: "output-1"},
		"input_asset:input-1":              {StorageKey: "blobs/ab/abcdef.png", Size: 23, SHA256: "abcdef", SourceKind: "input_asset", SourceID: "input-1"},
		"artifact_claim:attempt-1:claim-1": {StorageKey: "blobs/ab/abcdef.png", Size: 23, SHA256: "abcdef", SourceKind: "artifact_claim", SourceID: "attempt-1:claim-1"},
	}
	for _, record := range records {
		key := record.SourceKind + ":" + record.SourceID
		if record != want[key] {
			t.Errorf("record %s = %+v, want %+v", key, record, want[key])
		}
	}
}

func TestBlobReferenceRecordsRejectsMalformedClaimJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-claim.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE generation_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_input_assets (id TEXT, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)`,
		`CREATE TABLE generation_artifact_claims (attempt_id TEXT, claim_id TEXT, stored_asset_json TEXT)`,
		`INSERT INTO generation_artifact_claims VALUES ('attempt', 'claim', '{')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if records, err := BlobReferenceRecords(context.Background(), path); err == nil || records != nil {
		t.Fatalf("malformed claim silently omitted: records=%+v err=%v", records, err)
	}
}
