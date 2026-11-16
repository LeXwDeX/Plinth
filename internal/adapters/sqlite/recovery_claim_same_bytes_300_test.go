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

// #300: migration 0093 adds generation_artifact_claims.same_bytes_as. A recovery
// receipt seals a digest over the claim rows of its failed source, so the upgrade
// must not change the digest of a row that declares nothing.

func TestRecoveryLegacySourceSnapshotSurvivesClaimSameBytesMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-92.db")
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
		if m.version == 93 {
			cut = i
			break
		}
	}
	if cut < 0 {
		t.Fatal("missing real 0093 migration")
	}
	if err = r.runMigrations(ctx, all[:cut]); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('session','owner','Owner','source','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES ('task','owner','session','text-to-3d','{"id":"original","revision":"r1"}','{}','hash','FAILED','attempt',1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('attempt','task',1,'{"id":"original","revision":"r1"}','FAILED','once','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`,
		`INSERT INTO generation_artifact_claims(attempt_id,claim_id,role,variant,locator,media_type) VALUES ('attempt','claim-model','model','','https://assets.example.test/a.glb','model/gltf-binary')`,
		`INSERT INTO generation_artifact_claims(attempt_id,claim_id,role,variant,locator,media_type) VALUES ('attempt','claim-gltf','converted_model','converted.gltf','https://assets.example.test/a.glb','model/gltf-binary')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before, err := r.ReadRecoverySource(ctx, "owner", "task")
	if err != nil {
		t.Fatal(err)
	}
	// Seal a receipt on the 0092 shape; its digest and payload must survive the upgrade.
	receipt := recoveryRetentionReceipt(t, []byte("pre-same-bytes staged bytes"))
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
		t.Fatalf("0092 receipt changed or no longer binds its source: %v", err)
	}
	if before.Binding != after.Binding {
		t.Fatalf("recovery source binding changed solely by 0093: %s -> %s", before.Binding.SnapshotSHA256, after.Binding.SnapshotSHA256)
	}

	// A declaration made after the upgrade is evidence like any other claim column.
	if _, err = db.Exec(`UPDATE generation_artifact_claims SET same_bytes_as = 'claim-gltf' WHERE claim_id = 'claim-model'`); err != nil {
		t.Fatal(err)
	}
	declared, err := r.ReadRecoverySource(ctx, "owner", "task")
	if err != nil {
		t.Fatal(err)
	}
	if declared.Binding.SnapshotSHA256 == after.Binding.SnapshotSHA256 {
		t.Fatal("a declared same_bytes_as left the source digest unchanged")
	}
}

func TestRecoverySourceClaimsOmitOnlyTheEmptyDeclaration(t *testing.T) {
	legacy := map[string]any{"claim_id": "a", "same_bytes_as": ""}
	null := map[string]any{"claim_id": "b", "same_bytes_as": nil}
	absent := map[string]any{"claim_id": "c"}
	declared := map[string]any{"claim_id": "d", "same_bytes_as": "a"}
	got := recoverySourceClaims([]map[string]any{legacy, null, absent, declared})
	want := []map[string]any{{"claim_id": "a"}, {"claim_id": "b"}, {"claim_id": "c"}, {"claim_id": "d", "same_bytes_as": "a"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot rows = %v, want %v", got, want)
	}
	if _, kept := legacy["same_bytes_as"]; !kept {
		t.Fatal("the original row was mutated")
	}
}
