package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func recoveryRetentionRow(table string, values map[string]string) retentionRow {
	r := retentionRow{Table: table}
	for k := range values {
		r.Columns = append(r.Columns, k)
	}
	sort.Strings(r.Columns)
	for _, k := range r.Columns {
		r.Cells = append(r.Cells, retentionCell{Type: "text", Value: values[k]})
	}
	return r
}
func recoveryRetentionReceipt(t *testing.T, data []byte) generation.RecoveryReceipt {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	receipt, err := generation.SealRecoveryReceipt(generation.RecoveryReceipt{
		Source:        generation.RecoverySourceBinding{OwnerID: "owner", SessionID: "source-session", TaskID: "source-task", AttemptID: "source-attempt", OfferingID: "original", OfferingRevision: "r1", OfferingSHA256: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("b", 64)},
		ArchiveSHA256: strings.Repeat("c", 64), ManifestSHA256: strings.Repeat("d", 64), OfferingID: "local/recovery", OfferingRevision: "r1", OfferingSHA256: strings.Repeat("e", 64),
		Slots: []generation.RecoverySlot{{SourceRole: "model", SourceVariant: "glb", Role: "model", Variant: "glb", Required: true, StorageKey: "blobs/" + digest[:2] + "/" + digest + ".bin", SHA256: digest, Size: int64(len(data)), MediaType: "application/octet-stream"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func recoveryRetentionSnapshot(t *testing.T) (retentionSnapshot, generation.RecoveryReceipt) {
	t.Helper()
	receipt := recoveryRetentionReceipt(t, []byte("staged receipt-only CAS bytes"))
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return retentionSnapshot{Rows: []retentionRow{
		recoveryRetentionRow("generation_tasks", map[string]string{"id": "source-task", "owner_id": "owner", "session_id": "source-session", "state": "FAILED", "finished_at": "2020-01-01T00:00:00Z", "resource_state_json": "{}"}),
		recoveryRetentionRow("generation_attempts", map[string]string{"id": "source-attempt", "task_id": "source-task", "state": "FAILED"}),
		recoveryRetentionRow("generation_recovery_receipts", map[string]string{"id": receipt.ID, "digest": receipt.Digest, "owner_id": "owner", "session_id": "source-session", "source_task_id": "source-task", "source_attempt_id": "source-attempt", "payload_json": string(raw)}),
	}}, receipt
}
func setRecoveryRetentionCell(row *retentionRow, column, value string) {
	for i, c := range row.Columns {
		if c == column {
			row.Cells[i].Value = value
			return
		}
	}
	panic("missing fixture column")
}
func TestRetentionRecoveryReceiptOnlyCASProtectedAndBackedUp(t *testing.T) {
	snapshot, receipt := recoveryRetentionSnapshot(t)
	in, err := inventoryFromRetentionSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Blobs) != 1 || in.Blobs[0].File.Key != receipt.Slots[0].StorageKey {
		t.Fatal("receipt-only CAS omitted")
	}
	foundExternal := false
	for _, g := range in.Blobs[0].Groups {
		foundExternal = foundExternal || g == ""
	}
	if !foundExternal {
		t.Fatal("receipt CAS must have an external protected reference")
	}
	m, err := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) != 0 || len(m.Retained) != 1 || len(m.Files) != 0 || len(m.BackupFiles) != 1 {
		t.Fatalf("receipt or source reference lost: %+v", m)
	}
	if !reflect.DeepEqual(m.BackupFiles[0], in.Blobs[0].File) {
		t.Fatal("backup identity changed")
	}
}
func TestRetentionRecoveryReceiptSharedCASAndGenericJSONReferences(t *testing.T) {
	snapshot, receipt := recoveryRetentionSnapshot(t)
	slot := receipt.Slots[0]
	snapshot.Rows = append(snapshot.Rows,
		recoveryRetentionRow("generation_input_assets", map[string]string{"id": "shared-input", "storage_key": slot.StorageKey, "sha256": slot.SHA256, "size_bytes": strconv.FormatInt(slot.Size, 10), "created_at": "2020-01-01T00:00:00Z"}),
		recoveryRetentionRow("generation_tasks", map[string]string{"id": "additional-source-task", "state": "FAILED", "finished_at": "2020-01-01T00:00:00Z", "resource_state_json": "{}"}),
	)
	// The generic scan must still see all receipt JSON, including nested fields not projected by its typed decoder.
	var raw map[string]any
	if err := json.Unmarshal([]byte(snapshot.Rows[2].text("payload_json")), &raw); err != nil {
		t.Fatal(err)
	}
	raw["extra_reference"] = map[string]any{"task_id": "additional-source-task"}
	payload, _ := json.Marshal(raw)
	setRecoveryRetentionCell(&snapshot.Rows[2], "payload_json", string(payload))
	in, err := inventoryFromRetentionSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m, err := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) != 0 || len(m.Files) != 0 || len(m.BackupFiles) != 1 || !reflect.DeepEqual(m.Inputs, []string{"shared-input"}) {
		t.Fatalf("shared CAS or generic source protection lost: %+v", m)
	}
	raw["extra_reference"] = map[string]any{"storage_key": "blobs/unregistered"}
	payload, _ = json.Marshal(raw)
	setRecoveryRetentionCell(&snapshot.Rows[2], "payload_json", string(payload))
	if _, err = inventoryFromRetentionSnapshot(snapshot); err == nil {
		t.Fatal("receipt bypassed generic unregistered CAS rejection")
	}
}

func TestRetentionRecoveryReceiptRejectsTamperBindingAndCASConflicts(t *testing.T) {
	for _, column := range []string{"id", "digest", "owner_id", "session_id", "source_task_id", "source_attempt_id", "payload_json"} {
		t.Run("receipt_row_"+column, func(t *testing.T) {
			s, _ := recoveryRetentionSnapshot(t)
			setRecoveryRetentionCell(&s.Rows[2], column, "tampered")
			if _, err := inventoryFromRetentionSnapshot(s); err == nil {
				t.Fatal("accepted mismatched receipt row")
			}
		})
	}
	for _, column := range []string{"owner_id", "session_id"} {
		t.Run("source_"+column, func(t *testing.T) {
			s, _ := recoveryRetentionSnapshot(t)
			setRecoveryRetentionCell(&s.Rows[0], column, "different")
			if _, err := inventoryFromRetentionSnapshot(s); err == nil {
				t.Fatal("accepted mismatched source binding")
			}
		})
	}
	t.Run("attempt_belongs_to_another_task", func(t *testing.T) {
		s, _ := recoveryRetentionSnapshot(t)
		s.Rows = append(s.Rows, recoveryRetentionRow("generation_tasks", map[string]string{"id": "other-task", "resource_state_json": "{}"}))
		setRecoveryRetentionCell(&s.Rows[1], "task_id", "other-task")
		if _, err := inventoryFromRetentionSnapshot(s); err == nil {
			t.Fatal("accepted mismatched source attempt")
		}
	})
	for _, column := range []string{"sha256", "size_bytes"} {
		t.Run("conflicting_"+column, func(t *testing.T) {
			s, receipt := recoveryRetentionSnapshot(t)
			slot := receipt.Slots[0]
			row := recoveryRetentionRow("generation_input_assets", map[string]string{"id": "input", "storage_key": slot.StorageKey, "sha256": slot.SHA256, "size_bytes": strconv.FormatInt(slot.Size, 10), "created_at": "2020-01-01T00:00:00Z"})
			if column == "sha256" {
				setRecoveryRetentionCell(&row, column, strings.Repeat("f", 64))
			} else {
				setRecoveryRetentionCell(&row, column, "999")
			}
			s.Rows = append(s.Rows, row)
			if _, err := inventoryFromRetentionSnapshot(s); err == nil {
				t.Fatal("accepted conflicting CAS identity")
			}
		})
	}
	t.Run("payload_identity_changed", func(t *testing.T) {
		s, _ := recoveryRetentionSnapshot(t)
		raw := s.Rows[2].text("payload_json")
		// Change the slot size without resealing the trusted receipt digest.
		setRecoveryRetentionCell(&s.Rows[2], "payload_json", strings.Replace(raw, `"size":29`, `"size":30`, 1))
		if _, err := inventoryFromRetentionSnapshot(s); err == nil {
			t.Fatal("accepted tampered receipt payload")
		}
	})
}

func TestRetentionRecoveryReceiptOnlyCASRealSnapshotBackup(t *testing.T) {
	ctx := context.Background()
	root := retentionTestDir(t)
	dbPath := filepath.Join(root, "source.sqlite")
	cas := filepath.Join(root, "cas")
	data := []byte("staged receipt-only CAS bytes")
	receipt := recoveryRetentionReceipt(t, data)
	slot := receipt.Slots[0]
	if err := os.MkdirAll(filepath.Dir(filepath.Join(cas, slot.StorageKey)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cas, slot.StorageKey), data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	retentionFixtureTask(t, r, "source-task")
	retentionFixtureTask(t, r, "unreferenced-task")
	for _, stmt := range []string{
		`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('source-session','owner','Owner','source','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`UPDATE generation_tasks SET state='FAILED',session_id='source-session',current_attempt_id='source-attempt' WHERE id='source-task'`,
		`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('source-attempt','source-task',1,'{}','FAILED','source-key','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
	} {
		if _, err = r.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(receipt)
	if _, err = r.db.Exec(`INSERT INTO generation_recovery_receipts(id,owner_id,session_id,source_task_id,source_attempt_id,digest,payload_json,created_at) VALUES (?,'owner','source-session','source-task','source-attempt',?,?,'2020-01-01T00:00:00Z')`, receipt.ID, receipt.Digest, string(raw)); err != nil {
		t.Fatal(err)
	}
	r.Close()
	files, err := local.OpenRetentionFiles(cas)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	session, err := OpenRetentionSession(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	d, i, err := local.RetentionPathIdentity(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	svc := generation.RetentionService{Repository: session, Files: files, Policy: retentionTestPolicy(), Approver: &retentionFixtureApprover{}, Identity: generation.RetentionIdentity{Database: dbPath, Storage: cas, DatabaseDevice: d, DatabaseInode: i, StorageDevice: files.Device, StorageInode: files.Inode}}
	m, err := svc.Prepare(ctx, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Tasks, []string{"unreferenced-task"}) || len(m.Files) != 0 || len(m.BackupFiles) != 1 {
		t.Fatalf("unexpected plan %+v", m)
	}
	backup := filepath.Join(root, "backup")
	result, err := svc.Apply(ctx, m, backup)
	if err != nil || !result.DatabaseCommitted {
		t.Fatalf("isolated apply: %+v %v", result, err)
	}
	for _, p := range []string{filepath.Join(cas, slot.StorageKey), filepath.Join(backup, slot.StorageKey)} {
		got, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(got, data) {
			t.Fatalf("staged or backup bytes lost: %v", e)
		}
	}
	a, _ := os.Stat(filepath.Join(cas, slot.StorageKey))
	b, _ := os.Stat(filepath.Join(backup, slot.StorageKey))
	if os.SameFile(a, b) {
		t.Fatal("backup shares staged source inode")
	}
	session.Close()
	for _, db := range []string{dbPath, filepath.Join(backup, "restore-verified.sqlite")} {
		check, conn, e := openRetentionReadDB(ctx, db)
		if e != nil {
			t.Fatal(e)
		}
		var payload string
		e = conn.QueryRowContext(ctx, `SELECT payload_json FROM generation_recovery_receipts WHERE id=?`, receipt.ID).Scan(&payload)
		conn.Close()
		check.Close()
		if e != nil || payload != string(raw) {
			t.Fatalf("receipt lost from source/restore: %v", e)
		}
	}
}
