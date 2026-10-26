package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

type assetDependencyPreparedRepository struct {
	dependencySourceRepository
	prepared PreparedInput
	creates  int
}

func (r *assetDependencyPreparedRepository) CreatePreparedInput(_ context.Context, input PreparedInput) error {
	// Exercise the existing persisted representation, including empty remote IDs.
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &r.prepared)
}
func (r *assetDependencyPreparedRepository) GetPreparedInput(context.Context, OwnerID, PreparedInputID) (PreparedInput, error) {
	return r.prepared, nil
}
func (r *assetDependencyPreparedRepository) GetByIdempotency(context.Context, OwnerID, string) (Task, error) {
	return Task{}, RepositoryNotFound
}
func (r *assetDependencyPreparedRepository) Create(_ context.Context, task Task, _ Event, _ int) (Task, bool, error) {
	r.creates++
	return task, false, nil
}

type assetDependencyCatalog struct {
	Catalog
	offering Offering
}

func (c assetDependencyCatalog) Normalize(_ Capability, _ OfferingID, raw json.RawMessage) (Offering, json.RawMessage, error) {
	return c.offering, append(json.RawMessage(nil), raw...), nil
}
func (c assetDependencyCatalog) Get(OfferingID) (Offering, bool) { return c.offering, true }

type assetDependencyDestinationRegistry struct{ sourceCalls int }

func (r *assetDependencyDestinationRegistry) Provider(o Offering) (ProviderPort, bool) {
	if o.ID != "local/reverse" {
		r.sourceCalls++
		return nil, false
	}
	return &dependencyProjection{}, true
}

func TestAssetsTaskPreparedInputFreezesSelectedBytesAndRevalidatesBeforeAdmission(t *testing.T) {
	for _, name := range []string{"valid", "unrelated added asset", "changed bytes", "changed metadata", "deleted source", "other owner", "failed source", "wrong offering", "ambiguous source", "missing store"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repo := &assetDependencyPreparedRepository{dependencySourceRepository: dependencySourceRepository{source: Aggregate{Task: assetTaskSource(), CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded}}}}
			store := &taskAssetReadStore{body: "saved model"}
			registry := &assetDependencyDestinationRegistry{}
			offering := Offering{ID: "local/reverse", Revision: "1", ProviderID: "local", Capability: FormatConversion, Parameters: []Parameter{assetTaskParameter(t)}}
			m, err := New(Dependencies{Repository: repo, Catalog: assetDependencyCatalog{offering: offering}, Providers: registry, Assets: store, SchemaVersion: "test"})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := m.PrepareInput(ctx, PrepareInputCommand{OwnerID: "owner", Capability: FormatConversion, OfferingID: offering.ID, Input: json.RawMessage(`{"source_task":"source"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].RemoteID != "" || prepared.Dependencies[0].UpstreamProviderID != "" || len(prepared.Dependencies[0].OutputAssets) != 1 || prepared.Dependencies[0].OutputAssets[0].ID != "selected" || len(prepared.InputHash) != 64 {
				t.Fatalf("incomplete frozen dependency: %+v", prepared)
			}
			if store.opens != 1 || store.closes != 1 || repo.creates != 0 {
				t.Fatalf("prepare did not check bytes without creating work: store=%+v creates=%d", store, repo.creates)
			}
			switch name {
			case "unrelated added asset":
				repo.source.Task.Assets = append(repo.source.Task.Assets, Asset{ID: "unrelated", TaskID: "source", Role: "thumbnail"})
			case "changed bytes":
				store.body = "changed mdl"
			case "changed metadata":
				store.body = "changed mdl"
				sum := sha256.Sum256([]byte(store.body))
				repo.source.Task.Assets[0].SHA256 = hex.EncodeToString(sum[:])
			case "deleted source":
				repo.err = RepositoryNotFound
			case "other owner":
				repo.source.Task.OwnerID = "other"
			case "failed source":
				repo.source.Task.State = TaskFailed
			case "wrong offering":
				repo.source.Task.Offering.ID = "other"
			case "ambiguous source":
				other := repo.source.Task.Assets[0]
				other.ID = "second"
				repo.source.Task.Assets = append(repo.source.Task.Assets, other)
			case "missing store":
				m.assets = nil
			}
			_, err = m.SubmitPrepared(ctx, SubmitPreparedCommand{OwnerID: "owner", PreparedInputID: prepared.ID, IdempotencyKey: "submit", AuthorizationRef: "approved", OperationID: "operation"})
			valid := name == "valid" || name == "unrelated added asset"
			if (err == nil) != valid {
				t.Fatalf("valid=%v error=%v", valid, err)
			}
			if valid && repo.creates != 1 || !valid && repo.creates != 0 {
				t.Fatalf("unexpected Task admission: %d", repo.creates)
			}
			if registry.sourceCalls != 0 {
				t.Fatal("prepared bytes depended on source Provider availability")
			}
		})
	}
}
