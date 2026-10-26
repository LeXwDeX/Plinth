package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func recoveryRows(ctx context.Context, q queryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, column := range columns {
			row[column] = values[i]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// Historical imports may store JSON with SQLite's TEXT or BLOB affinity.
// Preserve the exact bytes for identity; never compare interfaces holding slices.
func recoveryJSONBytes(value any) []byte {
	switch value := value.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		return nil
	}
}

// The 0055 columns add pricing evidence, not new legacy Task facts. Omitting
// only their historical NULL/zero defaults preserves receipts sealed on 0054;
// native or malformed non-default evidence remains inside the source digest.
func recoverySourceTaskSnapshot(task map[string]any) map[string]any {
	required, exists := task["native_quote_required"]
	if task["native_quote_json"] != nil || exists && required != int64(0) {
		return task
	}
	result := make(map[string]any, len(task))
	for key, value := range task {
		if key != "native_quote_json" && key != "native_quote_required" {
			result[key] = value
		}
	}
	return result
}

func readRecoverySource(ctx context.Context, q queryer, owner generation.OwnerID, id generation.TaskID) (generation.RecoverySource, error) {
	var result generation.RecoverySource
	tasks, err := recoveryRows(ctx, q, `SELECT t.* FROM generation_tasks t JOIN go_chat_sessions s ON s.id=t.session_id AND s.owner_id=t.owner_id WHERE t.id=? AND t.owner_id=? AND t.state='FAILED' AND NOT EXISTS(SELECT 1 FROM generation_task_tombstones d WHERE d.task_id=t.id)`, id, owner)
	if err != nil {
		return result, err
	}
	if len(tasks) != 1 {
		return result, generation.RepositoryNotFound
	}
	task := tasks[0]
	attemptID, ok := task["current_attempt_id"].(string)
	if !ok || attemptID == "" {
		return result, generation.RepositoryConflict
	}
	attempts, err := recoveryRows(ctx, q, `SELECT * FROM generation_attempts WHERE id=? AND task_id=?`, attemptID, id)
	if err != nil {
		return result, err
	}
	if len(attempts) != 1 || attempts[0]["state"] != "FAILED" {
		return result, generation.RepositoryConflict
	}
	claims, err := recoveryRows(ctx, q, `SELECT * FROM generation_artifact_claims WHERE attempt_id=? ORDER BY claim_id`, attemptID)
	if err != nil {
		return result, err
	}
	assets, err := recoveryRows(ctx, q, `SELECT * FROM generation_assets WHERE task_id=? ORDER BY id`, id)
	if err != nil {
		return result, err
	}
	result.RegisteredAssetCount = len(assets)
	raw := recoveryJSONBytes(task["offering_json"])
	if len(raw) == 0 || json.Unmarshal(raw, &result.Offering) != nil {
		return result, generation.RepositoryConflict
	}
	if !bytes.Equal(recoveryJSONBytes(attempts[0]["offering_json"]), raw) {
		return result, generation.RepositoryConflict
	}
	sum := sha256.Sum256(raw)
	// Local delivery completion provenance does not describe the provider
	// bytes or the recovery receipt's source snapshot; keep 0054 receipts
	// bound across 0069 by omitting this local-only attempt column.
	attemptSnapshot := make(map[string]any, len(attempts[0]))
	for key, value := range attempts[0] {
		if key == "delivery_completion_json" {
			continue
		}
		attemptSnapshot[key] = value
	}
	snapshot, err := generation.RecoveryDigest(map[string]any{"task": recoverySourceTaskSnapshot(task), "attempt": attemptSnapshot, "claims": claims, "assets": assets})
	if err != nil {
		return result, err
	}
	session, ok := task["session_id"].(string)
	if !ok || session == "" {
		return result, generation.RepositoryConflict
	}
	result.Binding = generation.RecoverySourceBinding{OwnerID: owner, SessionID: session, TaskID: id, AttemptID: generation.AttemptID(attemptID), OfferingID: result.Offering.ID, OfferingRevision: result.Offering.Revision, OfferingSHA256: hex.EncodeToString(sum[:]), SnapshotSHA256: snapshot}
	return result, nil
}
func (r *Repository) ReadRecoverySource(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.RecoverySource, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return generation.RecoverySource{}, err
	}
	defer tx.Rollback()
	source, err := readRecoverySource(ctx, tx, owner, id)
	if err != nil {
		return source, err
	}
	return source, tx.Commit()
}

