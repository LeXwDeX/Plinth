package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type commandFixture struct {
	dbPath, storageRoot, owner, failedID, deliveryID, sessionID string
	offering                                                    generation.Offering
}

func newCommandFixture(t *testing.T) commandFixture {
	t.Helper()
	root := t.TempDir()
	f := commandFixture{
		dbPath: filepath.Join(root, "plinth.db"), storageRoot: filepath.Join(root, "storage"),
		owner: "owner-330", failedID: "task_failed_330", deliveryID: "task_delivery_330", sessionID: "session-330",
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("local/generated-glb/to-gltf")
	if !ok {
		t.Fatal("local glTF conversion Offering missing")
	}
	f.offering = offering
	repository, err := sqlite.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := local.New(f.storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset, err := store.Put(context.Background(), generation.AssetWrite{
		AssetID: "asset_delivery_330", OwnerID: generation.OwnerID(f.owner),
		TaskID: generation.TaskID(f.deliveryID), AttemptID: "attempt_delivery_330",
		Role: "model", Variant: "gltf", Ordinal: 1, MediaType: "application/zip", MaxBytes: 1 << 20,
		Body: bytes.NewReader(gltfPackage(t)),
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	offeringJSON, err := json.Marshal(f.offering)
	if err != nil {
		t.Fatal(err)
	}
	stamp := "2026-10-23T00:00:00Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
			[]any{f.sessionID, f.owner, "Owner", "completion", stamp, stamp}},
		{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,progress,failure_json,current_attempt_id,version,created_at,updated_at,finished_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{f.failedID, f.owner, f.sessionID, string(generation.FormatConversion), string(offeringJSON), `{"source_asset":"asset-input"}`, "hash-a", string(generation.TaskFailed), 0, `{"class":"transport","retryable":true,"detail":"local conversion did not start"}`, "attempt_failed_330", 1, stamp, stamp, stamp}},
		{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,progress,current_attempt_id,version,created_at,updated_at,finished_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{f.deliveryID, f.owner, f.sessionID, string(generation.FormatConversion), string(offeringJSON), `{"source_asset":"asset-input"}`, "hash-b", string(generation.TaskSucceeded), 100, "attempt_delivery_330", 1, stamp, stamp, stamp}},
		{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,failure_json,last_observation_phase,started_at,updated_at,finished_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{"attempt_failed_330", f.failedID, 1, string(offeringJSON), string(generation.AttemptFailed), "failed-key", `{"class":"transport","retryable":true,"detail":"local conversion did not start"}`, string(generation.UpstreamCompleted), stamp, stamp, stamp}},
		{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,last_observation_phase,started_at,updated_at,finished_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			[]any{"attempt_delivery_330", f.deliveryID, 1, string(offeringJSON), string(generation.AttemptUpstreamSucceeded), "delivery-key", string(generation.UpstreamCompleted), stamp, stamp, stamp}},
		{`INSERT INTO generation_assets(id,task_id,attempt_id,role,variant,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at,delivery_profile) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{asset.ID, f.deliveryID, "attempt_delivery_330", "model", "gltf", 0, 1, asset.StorageKey, asset.MediaType, asset.Size, asset.SHA256, stamp, asset.DeliveryProfile}},
		{`INSERT INTO generation_upstream_refetches(task_id,owner_id,session_id,source_task_id,source_attempt_id,ordinal,requested_by,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			[]any{f.deliveryID, f.owner, f.sessionID, f.failedID, "attempt_failed_330", 1, "owner", stamp}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	return f
}

func gltfPackage(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"model.gltf": `{"asset":{"version":"2.0"},"buffers":[{"uri":"model.bin","byteLength":4}]}`,
		"model.bin":  "\x00\x00\x00\x00",
	} {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, path := range files {
		relative, _ := filepath.Rel(root, path)
		result[relative] = fileDigest(t, path)
	}
	return result
}

func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), err
}

func TestDryRunIsReadOnlyAndApplyIsIdempotent(t *testing.T) {
	f := newCommandFixture(t)
	dbBefore := fileDigest(t, f.dbPath)
	storeBefore := treeSnapshot(t, f.storageRoot)
	stdout, err := runCommand(t,
		"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
		"--failed-task", f.failedID, "--delivery-task", f.deliveryID)
	if err != nil {
		t.Fatalf("dry-run = %v", err)
	}
	if !strings.Contains(stdout, `"dry_run":true`) || !strings.Contains(stdout, `"asset_count":1`) {
		t.Fatalf("dry-run output = %s", stdout)
	}
	if fileDigest(t, f.dbPath) != dbBefore || !sameTree(treeSnapshot(t, f.storageRoot), storeBefore) {
		t.Fatal("dry-run changed database or storage")
	}
	if _, err := runCommand(t,
		"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
		"--failed-task", f.failedID, "--delivery-task", f.deliveryID, "--apply"); err != nil {
		t.Fatalf("apply = %v", err)
	}
	repository, err := sqlite.OpenReadOnly(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	completed, err := repository.Get(context.Background(), generation.OwnerID(f.owner), generation.TaskID(f.failedID))
	if err != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) != 1 {
		t.Fatalf("completed = %#v %v", completed, err)
	}
	aggregate, err := repository.Load(context.Background(), generation.TaskID(f.failedID))
	if err != nil || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptDeliveryCompleted {
		t.Fatalf("completion attempt = %#v %v", aggregate.CurrentAttempt, err)
	}
	attemptCount, assetCount := completionCounts(t, f)
	if _, err := runCommand(t,
		"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
		"--failed-task", f.failedID, "--delivery-task", f.deliveryID, "--apply"); err != nil {
		t.Fatalf("repeated apply = %v", err)
	}
	if againAttempts, againAssets := completionCounts(t, f); againAttempts != attemptCount || againAssets != assetCount {
		t.Fatalf("repeated apply changed rows: attempts %d->%d assets %d->%d", attemptCount, againAttempts, assetCount, againAssets)
	}
}

func completionCounts(t *testing.T, f commandFixture) (int, int) {
	t.Helper()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var attempts, assets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_attempts WHERE task_id=?`, f.failedID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_assets WHERE task_id=?`, f.failedID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	return attempts, assets
}

func sameTree(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, digest := range left {
		if right[name] != digest {
			return false
		}
	}
	return true
}

// #330 third follow-ups: two adjacent Plan-retry grants of the same Plan and
// index each carry their own frozen PreparedInput. The command's default dry
// run writes nothing; --apply completes once, keeps the original failed
// Attempt, registers its own assets and issues no paid request.
func TestPlanRetryDryRunAndApplyValidateEachGrantAndWriteOnce(t *testing.T) {
	f := newCommandFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM generation_upstream_refetches WHERE task_id=?`, []any{f.deliveryID}},
		{`UPDATE generation_tasks SET idempotency_key=? WHERE id=?`, []any{"processing:plan-330:0:0", f.failedID}},
		{`UPDATE generation_tasks SET idempotency_key=? WHERE id=?`, []any{"processing:plan-330:0:1", f.deliveryID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	db.Close()

	first := preparePlanFixtureInput(t, f, "plan-fixture-a")
	second := preparePlanFixtureInput(t, f, "plan-fixture-b")
	if first.InputHash == second.InputHash {
		t.Fatal("fixture must prepare the adjacent retries under distinct hashes")
	}

	db, err = sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	grants := []struct {
		request  string
		prepared generation.PreparedInput
		step     string
		taskID   string
	}{
		{"grant-plan-a", first, "0:0", f.failedID},
		{"grant-plan-b", second, "0:1", f.deliveryID},
	}
	for _, grant := range grants {
		if _, err := db.Exec(`INSERT INTO execution_grants (request_id, owner_id, session_id, offering_id,
 offering_revision, prepared_input_id, prepared_input_hash, idempotency_key, operation_id, plan_step_id,
 status, task_id, source, request_version, created_at, updated_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,'bound',?,'human_browser',1,?,?)`,
			grant.request, f.owner, f.sessionID, f.offering.ID, f.offering.Revision,
			grant.prepared.ID, grant.prepared.InputHash, "processing:plan-330:"+grant.step,
			"plan-330", grant.step, grant.taskID, "2026-10-23T00:00:00Z", "2026-10-23T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
		"--failed-task", f.failedID, "--delivery-task", f.deliveryID}
	// Each adjacent grant must validate its own PreparedInput: binding either
	// retry to the other's hash is refused.
	for index, grant := range grants {
		other := grants[1-index]
		if _, err := db.Exec(`UPDATE execution_grants SET prepared_input_hash=? WHERE request_id=?`,
			other.prepared.InputHash, grant.request); err != nil {
			t.Fatal(err)
		}
		if _, err := runCommand(t, args...); err == nil {
			t.Fatalf("dry-run accepted %s bound to the other retry's PreparedInput", grant.request)
		}
		if _, err := db.Exec(`UPDATE execution_grants SET prepared_input_hash=? WHERE request_id=?`,
			grant.prepared.InputHash, grant.request); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	originalAttempt := attemptSnapshot(t, f, "attempt_failed_330")
	tasksBefore := rowCount(t, f, `SELECT COUNT(*) FROM generation_tasks`)
	grantsBefore := rowCount(t, f, `SELECT COUNT(*) FROM execution_grants`)
	dbBefore := fileDigest(t, f.dbPath)
	storeBefore := treeSnapshot(t, f.storageRoot)

	dry, err := runCommand(t, args...)
	if err != nil {
		t.Fatalf("dry-run = %v", err)
	}
	if !strings.Contains(dry, `"dry_run":true`) || !strings.Contains(dry, `"kind":"plan_retry"`) ||
		!strings.Contains(dry, `"original_attempt_id":"attempt_failed_330"`) {
		t.Fatalf("dry-run output = %s", dry)
	}
	if fileDigest(t, f.dbPath) != dbBefore || !sameTree(treeSnapshot(t, f.storageRoot), storeBefore) {
		t.Fatal("dry-run changed database or storage")
	}

	applied, err := runCommand(t, append(args, "--apply")...)
	if err != nil {
		t.Fatalf("apply = %v", err)
	}
	if !strings.Contains(applied, `"dry_run":false`) || !strings.Contains(applied, `"task_updated":true`) ||
		!strings.Contains(applied, `"provider_requests":0`) {
		t.Fatalf("apply output = %s", applied)
	}
	repository, err := sqlite.OpenReadOnly(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.Get(context.Background(), generation.OwnerID(f.owner), generation.TaskID(f.failedID))
	if err != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) != 1 {
		t.Fatalf("completed Task = %#v, %v", completed, err)
	}
	aggregate, err := repository.Load(context.Background(), generation.TaskID(f.failedID))
	if err != nil || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptDeliveryCompleted ||
		aggregate.CurrentAttempt.DeliveryCompletion == nil ||
		aggregate.CurrentAttempt.DeliveryCompletion.Kind != generation.DeliveryCompletionPlanRetry ||
		aggregate.CurrentAttempt.DeliveryCompletion.OriginalAttemptID != "attempt_failed_330" {
		t.Fatalf("completion Attempt = %#v, %v", aggregate.CurrentAttempt, err)
	}
	if completed.Assets[0].TaskID != generation.TaskID(f.failedID) ||
		completed.Assets[0].AttemptID != aggregate.CurrentAttempt.ID {
		t.Fatalf("registered asset = %#v", completed.Assets[0])
	}
	repository.Close()
	if attemptSnapshot(t, f, "attempt_failed_330") != originalAttempt {
		t.Fatal("apply changed the original failed Attempt")
	}
	if rowCount(t, f, `SELECT COUNT(*) FROM generation_tasks`) != tasksBefore ||
		rowCount(t, f, `SELECT COUNT(*) FROM execution_grants`) != grantsBefore {
		t.Fatal("apply created a paid Task or grant")
	}
	attemptCount, assetCount := completionCounts(t, f)
	if attemptCount != 2 || assetCount != 1 {
		t.Fatalf("completion rows: attempts %d, assets %d; want 2/1", attemptCount, assetCount)
	}

	again, err := runCommand(t, append(args, "--apply")...)
	if err != nil {
		t.Fatalf("repeated apply = %v", err)
	}
	if !strings.Contains(again, `"task_updated":false`) || !strings.Contains(again, `"already_completed":true`) {
		t.Fatalf("repeated apply output = %s", again)
	}
	if againAttempts, againAssets := completionCounts(t, f); againAttempts != attemptCount || againAssets != assetCount {
		t.Fatalf("repeated apply changed rows: attempts %d->%d assets %d->%d", attemptCount, againAttempts, assetCount, againAssets)
	}
}

func attemptSnapshot(t *testing.T, f commandFixture, attemptID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var snapshot string
	if err := db.QueryRow(`SELECT state || '|' || COALESCE(failure_json,'') || '|' ||
 COALESCE(last_observation_phase,'') || '|' || COALESCE(delivery_completion_json,'') || '|' ||
 COALESCE(finished_at,'') FROM generation_attempts WHERE id=?`, attemptID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func rowCount(t *testing.T, f commandFixture, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// preparePlanFixtureInput freezes one PreparedInput for the fixture's exact
// Offering and input, using the real hash and store. The adjacent retries use
// different Schema revisions, as they may in production.
func preparePlanFixtureInput(t *testing.T, f commandFixture, schemaVersion string) generation.PreparedInput {
	t.Helper()
	repository, err := sqlite.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	offering := f.offering
	offering.AdapterKey = "plan-fixture"
	registry := providers.NewRegistry()
	if err := registry.Register(offering.AdapterKey, planFixturePort{}); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{
		Repository: repository, Catalog: planFixtureCatalog{offering: offering}, Providers: registry,
		Assets: planFixtureStore{}, Inputs: planFixtureInputs{}, SchemaVersion: schemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	prepared, err := module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: generation.OwnerID(f.owner), SessionID: f.sessionID, Capability: generation.FormatConversion,
		OfferingID: f.offering.ID, Input: json.RawMessage(`{"source_asset":"asset-input"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

type planFixtureCatalog struct{ offering generation.Offering }

func (c planFixtureCatalog) Normalize(_ generation.Capability, _ generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	return c.offering, input, nil
}

func (c planFixtureCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	return c.offering, id == c.offering.ID
}

func (c planFixtureCatalog) List(generation.Capability) []generation.Offering {
	return []generation.Offering{c.offering}
}

type planFixturePort struct{}

func (planFixturePort) Start(context.Context, generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unused")
}

func (planFixturePort) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unused")
}

func (planFixturePort) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unused")
}

type planFixtureInputs struct{}

func (planFixtureInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (planFixtureInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}

type planFixtureStore struct{}

func (planFixtureStore) Put(context.Context, generation.AssetWrite) (generation.Asset, error) {
	return generation.Asset{}, errors.New("unused")
}

func (planFixtureStore) Open(context.Context, generation.Asset) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}

func (planFixtureStore) Delete(context.Context, generation.Asset) error {
	return errors.New("unused")
}

func TestDryRunAndApplyRefuseAnOldSchemaWithoutWriting(t *testing.T) {
	f := newCommandFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS generation_attempts_delivery_completion`,
		`ALTER TABLE generation_attempts DROP COLUMN delivery_completion_json`,
		`DELETE FROM go_schema_migrations WHERE version = 69`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := fileDigest(t, f.dbPath)
	for _, apply := range []bool{false, true} {
		args := []string{"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
			"--failed-task", f.failedID, "--delivery-task", f.deliveryID}
		if apply {
			args = append(args, "--apply")
		}
		if _, err := runCommand(t, args...); err == nil || !strings.Contains(err.Error(), "schema") {
			t.Fatalf("old schema apply=%v error = %v", apply, err)
		}
		if fileDigest(t, f.dbPath) != before {
			t.Fatal("old-schema refusal changed the database")
		}
	}
}

func TestDryRunRefusesAMismatchedPairWithoutWriting(t *testing.T) {
	f := newCommandFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,progress,failure_json,current_attempt_id,version,created_at,updated_at,finished_at) SELECT 'task_other_failed',owner_id,session_id,capability,offering_json,input_json,'other-hash','FAILED',0,failure_json,'attempt_failed_330',1,created_at,updated_at,finished_at FROM generation_tasks WHERE id=?`, f.failedID); err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, f.dbPath)
	if _, err := runCommand(t,
		"--database", f.dbPath, "--storage-root", f.storageRoot, "--owner", f.owner,
		"--failed-task", "task_other_failed", "--delivery-task", f.deliveryID); err == nil {
		t.Fatal("mismatched pair was accepted")
	}
	if fileDigest(t, f.dbPath) != before {
		t.Fatal("mismatched pair changed the database")
	}
}

func TestDryRunDoesNotCreateMissingStorageLayout(t *testing.T) {
	f := newCommandFixture(t)
	empty := t.TempDir()
	if _, err := runCommand(t,
		"--database", f.dbPath, "--storage-root", empty, "--owner", f.owner,
		"--failed-task", f.failedID, "--delivery-task", f.deliveryID); err == nil {
		t.Fatal("dry-run accepted a store without blobs")
	}
	if _, err := os.Stat(filepath.Join(empty, "blobs")); !os.IsNotExist(err) {
		t.Fatal("dry-run created the storage layout")
	}
}

func TestCommandRejectsMissingArgumentsBeforeOpeningAnything(t *testing.T) {
	if _, err := runCommand(t); err == nil {
		t.Fatal("missing arguments were accepted")
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "unused")); err == nil {
		t.Fatal("unexpected file")
	}
}
