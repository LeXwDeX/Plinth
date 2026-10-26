package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	sqliteDriver "modernc.org/sqlite"
)

var ErrRetentionCommitUnknown = errors.New("retention commit outcome is unknown; read the immutable receipt before any further action")

type RetentionSession struct {
	reserved                    bool
	db                          *sql.DB
	conn                        *sql.Conn
	path                        string
	device, inode               uint64
	closed                      bool
	backedUpSnapshot            string
	backupDirectory, backupHash string
}

var _ generation.RetentionSession = (*RetentionSession)(nil)

func retentionDSN(path, mode string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {mode}, "_pragma": {"foreign_keys(1)", "busy_timeout(0)"}}
	u.RawQuery = q.Encode()
	return u.String()
}

// OpenRetentionSession never creates or migrates a source database. BEGIN
// IMMEDIATE excludes every database writer for snapshot, backup and deletion.
// The command also holds the CAS exclusive lease before calling this method.
func OpenRetentionSession(ctx context.Context, path string) (*RetentionSession, error) {
	d, i, e := local.RetentionPathIdentity(path, false)
	if e != nil {
		return nil, e
	}
	if e = validateRetentionSidecars(path); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", retentionDSN(path, "rw"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(2)
	c, e := db.Conn(ctx)
	if e != nil {
		db.Close()
		return nil, e
	}
	s := &RetentionSession{db: db, conn: c, path: path, device: d, inode: i}
	fail := func(err error) (*RetentionSession, error) { s.Close(); return nil, err }
	if _, e = c.ExecContext(ctx, "PRAGMA synchronous = FULL"); e != nil {
		return fail(e)
	}
	if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
		return fail(e)
	}
	s.reserved = true
	if e = s.verifyIdentity(); e != nil {
		return fail(e)
	}
	if e = retentionForeignKeys(ctx, c); e != nil {
		return fail(e)
	}
	if e = verifyRetentionMigrations(ctx, c); e != nil {
		return fail(e)
	}
	return s, nil
}
func (s *RetentionSession) verifyIdentity() error {
	d, i, e := local.RetentionPathIdentity(s.path, false)
	if e != nil {
		return e
	}
	if d != s.device || i != s.inode {
		return generation.ErrRetentionChanged
	}
	return nil
}
func (s *RetentionSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	// ROLLBACK is harmless after a successful COMMIT; never retries mutations.
	s.conn.ExecContext(context.Background(), "ROLLBACK")
	return errors.Join(s.conn.Close(), s.db.Close())
}
func retentionForeignKeys(ctx context.Context, q retentionQuery) error {
	rows, e := q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("retention refuses a foreign-key violation")
	}
	return rows.Err()
}
func retentionIntegrity(ctx context.Context, q retentionQuery) error {
	rows, e := q.QueryContext(ctx, "PRAGMA integrity_check")
	if e != nil {
		return e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var result string
		if e = rows.Scan(&result); e != nil {
			return e
		}
		if result != "ok" {
			return errors.New("retention database integrity check failed")
		}
		n++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if n != 1 {
		return errors.New("retention database integrity result missing")
	}
	return retentionForeignKeys(ctx, q)
}
func verifyRetentionMigrations(ctx context.Context, q retentionQuery) error {
	rows, e := q.QueryContext(ctx, "SELECT version,name,checksum FROM go_schema_migrations ORDER BY version")
	if e != nil {
		return e
	}
	defer rows.Close()
	all := migrations()
	i := 0
	for rows.Next() {
		var version int
		var name, hash string
		if e = rows.Scan(&version, &name, &hash); e != nil {
			return e
		}
		if i >= len(all) || all[i].version != version || all[i].name != name || migrationChecksum(all[i].sql) != hash {
			return errors.New("retention requires the exact current migration ledger")
		}
		i++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if i != len(all) {
		return errors.New("retention database requires migration before offline maintenance")
	}
	return nil
}
func (s *RetentionSession) Inventory(ctx context.Context) (generation.RetentionInventory, error) {
	if e := s.verifyIdentity(); e != nil {
		return generation.RetentionInventory{}, e
	}
	snap, e := readRetentionSnapshot(ctx, s.conn)
	if e != nil {
		return generation.RetentionInventory{}, e
	}
	return inventoryFromRetentionSnapshot(snap)
}

func exclusiveRetentionDatabase(path string) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func copyRetentionDatabase(ctx context.Context, source *sql.Conn, destination string) error {
	if e := exclusiveRetentionDatabase(destination); e != nil {
		return e
	}
	return source.Raw(func(driverConn any) error {
		creator, ok := driverConn.(interface {
			NewBackup(string) (*sqliteDriver.Backup, error)
		})
		if !ok {
			return errors.New("SQLite backup API unavailable")
		}
		b, e := creator.NewBackup(retentionDSN(destination, "rw"))
		if e != nil {
			return e
		}
		var stepErr error
		for {
			if stepErr = ctx.Err(); stepErr != nil {
				break
			}
			more, e := b.Step(128)
			if e != nil {
				stepErr = e
				break
			}
			if !more {
				break
			}
		}
		return errors.Join(stepErr, b.Finish())
	})
}
func openRetentionReadDB(ctx context.Context, path string) (*sql.DB, *sql.Conn, error) {
	if _, _, e := local.RetentionPathIdentity(path, false); e != nil {
		return nil, nil, e
	}
	if e := validateRetentionSidecars(path); e != nil {
		return nil, nil, e
	}
	db, e := sql.Open("sqlite", retentionDSN(path, "ro"))
	if e != nil {
		return nil, nil, e
	}
	db.SetMaxOpenConns(1)
	c, e := db.Conn(ctx)
	if e != nil {
		db.Close()
		return nil, nil, e
	}
	return db, c, nil
}
func hashRetentionDatabase(path string) (string, error) {
	if _, _, e := local.RetentionPathIdentity(path, false); e != nil {
		return "", e
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	if e = f.Sync(); e != nil {
		return "", e
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, 1<<31))
	if e != nil {
		return "", e
	}
	if n == 1<<31 {
		return "", errors.New("backup database exceeds verification bound")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BackupAndVerify uses an independent read connection while this session owns
// BEGIN IMMEDIATE. SQLite forbids backup_step on the writer connection itself.
// It then restores into a second, fresh database and compares every typed row,
// including audit/approval records, followed by integrity and FK checks.
func (s *RetentionSession) BackupAndVerify(ctx context.Context, directory string) (string, error) {
	if s.backedUpSnapshot != "" {
		return "", errors.New("this retention session already created a backup")
	}
	if e := s.verifyIdentity(); e != nil {
		return "", e
	}
	if _, _, e := local.RetentionPathIdentity(directory, true); e != nil {
		return "", e
	}
	before, e := readRetentionSnapshot(ctx, s.conn)
	if e != nil {
		return "", e
	}
	if e = retentionIntegrity(ctx, s.conn); e != nil {
		return "", e
	}
	readDB, reader, e := openRetentionReadDB(ctx, s.path)
	if e != nil {
		return "", e
	}
	backup := filepath.Join(directory, "database.sqlite")
	e = copyRetentionDatabase(ctx, reader, backup)
	reader.Close()
	readDB.Close()
	if e != nil {
		return "", e
	}
	backupDB, backupReader, e := openRetentionReadDB(ctx, backup)
	if e != nil {
		return "", e
	}
	backupSnapshot, e := readRetentionSnapshot(ctx, backupReader)
	if e == nil && backupSnapshot.CompleteHash != before.CompleteHash {
		e = generation.ErrRetentionChanged
	}
	if e == nil {
		e = retentionIntegrity(ctx, backupReader)
	}
	restore := filepath.Join(directory, "restore-verified.sqlite")
	if e == nil {
		e = copyRetentionDatabase(ctx, backupReader, restore)
	}
	backupReader.Close()
	backupDB.Close()
	if e != nil {
		return "", e
	}
	restoreDB, restored, e := openRetentionReadDB(ctx, restore)
	if e != nil {
		return "", e
	}
	restoredSnapshot, e := readRetentionSnapshot(ctx, restored)
	if e == nil && restoredSnapshot.CompleteHash != before.CompleteHash {
		e = errors.New("retention restore round-trip changed logical database content")
	}
	if e == nil {
		e = retentionIntegrity(ctx, restored)
	}
	restored.Close()
	restoreDB.Close()
	if e != nil {
		return "", e
	}
	if _, e = hashRetentionDatabase(restore); e != nil {
		return "", e
	}
	hash, e := hashRetentionDatabase(backup)
	if e != nil {
		return "", e
	}
	if e = local.SyncRetentionDirectory(directory); e != nil {
		return "", e
	}
	// The writer reservation has remained held throughout both copies.
	after, e := readRetentionSnapshot(ctx, s.conn)
	if e != nil {
		return "", e
	}
	if after.CompleteHash != before.CompleteHash {
		return "", generation.ErrRetentionChanged
	}
	s.backedUpSnapshot = before.CompleteHash
	s.backupDirectory = directory
	s.backupHash = hash
	return hash, nil
}

func (s *RetentionSession) Receipt(ctx context.Context, hash string) (generation.RetentionReceipt, error) {
	return readRetentionReceipt(ctx, s.conn, hash)
}
func readRetentionReceipt(ctx context.Context, q retentionQuery, hash string) (generation.RetentionReceipt, error) {
	var out generation.RetentionReceipt
	rows, e := q.QueryContext(ctx, "SELECT receipt_json FROM generation_retention_receipts WHERE manifest_sha256 = ?", hash)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return out, e
		}
		return out, generation.ErrRetentionReceiptNotFound
	}
	var b string
	if e = rows.Scan(&b); e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(b), &out); e != nil {
		return out, e
	}
	return out, rows.Err()
}
func ReadRetentionReceipt(ctx context.Context, database, hash string) (generation.RetentionReceipt, error) {
	db, c, e := openRetentionReadDB(ctx, database)
	if e != nil {
		return generation.RetentionReceipt{}, e
	}
	defer db.Close()
	defer c.Close()
	return readRetentionReceipt(ctx, c, hash)
}

func (s *RetentionSession) DeleteAndRecord(ctx context.Context, m generation.RetentionManifest, receipt generation.RetentionReceipt) (generation.RetentionReceipt, error) {
	hash, e := m.Digest()
	if e != nil {
		return receipt, e
	}
	if receipt.ManifestHash != hash || receipt.Approval.ManifestHash != hash || receipt.Approval.Purpose != "apply" || receipt.Approval.Actor == "" || receipt.Approval.Nonce == "" || receipt.Approval.ApprovedAt == "" {
		return receipt, errors.New("exact manifest human approval is required")
	}
	if s.backedUpSnapshot == "" || receipt.BackupDatabaseHash != s.backupHash || receipt.BackupDirectory != s.backupDirectory {
		return receipt, errors.New("fresh verified backup is required")
	}
	if _, e = s.Receipt(ctx, hash); !errors.Is(e, generation.ErrRetentionReceiptNotFound) {
		if e != nil {
			return receipt, e
		}
		return receipt, errors.New("retention manifest already applied; read its receipt")
	}
	before, e := readRetentionSnapshot(ctx, s.conn)
	if e != nil {
		return receipt, e
	}
	if before.Hash != m.SnapshotHash || before.Schema != m.SchemaHash || before.CompleteHash != s.backedUpSnapshot {
		return receipt, generation.ErrRetentionChanged
	}
	in, e := inventoryFromRetentionSnapshot(before)
	if e != nil {
		return receipt, e
	}
	cutoff, e := time.Parse(time.RFC3339Nano, m.Cutoff)
	if e != nil {
		return receipt, e
	}
	plan, e := generation.PlanRetention(in, m.Policy, cutoff, time.Now())
	if e != nil {
		return receipt, e
	}
	if !reflect.DeepEqual(plan.Tasks, m.Tasks) || !reflect.DeepEqual(plan.Inputs, m.Inputs) {
		return receipt, generation.ErrRetentionChanged
	}
	if e = s.verifyIdentity(); e != nil {
		return receipt, e
	}
	if e = retentionForeignKeys(ctx, s.conn); e != nil {
		return receipt, e
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = s.conn.ExecContext(context.Background(), "ROLLBACK")
			s.reserved = false
		}
	}()
	for _, id := range m.Tasks {
		if _, e = s.conn.ExecContext(ctx, "DELETE FROM generation_assets WHERE task_id = ?", id); e != nil {
			return receipt, e
		}
		result, err := s.conn.ExecContext(ctx, "DELETE FROM generation_tasks WHERE id = ?", id)
		if err != nil {
			return receipt, err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return receipt, errors.New("retention task deletion count changed")
		}
	}
	for _, id := range m.Inputs {
		result, err := s.conn.ExecContext(ctx, "DELETE FROM generation_input_assets WHERE id = ?", id)
		if err != nil {
			return receipt, err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return receipt, errors.New("retention input deletion count changed")
		}
	}
	if e = retentionForeignKeys(ctx, s.conn); e != nil {
		return receipt, e
	}
	after, e := readRetentionSnapshot(ctx, s.conn)
	if e != nil {
		return receipt, e
	}
	receipt.AfterSnapshotHash = after.Hash
	receipt.CommittedAt = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, e := json.Marshal(receipt)
	if e != nil {
		return receipt, e
	}
	if _, e = s.conn.ExecContext(ctx, "INSERT INTO generation_retention_receipts (manifest_sha256,approval_nonce,receipt_json) VALUES (?,?,?)", hash, receipt.Approval.Nonce, string(encoded)); e != nil {
		return receipt, e
	}
	if _, e = s.conn.ExecContext(ctx, "COMMIT"); e != nil {
		return receipt, fmt.Errorf("%w: %v", ErrRetentionCommitUnknown, e)
	}
	s.reserved = false
	committed = true
	return receipt, nil
}

func (s *RetentionSession) ReserveCleanup(ctx context.Context) error {
	if s.reserved {
		return nil
	}
	if _, err := s.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("database committed; cannot reserve cleanup: %w", err)
	}
	s.reserved = true
	return s.verifyIdentity()
}

func validateRetentionSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, _, e := local.RetentionPathIdentity(path+suffix, false); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
