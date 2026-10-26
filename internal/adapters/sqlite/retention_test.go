package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func retentionTestDir(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func retentionTestPolicy() schema.TaskRetentionPolicy {
	return schema.TaskRetentionPolicy{Enabled: true, MinimumAgeSeconds: 1, TaskStates: []string{"SUCCEEDED", "FAILED", "CANCELED"}, TaskTimeField: "finished_at", InputTimeField: "created_at", ReferenceMode: "preserve_all"}
}
func retentionFixtureTask(t *testing.T, r *Repository, id string) {
	t.Helper()
	_, e := r.db.Exec(`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at,finished_at) VALUES (?,'owner','text-to-3d','{"id":"offering","parameters":[]}','{}','hash','SUCCEEDED',1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`, id)
	if e != nil {
		t.Fatal(e)
	}
}

func TestRetentionBackupRoundTripAndAtomicReceipt(t *testing.T) {
	for _, journal := range []string{"WAL", "DELETE"} {
		t.Run(journal, func(t *testing.T) {
			ctx := context.Background()
			root := retentionTestDir(t)
			path := filepath.Join(root, "source.sqlite")
			r, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			retentionFixtureTask(t, r, "retention-delete-task")
			retentionFixtureTask(t, r, "retention-keep-task")
			_, e = r.db.Exec(`INSERT INTO generation_prepared_inputs(id,owner_id,session_id,input_hash,payload_json,created_at) VALUES ('prepared','owner','','hash','{"source":"retention-keep-task"}','2020-01-01T00:00:00Z')`)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.db.Exec("PRAGMA journal_mode = " + journal); e != nil {
				t.Fatal(e)
			}
			r.Close()
			s, e := OpenRetentionSession(ctx, path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			in, e := s.Inventory(ctx)
			if e != nil {
				t.Fatal(e)
			}
			m, e := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(m.Tasks, []string{"retention-delete-task"}) {
				t.Fatalf("task plan: %+v retained=%+v", m.Tasks, m.Retained)
			}
			// Another connection cannot acquire a write reservation during the backup.
			other, e := sql.Open("sqlite", retentionDSN(path, "rw"))
			if e != nil {
				t.Fatal(e)
			}
			defer other.Close()
			if _, e = other.ExecContext(ctx, "UPDATE generation_tasks SET progress=1"); e == nil {
				t.Fatal("writer entered reserved snapshot")
			}
			backup := filepath.Join(root, "backup")
			if e = os.Mkdir(backup, 0700); e != nil {
				t.Fatal(e)
			}
			hash, e := s.BackupAndVerify(ctx, backup)
			if e != nil {
				t.Fatal(e)
			}
			if len(hash) != 64 {
				t.Fatal(hash)
			}
			mh, _ := m.Digest()
			receipt := generation.RetentionReceipt{ManifestHash: mh, Approval: generation.RetentionApproval{ManifestHash: mh, Actor: "test-fixture-human", Nonce: "fixture-nonce", Purpose: "apply", ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}, BackupDirectory: backup, BackupDatabaseHash: hash}
			got, e := s.DeleteAndRecord(ctx, m, receipt)
			if e != nil {
				t.Fatal(e)
			}
			if got.AfterSnapshotHash == m.SnapshotHash || got.CommittedAt == "" {
				t.Fatal("missing committed receipt")
			}
			s.Close()
			read, e := ReadRetentionReceipt(ctx, path, mh)
			if e != nil || !reflect.DeepEqual(read, got) {
				t.Fatalf("receipt mismatch: %+v %v", read, e)
			}
			check, c, e := openRetentionReadDB(ctx, path)
			if e != nil {
				t.Fatal(e)
			}
			defer check.Close()
			defer c.Close()
			var count int
			if e = c.QueryRowContext(ctx, "SELECT count(*) FROM generation_tasks").Scan(&count); e != nil || count != 1 {
				t.Fatalf("remaining tasks=%d error=%v", count, e)
			}
			for _, name := range []string{"database.sqlite", "restore-verified.sqlite"} {
				db, conn, e := openRetentionReadDB(ctx, filepath.Join(backup, name))
				if e != nil {
					t.Fatal(e)
				}
				if e = conn.QueryRowContext(ctx, "SELECT count(*) FROM generation_tasks").Scan(&count); e != nil || count != 2 {
					t.Fatalf("restore count=%d err=%v", count, e)
				}
				conn.Close()
				db.Close()
			}
		})
	}
}

func TestRetentionRefusesMissingBackupAndChangedSnapshot(t *testing.T) {
	ctx := context.Background()
	root := retentionTestDir(t)
	path := filepath.Join(root, "source.sqlite")
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	retentionFixtureTask(t, r, "retention-delete-task")
	r.Close()
	s, e := OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	in, e := s.Inventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m, e := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	hash, _ := m.Digest()
	receipt := generation.RetentionReceipt{ManifestHash: hash, Approval: generation.RetentionApproval{ManifestHash: hash, Actor: "test-fixture-human", Nonce: "fixture", Purpose: "apply", ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if _, e = s.DeleteAndRecord(ctx, m, receipt); e == nil {
		t.Fatal("deletion without backup accepted")
	}
	if _, e = s.Receipt(ctx, hash); !errors.Is(e, generation.ErrRetentionReceiptNotFound) {
		t.Fatal(e)
	}
}

type retentionFixtureApprover struct{ calls int }

func (a *retentionFixtureApprover) Approve(_ context.Context, hash, purpose string) (generation.RetentionApproval, error) {
	a.calls++
	return generation.RetentionApproval{ManifestHash: hash, Actor: "isolated-test-fixture-human", Nonce: fmt.Sprintf("%032x", a.calls), Purpose: purpose, ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

type retentionFailSecondFile struct {
	*local.RetentionFiles
	removes int
}

func (f *retentionFailSecondFile) Remove(ctx context.Context, file generation.RetentionFile) error {
	f.removes++
	if f.removes == 2 {
		return errors.New("fixture unlink failure")
	}
	return f.RetentionFiles.Remove(ctx, file)
}
func TestRetentionServicePartialCleanupRequiresNewApprovalAndKeepsBackup(t *testing.T) {
	ctx := context.Background()
	root := retentionTestDir(t)
	path := filepath.Join(root, "source.sqlite")
	cas := filepath.Join(root, "cas")
	if e := os.Mkdir(cas, 0700); e != nil {
		t.Fatal(e)
	}
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		data := []byte(fmt.Sprintf("independent-retention-fixture-%d", i))
		h := sha256.Sum256(data)
		digest := hex.EncodeToString(h[:])
		key := "blobs/" + digest[:2] + "/" + digest + ".bin"
		if e = os.MkdirAll(filepath.Dir(filepath.Join(cas, key)), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(cas, key), data, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = r.db.Exec(`INSERT INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,'owner',?,'application/octet-stream',?,?,'2020-01-01T00:00:00Z')`, fmt.Sprintf("isolated-input-%d", i), key, len(data), digest); e != nil {
			t.Fatal(e)
		}
	}
	r.Close()
	files, e := local.OpenRetentionFiles(cas)
	if e != nil {
		t.Fatal(e)
	}
	defer files.Close()
	session, e := OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	d, i, e := local.RetentionPathIdentity(path, false)
	if e != nil {
		t.Fatal(e)
	}
	approver := &retentionFixtureApprover{}
	fault := &retentionFailSecondFile{RetentionFiles: files}
	svc := generation.RetentionService{Repository: session, Files: fault, Policy: retentionTestPolicy(), Approver: approver, Identity: generation.RetentionIdentity{Database: path, Storage: cas, DatabaseDevice: d, DatabaseInode: i, StorageDevice: files.Device, StorageInode: files.Inode}}
	m, e := svc.Prepare(ctx, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Inputs) != 2 || len(m.Files) != 2 {
		t.Fatalf("unexpected fixture plan %+v", m)
	}
	backup := filepath.Join(root, "backup")
	result, e := svc.Apply(ctx, m, backup)
	if e == nil || !result.DatabaseCommitted || len(result.Deleted) != 1 || result.Receipt == nil {
		t.Fatalf("partial cleanup: %+v %v", result, e)
	}
	if approver.calls != 1 {
		t.Fatal("missing exact apply approval")
	}
	for _, file := range m.BackupFiles {
		data, e := os.ReadFile(filepath.Join(backup, file.Key))
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != file.SHA256 {
			t.Fatal("backup damaged by source unlink")
		}
	}
	session.Close()
	session, e = OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	svc.Repository = session
	svc.Files = files
	svc.Approver = nil
	if _, e = svc.Cleanup(ctx, m); e == nil {
		t.Fatal("cleanup resumed without independent approval")
	}
	svc.Approver = approver
	// Before destructive recovery, prove every original recovery object is still
	// intact, including the backup of the blob already deleted from the source.
	alreadyDeleted := result.Deleted[0]
	for _, relative := range []string{"database.sqlite", "manifest.json", alreadyDeleted} {
		target := filepath.Join(backup, relative)
		original, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if relative == "manifest.json" {
			mode = 0400
			if err = os.Chmod(target, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if relative == alreadyDeleted {
			err = os.Remove(target)
		} else {
			err = os.WriteFile(target, []byte("damaged backup"), 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.Cleanup(ctx, m)
		if err == nil || len(got.Deleted) != 0 {
			t.Fatalf("cleanup continued without intact %s: %+v %v", relative, got, err)
		}
		for _, file := range m.Files {
			if file.Key != alreadyDeleted {
				if _, err = files.Inspect(ctx, file); err != nil {
					t.Fatalf("remaining source lost: %v", err)
				}
			}
		}
		if err = os.WriteFile(target, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
	}
	result, e = svc.Cleanup(ctx, m)
	if e != nil || len(result.Deleted) != 1 || len(result.AlreadyAbsent) != 1 || approver.calls != 5 {
		t.Fatalf("approved recovery: %+v %v approvals=%d", result, e, approver.calls)
	}
	hash, _ := m.Digest()
	if _, e = ReadRetentionReceipt(ctx, path, hash); e != nil {
		t.Fatal(e)
	}
	// The independent backup retains both records and passes its own source checks.
	session.Close()
	restore, e := OpenRetentionSession(ctx, filepath.Join(backup, "restore-verified.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer restore.Close()
	in, e := restore.Inventory(ctx)
	if e != nil || len(in.Entities) != 2 {
		t.Fatalf("backup inventory: %+v %v", in, e)
	}
}

func TestRetentionAllPersistentReferenceFormsProtectSource(t *testing.T) {
	// Synthetic rows exercise decoder coverage without bypassing any real DB
	// constraints. The actual snapshot/schema and transaction tests are above.
	row := func(table string, values map[string]string) retentionRow {
		r := retentionRow{Table: table}
		for k, v := range values {
			r.Columns = append(r.Columns, k)
			r.Cells = append(r.Cells, retentionCell{Type: "text", Value: v})
		}
		return r
	}
	task := row("generation_tasks", map[string]string{"id": "retention-target-uuid", "state": "SUCCEEDED", "finished_at": "2020-01-01T00:00:00Z", "resource_state_json": "{}"})
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"source_task_id":"retention-target-uuid"}`))
	for name, payload := range map[string]string{
		"canvas":                    `{"asset":{"task_id":"retention-target-uuid"}}`,
		"checkpoint_candidates_212": `{"source_candidates":[{"task_id":"retention-target-uuid"}],"candidate_ids":[]}`,
		"base64_tool_arguments":     `{"arguments":"` + encoded + `"}`,
		"nested_json":               `{"payload":"{\"source_task_id\":\"retention-target-uuid\"}"}`,
		"escaped_url":               `{"url":"/tasks/retention%2Dtarget%2Duuid"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := retentionSnapshot{Rows: []retentionRow{task, row("go_canvas_nodes", map[string]string{"data_json": payload})}}
			in, e := inventoryFromRetentionSnapshot(s)
			if e != nil {
				t.Fatal(e)
			}
			m, e := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if len(m.Tasks) != 0 || len(m.Retained) != 1 {
				t.Fatalf("reference lost: %+v", m)
			}
		})
	}
	for _, payload := range []string{`{bad`, `{"arguments":"unsupported-wire-encoding"}`, `{"source":"retention-target-uuid","source":"other"}`} {
		if _, e := inventoryFromRetentionSnapshot(retentionSnapshot{Rows: []retentionRow{task, row("go_canvas_nodes", map[string]string{"data_json": payload})}}); e == nil {
			t.Fatal("unknown persistent reference encoding accepted")
		}
	}
}

func TestRetentionTransactionFailureRollsBackAllDeletedRows(t *testing.T) {
	ctx := context.Background()
	root := retentionTestDir(t)
	path := filepath.Join(root, "source.sqlite")
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	retentionFixtureTask(t, r, "retention-rollback-task")
	// A nonce previously recorded by another operation must reject this entire
	// SQL transaction, including deletes performed before receipt insertion.
	if _, e = r.db.Exec(`INSERT INTO generation_retention_receipts(manifest_sha256,approval_nonce,receipt_json) VALUES ('0000000000000000000000000000000000000000000000000000000000000000','already-used','{}')`); e != nil {
		t.Fatal(e)
	}
	r.Close()
	s, e := OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	in, e := s.Inventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m, e := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(root, "backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	bh, e := s.BackupAndVerify(ctx, backup)
	if e != nil {
		t.Fatal(e)
	}
	mh, _ := m.Digest()
	_, e = s.DeleteAndRecord(ctx, m, generation.RetentionReceipt{ManifestHash: mh, Approval: generation.RetentionApproval{ManifestHash: mh, Actor: "test-fixture-human", Nonce: "already-used", Purpose: "apply", ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}, BackupDirectory: backup, BackupDatabaseHash: bh})
	if e == nil {
		t.Fatal("reused nonce accepted")
	}
	db, c, e := openRetentionReadDB(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	defer c.Close()
	var count int
	if e = c.QueryRowContext(ctx, "SELECT count(*) FROM generation_tasks WHERE id='retention-rollback-task'").Scan(&count); e != nil || count != 1 {
		t.Fatalf("partial SQL deletion persisted: count=%d err=%v", count, e)
	}
	if _, e = s.Receipt(ctx, mh); !errors.Is(e, generation.ErrRetentionReceiptNotFound) {
		t.Fatal("failed transaction produced receipt", e)
	}
}
func TestRetentionChangedRowsRejectFrozenManifest(t *testing.T) {
	ctx := context.Background()
	root := retentionTestDir(t)
	path := filepath.Join(root, "source.sqlite")
	r, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	retentionFixtureTask(t, r, "retention-stale-task")
	r.Close()
	s, e := OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	in, e := s.Inventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	m, e := generation.PlanRetention(in, retentionTestPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	r, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.db.Exec("UPDATE generation_tasks SET progress=99 WHERE id='retention-stale-task'"); e != nil {
		t.Fatal(e)
	}
	r.Close()
	s, e = OpenRetentionSession(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	backup := filepath.Join(root, "backup")
	os.Mkdir(backup, 0700)
	bh, e := s.BackupAndVerify(ctx, backup)
	if e != nil {
		t.Fatal(e)
	}
	mh, _ := m.Digest()
	_, e = s.DeleteAndRecord(ctx, m, generation.RetentionReceipt{ManifestHash: mh, Approval: generation.RetentionApproval{ManifestHash: mh, Actor: "test-fixture-human", Nonce: "fresh", Purpose: "apply", ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}, BackupDirectory: backup, BackupDatabaseHash: bh})
	if !errors.Is(e, generation.ErrRetentionChanged) {
		t.Fatalf("stale snapshot accepted: %v", e)
	}
}
