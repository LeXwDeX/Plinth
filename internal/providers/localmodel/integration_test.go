package localmodel_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// The fixture catalog supplies frozen declarations; the actual Port, SQLite,
// native input service, pure converters and CAS materializer all run unchanged.
type fixtureCatalog struct {
	offerings map[generation.OfferingID]generation.Offering
}

func (c fixtureCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	v, ok := c.offerings[id]
	return v, ok
}
func (c fixtureCatalog) List(capability generation.Capability) []generation.Offering {
	var result []generation.Offering
	for _, v := range c.offerings {
		if capability == "" || capability == v.Capability {
			result = append(result, v)
		}
	}
	return result
}
func (c fixtureCatalog) Normalize(capability generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	v, ok := c.Get(id)
	if !ok || v.Capability != capability {
		return generation.Offering{}, nil, &generation.Error{Code: generation.ErrInvalidInput}
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return v, nil, &generation.Error{Code: generation.ErrInvalidInput}
	}
	for _, p := range v.Parameters {
		if p.Fixed {
			values[p.Name] = p.Default
		}
	}
	raw, _ := json.Marshal(values)
	return v, raw, nil
}
func moduleFor(t *testing.T, repo *sqlite.Repository, blobs *local.Store, product fixtureCatalog) (*generation.Module, *inputassets.Service) {
	t.Helper()
	native, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := localmodel.New(localmodel.Config{Inputs: native, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("local-model", adapter); err != nil {
		t.Fatal(err)
	}
	// The production App injects the Schema conversion rule; without it an
	// uploaded conversion would skip the shared admission guard (#330).
	productSchema, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: product, Providers: registry, Assets: blobs, Inputs: native,
		Conversions: modeldelivery.NewSchemaConversionRule(productSchema.Application().ModelDelivery)})
	if err != nil {
		t.Fatal(err)
	}
	return module, native
}
func runLocalTask(t *testing.T, module *generation.Module, id generation.TaskID) generation.Task {
	t.Helper()
	for round := 0; round < 6; round++ {
		task, err := module.Get(context.Background(), "owner", id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			if task.State != generation.TaskSucceeded {
				t.Fatalf("local task failed: %#v", task.Failure)
			}
			return task
		}
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{WorkerID: "local-model-integration", BatchSize: 1, LeaseFor: time.Minute}); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("local task did not materialize")
	return generation.Task{}
}
func submitApprovedLocal(t *testing.T, module *generation.Module, repo *sqlite.Repository, offering generation.Offering, raw json.RawMessage, key string) generation.Accepted {
	t.Helper()
	ctx := context.Background()
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "local-session", Capability: generation.FormatConversion, OfferingID: offering.ID, Input: raw})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: repo, Prepared: repo}
	principal := executiongrant.Principal{OwnerID: "owner", ActorID: "fixture-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: prepared.SessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: key})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "local-session", PreparedInputID: prepared.ID, IdempotencyKey: key, OperationID: key, AuthorizationRef: grant.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func TestLocalModelSQLiteRestartAndRealMaterializationRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "generation.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	forward, reverse := offering(schema.ModelConversionGLBToGLTF), offering(schema.ModelConversionGLTFToGLB)
	catalog := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward, reverse.ID: reverse}}
	module, native := moduleFor(t, repo, blobs, catalog)
	for _, session := range []chat.Session{{ID: "local-session", OwnerID: "owner", Title: "local fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}, {ID: "foreign-session", OwnerID: "foreign", Title: "foreign fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}} {
		if _, err := repo.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	original := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true, MultiplePrimitives: true})
	uploaded, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(original), OriginalFilename: "real-triangle.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"source_asset": string(uploaded.ID)})
	accepted := submitApprovedLocal(t, module, repo, forward, raw, "real-forward")

	// Persist the inline conversion claim, then restart before ordinary materialization.
	if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "before-restart", BatchSize: 1, LeaseFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	module, _ = moduleFor(t, repo, blobs, catalog)
	first := runLocalTask(t, module, accepted.TaskID)
	if len(first.Assets) != 1 || first.Assets[0].MediaType != "model/gltf+json" || first.Assets[0].Variant != "gltf" {
		t.Fatal("forward real task did not deliver declared glTF")
	}
	raw, _ = json.Marshal(map[string]string{"source_task": string(first.ID)})
	// A different owner cannot use the real successful source Task.
	if _, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "foreign", SessionID: "foreign-session", Capability: generation.FormatConversion, OfferingID: reverse.ID, Input: raw}); err == nil {
		t.Fatal("foreign source reached reverse preparation")
	}
	reversed := submitApprovedLocal(t, module, repo, reverse, raw, "real-reverse")

	last := runLocalTask(t, module, reversed.TaskID)
	if len(last.Assets) != 1 || last.Assets[0].MediaType != "model/gltf-binary" || last.Assets[0].Variant != "glb" {
		t.Fatal("reverse real task did not deliver declared GLB")
	}
	reader, err := blobs.Open(ctx, last.Assets[0])
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(original, roundtrip) {
		t.Fatal("real CAS round trip changed model bytes")
	}
	originalReader, err := native.Open(ctx, uploaded)
	if err != nil {
		t.Fatal(err)
	}
	retained, _ := io.ReadAll(originalReader)
	_ = originalReader.Close()
	if !bytes.Equal(retained, original) {
		t.Fatal("original upload was replaced")
	}
	// Exactly the two successful conversion Tasks exist; no fake upload Task/job.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tasks, jobs int
	if err := db.QueryRow(`SELECT count(*) FROM generation_tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM generation_attempts WHERE upstream_job_id IS NOT NULL AND upstream_job_id<>''`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if tasks != 2 || jobs != 0 {
		t.Fatalf("unexpected task/job lifecycle tasks=%d jobs=%d", tasks, jobs)
	}
}

