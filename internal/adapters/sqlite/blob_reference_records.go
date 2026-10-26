package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// BlobReferenceRecord preserves the logical source of each persisted CAS
// reference so offline integrity reports can identify all affected records.
type BlobReferenceRecord struct {
	StorageKey string `json:"storage_key"`
	Size       int64  `json:"size_bytes"`
	SHA256     string `json:"sha256,omitempty"`
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
}

// BlobReferenceRecords reads all generation, input and materialization-claim
// references in one read-only SQLite snapshot. It includes tombstoned tasks;
// malformed stored-asset JSON fails the snapshot rather than silently
// omitting a potentially live reference.
func BlobReferenceRecords(ctx context.Context, databasePath string) ([]BlobReferenceRecord, error) {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("reference database must be a regular file")
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var records []BlobReferenceRecord
	queries := []struct {
		kind, query string
	}{
		{"generation_asset", `SELECT id, storage_key, size_bytes, sha256 FROM generation_assets ORDER BY id`},
		{"input_asset", `SELECT id, storage_key, size_bytes, sha256 FROM generation_input_assets ORDER BY id`},
	}
	for _, item := range queries {
		rows, err := tx.QueryContext(ctx, item.query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var record BlobReferenceRecord
			if err := rows.Scan(&record.SourceID, &record.StorageKey, &record.Size, &record.SHA256); err != nil {
				rows.Close()
				return nil, err
			}
			record.SourceKind = item.kind
			if record.StorageKey != "" {
				records = append(records, record)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	var recoveryTable int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='generation_recovery_receipts'`).Scan(&recoveryTable); err != nil {
		return nil, err
	}
	if recoveryTable == 1 {
		rows, err := tx.QueryContext(ctx, `SELECT id,digest,payload_json FROM generation_recovery_receipts ORDER BY id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, digest, raw string
			if err := rows.Scan(&id, &digest, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			receipt, err := decodeRecoveryReceipt(raw, id, digest)
			if err != nil {
				rows.Close()
				return nil, err
			}
			for _, slot := range receipt.Slots {
				records = append(records, BlobReferenceRecord{StorageKey: slot.StorageKey, Size: slot.Size, SHA256: slot.SHA256, SourceKind: "recovery_receipt", SourceID: id + ":" + slot.Role + ":" + slot.Variant})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	columns, err := tx.QueryContext(ctx, `PRAGMA table_info(generation_artifact_claims)`)
	if err != nil {
		return nil, err
	}
	storedClaim := false
	for columns.Next() {
		var id, notnull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err := columns.Scan(&id, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			columns.Close()
			return nil, err
		}
		if name == "stored_asset_json" {
			storedClaim = true
		}
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return nil, err
	}
	columns.Close()
	if storedClaim {
		rows, err := tx.QueryContext(ctx, `SELECT attempt_id, claim_id, stored_asset_json FROM generation_artifact_claims WHERE stored_asset_json IS NOT NULL ORDER BY attempt_id, claim_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var attemptID, claimID, encoded string
			if err := rows.Scan(&attemptID, &claimID, &encoded); err != nil {
				rows.Close()
				return nil, err
			}
			var asset struct {
				StorageKey string `json:"storage_key"`
				Size       int64  `json:"size"`
				SHA256     string `json:"sha256"`
			}
			if err := json.Unmarshal([]byte(encoded), &asset); err != nil {
				rows.Close()
				return nil, errors.New("invalid stored asset claim JSON")
			}
			if asset.StorageKey != "" {
				records = append(records, BlobReferenceRecord{StorageKey: asset.StorageKey, Size: asset.Size, SHA256: asset.SHA256, SourceKind: "artifact_claim", SourceID: attemptID + ":" + claimID})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return records, nil
}
