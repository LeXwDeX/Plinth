package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestAssetDeliveryProfileMigrationPreservesExistingLegacyRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE go_schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	previous := migrations()
	r := &Repository{db: db}
	profileUpgrade := -1
	for i, migration := range previous {
		if migration.version == 52 {
			profileUpgrade = i
			break
		}
	}
	if profileUpgrade < 1 {
		t.Fatal("test requires the 0052 profile upgrade")
	}
	if err = r.runMigrations(ctx, previous[:profileUpgrade]); err != nil {
		t.Fatal(err)
	}
	o := generation.Offering{ID: "legacy", ProviderID: "legacy", ModelID: "legacy", Capability: generation.TextTo3D, Domain: generation.DomainModel3D}
	raw, _ := json.Marshal(o)
	stamp := "2026-10-15T00:00:00Z"
	if _, err = db.Exec(`INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('legacy','owner','text-to-3d',?,'{}','hash','SUCCEEDED',1,?,?)`, string(raw), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('a','legacy',1,?,'UPSTREAM_SUCCEEDED','submit',?,?)`, string(raw), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES ('asset','legacy','a','model',1,1,'blobs/legacy','application/octet-stream',1,'legacy',?)`, stamp); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	task, err := reopened.Get(ctx, "owner", "legacy")
	if err != nil || len(task.Assets) != 1 || task.Assets[0].DeliveryProfile != "" || task.Assets[0].SHA256 != "legacy" {
		t.Fatalf("legacy row changed: %+v %v", task.Assets, err)
	}
}
func TestAssetDeliveryProfileImportPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "import.db")
	r, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	o := generation.Offering{ID: "import", ProviderID: "test", ModelID: "model", Capability: generation.TextTo3D, Domain: generation.DomainModel3D}
	task := generation.Task{ID: "task", OwnerID: "owner", Capability: o.Capability, Offering: o, Input: json.RawMessage(`{}`), State: generation.TaskSucceeded, Version: 1, CreatedAt: now, UpdatedAt: now}
	item := GenerationTaskImport{Task: task, Attempts: []GenerationAttemptImport{{ID: "attempt", Ordinal: 1, Offering: o, State: generation.AttemptUpstreamSucceeded, SubmissionKey: "import", StartedAt: now, UpdatedAt: now}}}
	asset := generation.Asset{ID: "asset", TaskID: task.ID, AttemptID: "attempt", Role: "arbitrary", Variant: "3mf", DeliveryProfile: schema.DeliveryProfile3MFPackageV1, StorageKey: "blobs/test", MediaType: "model/3mf", Size: 123, SHA256: "0123456789012345678901234567890123456789012345678901234567890123", CreatedAt: now}
	if _, err = r.ImportGenerationTasksAndAssets(ctx, []GenerationTaskImport{item}, []generation.Asset{asset}); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	restored, err := r.Get(ctx, "owner", task.ID)
	if err != nil || len(restored.Assets) != 1 || restored.Assets[0].DeliveryProfile != asset.DeliveryProfile {
		t.Fatalf("import dropped profile: %+v %v", restored.Assets, err)
	}
}
