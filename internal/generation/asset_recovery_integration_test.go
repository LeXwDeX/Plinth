package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/assetrecovery"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type recoveryCountPort struct {
	*assetrecovery.Adapter
	starts *int
}

func (p recoveryCountPort) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	*p.starts++
	return p.Adapter.Start(ctx, r)
}

type recoveryHarness struct {
	path, cas string
	db        *sql.DB
	repo      *sqlite.Repository
	store     *local.Store
	module    *generation.Module
	offering  generation.Offering
	source    generation.RecoverySource
	receipt   generation.RecoveryReceipt
	clock     *fakeClock
	starts    int
}

func newRecoveryHarness(t *testing.T) *recoveryHarness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	h := &recoveryHarness{path: filepath.Join(root, "tasks.db"), cas: filepath.Join(root, "cas"), clock: &fakeClock{now: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)}}
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h.offering, _ = c.Get("local/existing-assets/recover")
	h.offering.Outputs = h.offering.Outputs[:2]
	old := generation.Offering{ID: "tripo/frozen/text-to-3d", Revision: "original-r1", ProviderID: "tripo", ModelID: "frozen", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Outputs: append([]generation.AssetRequirement(nil), h.offering.Outputs...)}
	old.Outputs[0].Variant = ""
	old.Outputs[1].Variant = "converted.gltf"
	h.offering.ProviderOptions, _ = json.Marshal(schema.RecoveryOptions{ReceiptParameter: "receipt_id", DigestParameter: "receipt_digest", Sources: []schema.RecoverySourceContract{{OfferingID: old.ID, OfferingRevision: old.Revision, Slots: []schema.RecoverySlotMapping{{SourceRole: "model", SourceVariant: "", Role: "model", Variant: "glb"}, {SourceRole: "converted_model", SourceVariant: "converted.gltf", Role: "converted_model", Variant: "glb"}}}}})
	h.open(t)
	h.db, err = sql.Open("sqlite", h.path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(old)
	stamp := "2026-10-15T00:00:00Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('source-session','owner','Owner','original',?,?)`, []any{stamp, stamp}},
		{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES ('source-task','owner','source-session','text-to-3d',?,'{"prompt":"original"}','source-hash','FAILED','source-attempt',1,?,?,?)`, []any{raw, stamp, stamp, stamp}},
		{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,upstream_provider_id,upstream_job_id,opaque_json,started_at,updated_at,finished_at) VALUES ('source-attempt','source-task',1,?,'FAILED','source-key','tripo','remote-source','{"private":"unchanged"}',?,?,?)`, []any{string(raw), stamp, stamp, stamp}},
		{`INSERT INTO generation_usage_ledger(id,task_id,user_id,task_type,estimated_credits,task_status,created_at) VALUES ('source-usage','source-task','owner','text-to-3d',55,'FAILED',?)`, []any{stamp}},
	}
	for _, statement := range statements {
		if _, err = h.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	h.source, err = h.repo.ReadRecoverySource(ctx, "owner", "source-task")
	if err != nil {
		t.Fatal(err)
	}
	offeringSHA, _ := generation.RecoveryDigest(h.offering)
	receipt := generation.RecoveryReceipt{Source: h.source.Binding, ArchiveSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64), OfferingID: h.offering.ID, OfferingRevision: h.offering.Revision, OfferingSHA256: offeringSHA}
	for i, out := range h.offering.Outputs {
		asset, err := h.store.Put(ctx, generation.AssetWrite{AssetID: generation.AssetID("stage-" + out.Role), OwnerID: "owner", TaskID: "source-task", AttemptID: "source-attempt", Role: out.Role, Variant: out.Variant, Required: true, MediaType: out.MediaTypes[0], MaxBytes: out.MaxBytes, Body: bytes.NewReader(validGLB("same immutable bytes"))})
		if err != nil {
			t.Fatal(err)
		}
		receipt.Slots = append(receipt.Slots, generation.RecoverySlot{SourceRole: old.Outputs[i].Role, SourceVariant: old.Outputs[i].Variant, Role: out.Role, Variant: out.Variant, Required: true, StorageKey: asset.StorageKey, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType})
	}
	h.receipt, err = generation.SealRecoveryReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = sqlite.CreateRecoveryReceiptFile(ctx, h.path, h.receipt, h.offering); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.module.Close(); h.db.Close() })
	return h
}
func (h *recoveryHarness) open(t *testing.T) {
	t.Helper()
	var err error
	h.repo, err = sqlitetest.Open(h.path)
	if err != nil {
		t.Fatal(err)
	}
	h.repo.SetClock(h.clock.Now)
	h.store, err = local.New(h.cas)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := assetrecovery.New(h.repo, h.store)
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err = registry.Register(h.offering.AdapterKey, recoveryCountPort{adapter, &h.starts}); err != nil {
		t.Fatal(err)
	}
	h.module, err = generation.New(generation.Dependencies{Repository: h.repo, Catalog: rigReportCatalog{[]generation.Offering{h.offering}}, Providers: registry, Assets: h.store, Clock: h.clock, SchemaVersion: "recovery-test"})
	if err != nil {
		t.Fatal(err)
	}
}
func (h *recoveryHarness) reconcile(t *testing.T) {
	t.Helper()
	_, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{WorkerID: "recovery-test", LeaseFor: time.Minute, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(31 * time.Second)
}
func (h *recoveryHarness) assertSourceUnchanged(t *testing.T) {
	t.Helper()
	source, err := h.repo.ReadRecoverySource(context.Background(), "owner", "source-task")
	if err != nil || source.Binding != h.source.Binding {
		t.Fatalf("original source changed: %+v %v", source.Binding, err)
	}
	var count, credits int
	var state string
	if err = h.db.QueryRow(`SELECT count(*),estimated_credits,task_status FROM generation_usage_ledger WHERE task_id='source-task'`).Scan(&count, &credits, &state); err != nil || count != 1 || credits != 55 || state != "FAILED" {
		t.Fatalf("original usage changed: %d %d %s %v", count, credits, state, err)
	}
}
func TestAssetRecoveryPublicTaskReceiptAndMaterializationSurviveRestart(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	view, err := h.module.RecoveryEligibility(ctx, "owner", "source-task")
	if err != nil || !view.Eligible || len(view.Slots) != 2 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	prepared, _, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil || again.ID != prepared.ID || again.InputHash != prepared.InputHash {
		t.Fatal("preparation is not stable", err)
	}
	if _, err = h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "source-session", PreparedInputID: prepared.ID, IdempotencyKey: view.IdempotencyKey}); err == nil {
		t.Fatal("missing grant accepted")
	}
	command := authorizePrepared(t, ctx, h.repo, prepared, view.IdempotencyKey)
	bad := command
	bad.IdempotencyKey = "another-key"
	if _, err = h.module.SubmitPrepared(ctx, bad); err == nil {
		t.Fatal("different recovery key accepted")
	}
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if err = h.module.Close(); err != nil {
		t.Fatal(err)
	}
	h.open(t)
	var task generation.Task
	for i := 0; i < 8; i++ {
		h.reconcile(t)
		task, err = h.module.Get(ctx, "owner", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded || task.State == generation.TaskFailed {
			break
		}
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != 2 || h.starts != 1 {
		t.Fatalf("recovery state=%s assets=%d starts=%d failure=%+v", task.State, len(task.Assets), h.starts, task.Failure)
	}
	for _, asset := range task.Assets {
		body, err := h.store.Open(ctx, asset)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(body)
		body.Close()
		if err != nil || !bytes.Equal(raw, validGLB("same immutable bytes")) {
			t.Fatal("materialized bytes differ", err)
		}
	}
	replay, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || replay.TaskID != accepted.TaskID || !replay.Replayed {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	next, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil || next.ID != "" || view.RecoveryTaskID != accepted.TaskID {
		t.Fatalf("already recovered view=%+v %v", view, err)
	}
	provenance, err := h.module.TaskRecoveryProvenance(ctx, task)
	if err != nil || provenance.SourceTaskID != "source-task" || provenance.SourceAttemptID != "source-attempt" {
		t.Fatalf("provenance=%+v %v", provenance, err)
	}
	var newCredits int
	if err = h.db.QueryRow(`SELECT coalesce(sum(estimated_credits),0) FROM generation_usage_ledger WHERE task_id=?`, task.ID).Scan(&newCredits); err != nil || newCredits != 0 {
		t.Fatalf("recovery was charged %d %v", newCredits, err)
	}
	h.assertSourceUnchanged(t)
}
func TestAssetRecoveryRejectsCrossOwnerCrossSessionAndChangedEvidence(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	if _, err := h.module.RecoveryEligibility(ctx, "other-owner", "source-task"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("cross-owner error %v", err)
	}
	if _, err := h.repo.GetRecoveryReceipt(ctx, "other-owner", h.receipt.ID); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("receipt disclosed to other owner: %v", err)
	}
	input, _ := generation.RecoveryInput(h.offering, h.receipt)
	adapter, _ := assetrecovery.New(h.repo, h.store)
	for _, session := range []string{"", "other-session"} {
		if err := adapter.ValidateInput(ctx, generation.InputValidationRequest{OwnerID: "owner", SessionID: session, Offering: h.offering, Input: input}); err == nil {
			t.Fatal("wrong session accepted")
		}
	}
	if _, _, err := h.module.PrepareRecovery(ctx, "owner", "source-task", strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if _, err := h.db.Exec(`UPDATE generation_recovery_receipts SET digest='changed' WHERE id=?`, h.receipt.ID); err == nil {
		t.Fatal("receipt is mutable")
	}
	if err := os.WriteFile(filepath.Join(h.cas, h.receipt.Slots[0].StorageKey), validGLB("different byte value"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest); err == nil {
		t.Fatal("changed staged bytes accepted")
	}
	h.assertSourceUnchanged(t)
}
func TestAssetRecoveryReceiptCannotSurviveSourceMutation(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	if _, err := h.db.Exec(`UPDATE generation_attempts SET opaque_json='{"changed":true}' WHERE id='source-attempt'`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.module.RecoveryEligibility(ctx, "owner", "source-task"); err == nil {
		t.Fatal("receipt accepted after source checkpoint changed")
	}
	if err := sqlite.CreateRecoveryReceiptFile(ctx, h.path, h.receipt, h.offering); err == nil {
		t.Fatal("receipt replay accepted after source mutation")
	}
}

func TestAssetRecoveryReceiptReplayAndGCReferenceStayBound(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	if err := sqlite.CreateRecoveryReceiptFile(ctx, h.path, h.receipt, h.offering); err != nil {
		t.Fatal(err)
	}
	changed := h.receipt
	changed.ArchiveSHA256 = strings.Repeat("c", 64)
	changed, err := generation.SealRecoveryReceipt(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err = sqlite.CreateRecoveryReceiptFile(ctx, h.path, changed, h.offering); err == nil {
		t.Fatal("different receipt replaced original")
	}
	var count int
	if err = h.db.QueryRow(`SELECT count(*) FROM generation_recovery_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count=%d %v", count, err)
	}
	records, err := sqlite.BlobReferenceRecords(ctx, h.path)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, r := range records {
		if r.SourceKind == "recovery_receipt" {
			found++
			if r.StorageKey != h.receipt.Slots[0].StorageKey {
				t.Fatal("wrong staged reference")
			}
		}
	}
	if found != 2 {
		t.Fatalf("missing staged references: %d", found)
	}
	h.assertSourceUnchanged(t)
}
func TestAssetRecoveryCollectorRefusesWrongMigrationWithoutChangingSource(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	if _, err := h.db.Exec(`UPDATE go_schema_migrations SET checksum='wrong' WHERE version=54`); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.CheckRecoveryReceiptTargetFile(ctx, h.path); !errors.Is(err, generation.RepositoryConflict) {
		t.Fatalf("read-only migration preflight accepted: %v", err)
	}
	if err := sqlite.CreateRecoveryReceiptFile(ctx, h.path, h.receipt, h.offering); !errors.Is(err, generation.RepositoryConflict) {
		t.Fatalf("wrong migration accepted: %v", err)
	}
	h.assertSourceUnchanged(t)
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := sqlite.ReadRecoverySourceFile(ctx, missing, "owner", "source-task"); err == nil {
		t.Fatal("created missing database")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only helper wrote database")
	}
}

func TestAssetRecoveryAcceptedReplayDoesNotDependOnCurrentCatalog(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	prepared, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	command := authorizePrepared(t, ctx, h.repo, prepared, view.IdempotencyKey)
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.module.Close(); err != nil {
		t.Fatal(err)
	}
	h.offering.ID = "local/removed-from-current-catalog"
	h.open(t)
	replay, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || !replay.Replayed || replay.TaskID != accepted.TaskID {
		t.Fatalf("accepted recovery replay lost frozen identity: %+v %v", replay, err)
	}
	h.assertSourceUnchanged(t)
}

func TestAssetRecoveryMaterializationRejectsForgedLocatorAndMidReadMutation(t *testing.T) {
	ctx := context.Background()
	h := newRecoveryHarness(t)
	prepared, view, err := h.module.PrepareRecovery(ctx, "owner", "source-task", h.receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	command := authorizePrepared(t, ctx, h.repo, prepared, view.IdempotencyKey)
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	aggregate, err := h.repo.Load(ctx, accepted.TaskID)
	if err != nil || len(aggregate.Claims) != 2 {
		t.Fatalf("claims=%d %v", len(aggregate.Claims), err)
	}
	adapter, _ := assetrecovery.New(h.repo, h.store)
	request := generation.OpenAssetRequest{TaskID: accepted.TaskID, AttemptID: aggregate.Task.CurrentAttemptID, Offering: aggregate.Task.Offering, Claim: aggregate.Claims[0]}
	for _, change := range []func(*generation.OpenAssetRequest){
		func(r *generation.OpenAssetRequest) { r.Claim.Locator = "https://example.test/model.glb" },
		func(r *generation.OpenAssetRequest) { r.Claim.ID = "forged" },
		func(r *generation.OpenAssetRequest) { r.AttemptID = "source-attempt" },
		func(r *generation.OpenAssetRequest) { r.Claim.Metadata = json.RawMessage(`{"receipt_id":"forged"}`) },
		func(r *generation.OpenAssetRequest) { r.Claim.Variant = "fbx" },
	} {
		bad := request
		change(&bad)
		body, _, err := adapter.OpenAsset(ctx, bad)
		if body != nil {
			body.Close()
		}
		if err == nil {
			t.Fatal("forged claim accepted")
		}
	}
	body, _, err := adapter.OpenAsset(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if err = os.WriteFile(filepath.Join(h.cas, h.receipt.Slots[0].StorageKey), validGLB("changed after open"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(body); err == nil {
		t.Fatal("mid-read content mutation accepted")
	}
	h.assertSourceUnchanged(t)
}
