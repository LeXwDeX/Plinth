package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestRecoveryLegacySourceSnapshotSurvivesNativePricingMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-54.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE go_schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	r := &Repository{db: db}
	all := migrations()
	cut := -1
	for i, m := range all {
		if m.version == 55 {
			cut = i
			break
		}
	}
	if cut < 0 {
		t.Fatal("missing real 0055 migration")
	}
	if err = r.runMigrations(ctx, all[:cut]); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('session','owner','Owner','source','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES ('task','owner','session','text-to-3d','{"id":"original","revision":"r1"}','{}','hash','FAILED','attempt',1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('attempt','task',1,'{"id":"original","revision":"r1"}','FAILED','once','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before, err := r.ReadRecoverySource(ctx, "owner", "task")
	if err != nil {
		t.Fatal(err)
	}
	// Persist a sealed 0054 receipt before the real upgrade; its digest and payload must survive unchanged.
	receipt := recoveryRetentionReceipt(t, []byte("pre-pricing staged bytes"))
	receipt.Source = before.Binding
	receipt, err = generation.SealRecoveryReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO generation_recovery_receipts(id,owner_id,session_id,source_task_id,source_attempt_id,digest,payload_json,created_at) VALUES (?,'owner','session','task','attempt',?,?,'2020-01-01T00:00:00Z')`, receipt.ID, receipt.Digest, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = r.runMigrations(ctx, all[cut:]); err != nil {
		t.Fatal(err)
	}
	after, err := r.ReadRecoverySource(ctx, "owner", "task")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := r.GetRecoveryReceipt(ctx, "owner", receipt.ID)
	if err != nil || !reflect.DeepEqual(loaded, receipt) || loaded.Source != after.Binding {
		t.Fatalf("0054 receipt changed or no longer binds its source: %v", err)
	}
	if before.Binding != after.Binding {
		t.Fatalf("legacy recovery source binding changed solely by 0055: %s -> %s", before.Binding.SnapshotSHA256, after.Binding.SnapshotSHA256)
	}
}

func TestRecoveryNativePricingEvidenceStaysInSourceDigest(t *testing.T) {
	legacy := map[string]any{"id": "task", "native_quote_json": nil, "native_quote_required": int64(0)}
	if got := recoverySourceTaskSnapshot(legacy); len(got) != 1 || got["id"] != "task" || len(legacy) != 3 {
		t.Fatal("legacy defaults must be omitted without mutating the original row")
	}
	for _, task := range []map[string]any{
		{"id": "task", "native_quote_required": int64(1), "native_quote_json": `{"total_amount":"0.5"}`},
		{"id": "task", "native_quote_required": int64(1), "native_quote_json": nil},
		{"id": "task", "native_quote_required": int64(0), "native_quote_json": `null`},
		{"id": "task", "native_quote_required": "invalid", "native_quote_json": nil},
	} {
		if !reflect.DeepEqual(task, recoverySourceTaskSnapshot(task)) {
			t.Fatal("native or malformed quote evidence was dropped")
		}
	}
}