func TestLocalModelInvalidProfileRejectedDuringRealPreparationWithZeroTaskOrGrant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "generation.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	forward := offering(schema.ModelConversionGLBToGLTF)
	product := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward}}
	module, native := moduleFor(t, repo, blobs, product)
	document, binary := modelinputtest.Document(modelinputtest.Options{})
	document["animations"] = []any{map[string]any{}}
	raw := modelinputtest.GLB(document, binary)
	// The historical generic upload path intentionally has no static-model hook;
	// this proves the Provider's preparation hook, rather than upload validation.
	uploaded, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "unsupported.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"source_asset": string(uploaded.ID)})
	_, err = module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", Capability: generation.FormatConversion, OfferingID: forward.ID, Input: input})
	if !generation.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("unsupported source reached frozen preparation: %v", err)
	}
	_, err = module.Submit(ctx, generation.SubmitCommand{OwnerID: "owner", Capability: generation.FormatConversion, OfferingID: forward.ID, Input: input})
	if !generation.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("direct submit bypassed local profile: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"generation_tasks", "execution_grants", "generation_prepared_inputs"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("read-only invalid preparation wrote %s", table)
		}
	}
}

func TestLocalModelPreparedSourceByteChangeCannotClaimGrantOrCreateTask(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "generation.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	storage := filepath.Join(root, "cas")
	blobs, err := local.New(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	forward := offering(schema.ModelConversionGLBToGLTF)
	module, native := moduleFor(t, repo, blobs, fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward}})
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "local-session", OwnerID: "owner", Title: "fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	original := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	upload, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(original), OriginalFilename: "triangle.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"source_asset": string(upload.ID)})
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "local-session", Capability: generation.FormatConversion, OfferingID: forward.ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: repo, Prepared: repo}
	principal := executiongrant.Principal{OwnerID: "owner", ActorID: "fixture-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: prepared.SessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "changed-bytes", OperationID: "changed-bytes"})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
	}
	changed := bytes.Clone(original)
	changed[len(changed)-1] ^= 1
	file := filepath.Join(storage, filepath.FromSlash(upload.StorageKey))
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, changed, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "local-session", PreparedInputID: prepared.ID, IdempotencyKey: "changed-bytes", OperationID: "changed-bytes", AuthorizationRef: grant.RequestID})
	if err == nil {
		t.Fatal("changed source bytes were admitted after human approval")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tasks, claimed int
	if err := db.QueryRow(`SELECT count(*) FROM generation_tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM execution_grants WHERE status IN ('claimed','bound') OR task_id IS NOT NULL`).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || claimed != 0 {
		t.Fatalf("invalid frozen bytes wrote task/claimed grant: %d/%d", tasks, claimed)
	}
}

func TestLocalModelReadOnlyOfferingValidationChecksBytesWithoutFreezing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "generation.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	forward := offering(schema.ModelConversionGLBToGLTF)
	module, native := moduleFor(t, repo, blobs, fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward}})
	document, binary := modelinputtest.Document(modelinputtest.Options{})
	document["animations"] = []any{map[string]any{}}
	for _, tc := range []struct {
		name  string
		body  []byte
		valid bool
	}{{"static", modelinputtest.Triangle(modelinputtest.Options{Indexed: true}), true}, {"animation", modelinputtest.GLB(document, binary), false}} {
		upload, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(tc.body), OriginalFilename: tc.name + ".glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20})
		if err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(map[string]string{"source_asset": string(upload.ID)})
		for repeat := 0; repeat < 2; repeat++ {
			err := module.ValidateOfferingInput(ctx, "owner", forward.ID, forward.Revision, input)
			if tc.valid && err != nil {
				t.Fatalf("static validation failed: %v", err)
			}
			if !tc.valid && !generation.IsCode(err, generation.ErrUnsupportedModelInput) {
				t.Fatalf("animation marked available by read-only check: %v", err)
			}
		}
		if err := module.ValidateOfferingInput(ctx, "foreign", forward.ID, forward.Revision, input); !generation.IsCode(err, generation.ErrInvalidInput) {
			t.Fatalf("foreign input reached provider validation: %v", err)
		}
		if err := module.ValidateOfferingInput(ctx, "owner", forward.ID, "old-revision", input); !generation.IsCode(err, generation.ErrConflict) {
			t.Fatalf("changed revision accepted: %v", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"generation_prepared_inputs", "generation_tasks", "execution_grants", "processing_plans"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("read-only validation wrote %s", table)
		}
	}
}