// ReadRecoverySourceFile is the collector's read-only preview. Unlike Open it
// never creates a database, configures journaling or runs migrations.
func ReadRecoverySourceFile(ctx context.Context, path string, owner generation.OwnerID, id generation.TaskID) (generation.RecoverySource, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return generation.RecoverySource{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return generation.RecoverySource{}, err
	}
	if !info.Mode().IsRegular() {
		return generation.RecoverySource{}, generation.RepositoryConflict
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return generation.RecoverySource{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return generation.RecoverySource{}, err
	}
	defer tx.Rollback()
	source, err := readRecoverySource(ctx, tx, owner, id)
	if err != nil {
		return source, err
	}
	return source, tx.Commit()
}
func decodeRecoveryReceipt(raw, id, digest string) (generation.RecoveryReceipt, error) {
	var receipt generation.RecoveryReceipt
	if len(raw) > 256<<10 || json.Unmarshal([]byte(raw), &receipt) != nil || receipt.ID != id || receipt.Digest != digest || receipt.Validate() != nil {
		return generation.RecoveryReceipt{}, generation.RepositoryConflict
	}
	return receipt, nil
}
func (r *Repository) GetRecoveryReceipt(ctx context.Context, owner generation.OwnerID, id string) (generation.RecoveryReceipt, error) {
	var raw, digest string
	err := r.db.QueryRowContext(ctx, `SELECT payload_json,digest FROM generation_recovery_receipts WHERE owner_id=? AND id=?`, owner, id).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.RecoveryReceipt{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.RecoveryReceipt{}, err
	}
	receipt, err := decodeRecoveryReceipt(raw, id, digest)
	if err != nil || receipt.Source.OwnerID != owner {
		return generation.RecoveryReceipt{}, generation.RepositoryConflict
	}
	return receipt, nil
}
func (r *Repository) FindRecoveryReceipt(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.RecoveryReceipt, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM generation_recovery_receipts WHERE owner_id=? AND source_task_id=?`, owner, task).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.RecoveryReceipt{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.RecoveryReceipt{}, err
	}
	return r.GetRecoveryReceipt(ctx, owner, id)
}

// CreateRecoveryReceipt is only called by the trusted server-side collector.
// The transaction rechecks the entire unchanged source and inserts a receipt;
// it cannot update a historical Task, Attempt, asset, claim, event or usage row.
func (r *Repository) CreateRecoveryReceipt(ctx context.Context, receipt generation.RecoveryReceipt, offering generation.Offering) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	source, err := readRecoverySource(ctx, tx, receipt.Source.OwnerID, receipt.Source.TaskID)
	if err != nil {
		return err
	}
	if err := generation.ValidateRecoveryReceipt(receipt, source, offering); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM generation_recovery_receipts WHERE source_task_id=? AND source_attempt_id=?`, receipt.Source.TaskID, receipt.Source.AttemptID).Scan(&existing)
	if err == nil {
		if existing != receipt.Digest {
			return generation.RepositoryConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO generation_recovery_receipts(id,owner_id,session_id,source_task_id,source_attempt_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, receipt.ID, receipt.Source.OwnerID, receipt.Source.SessionID, receipt.Source.TaskID, receipt.Source.AttemptID, receipt.Digest, string(raw), formatTime(r.clock()))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) RecoveryTaskBinding(ctx context.Context, id generation.TaskID, attempt generation.AttemptID) (generation.Task, error) {
	aggregate, err := r.Load(ctx, id)
	if err != nil {
		return generation.Task{}, err
	}
	if aggregate.Task.CurrentAttemptID != attempt || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.ID != attempt {
		return generation.Task{}, generation.RepositoryConflict
	}
	return aggregate.Task, nil
}

// CreateRecoveryReceiptFile attaches a receipt only to an already deployed
// schema. It cannot create a database, migrate it, or change its journal mode.
func CreateRecoveryReceiptFile(ctx context.Context, path string, receipt generation.RecoveryReceipt, offering generation.Offering) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return generation.RepositoryConflict
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var checksum string
	if err := db.QueryRowContext(ctx, `SELECT checksum FROM go_schema_migrations WHERE version=54`).Scan(&checksum); err != nil {
		return err
	}
	if checksum != migrationChecksum(assetRecoveryReceiptsMigration) {
		return generation.RepositoryConflict
	}
	repository := &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}
	return repository.CreateRecoveryReceipt(ctx, receipt, offering)
}

// CheckRecoveryReceiptTargetFile preflights the deployed migration read-only,
// before the collector writes any bytes into the live CAS.
func CheckRecoveryReceiptTargetFile(ctx context.Context, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return generation.RepositoryConflict
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var checksum string
	if err = db.QueryRowContext(ctx, `SELECT checksum FROM go_schema_migrations WHERE version=54`).Scan(&checksum); err != nil {
		return err
	}
	if checksum != migrationChecksum(assetRecoveryReceiptsMigration) {
		return generation.RepositoryConflict
	}
	return nil
}
