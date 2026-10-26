package generation_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func deliveryPackage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, f := range [][2]string{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="start" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel" Target="/Other/start.model"/></Relationships>`},
		{"Other/start.model", `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`},
	} {
		out, err := w.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(out, f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type profileReturnStore struct {
	*local.Store
	mutate string
	writes []generation.AssetWrite
	opens  int
}

func (s *profileReturnStore) Put(ctx context.Context, w generation.AssetWrite) (generation.Asset, error) {
	s.writes = append(s.writes, w)
	asset, err := s.Store.Put(ctx, w)
	if s.mutate == "omitted" {
		asset.DeliveryProfile = ""
	}
	if s.mutate == "different" {
		asset.DeliveryProfile = schema.DeliveryProfileBinaryFBXV1
	}
	return asset, err
}
func (s *profileReturnStore) Open(ctx context.Context, a generation.Asset) (io.ReadCloser, error) {
	s.opens++
	return s.Store.Open(ctx, a)
}

type deliveryProfileAdapter struct {
	generation.ProviderPort
	body   []byte
	stored string
	store  *profileReturnStore
	starts int
}

func (a *deliveryProfileAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	a.starts++
	claim := generation.ArtifactClaim{ID: "private-package", Role: "arbitrary-output", Variant: "3mf", MediaType: "model/3mf", Inline: a.body}
	if a.stored != "" {
		asset, err := a.store.Store.Put(ctx, generation.AssetWrite{AssetID: "stored-profile", OwnerID: r.OwnerID, TaskID: r.TaskID, AttemptID: r.AttemptID, Role: claim.Role, Variant: claim.Variant, MediaType: claim.MediaType, DeliveryProfile: schema.DeliveryProfile3MFPackageV1, MaxBytes: 4 << 20, Body: bytes.NewReader(a.body)})
		if err != nil {
			return generation.Observation{}, err
		}
		if a.stored == "omitted" {
			asset.DeliveryProfile = ""
		}
		if a.stored == "different" {
			asset.DeliveryProfile = schema.DeliveryProfileBinaryFBXV1
		}
		claim.StoredAsset = &asset
		claim.Inline = nil
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Artifacts: []generation.ArtifactClaim{claim}}, nil
}
func (a *deliveryProfileAdapter) OpenAsset(_ context.Context, _ generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(a.body)), generation.AssetMetadata{MediaType: "model/3mf", Size: int64(len(a.body))}, nil
}
func TestDeliveryProfileMaterializationPersistsAndRejectsReturnOrRestoredMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, returned, stored string
		success                bool
	}{
		{"normal put", "", "", true}, {"omitted put profile", "omitted", "", false}, {"different put profile", "different", "", false},
		{"restored stored claim", "", "correct", true}, {"restored omitted profile", "", "omitted", false}, {"restored different profile", "", "different", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			dbPath, cas := filepath.Join(root, "test.db"), filepath.Join(root, "cas")
			one := 1
			offering := generation.Offering{ID: "profile/test", Revision: "frozen", ProviderID: "test", ModelID: "model", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "profile", Enabled: true, MaterializeAttempts: 1, Retry: generation.RetryPolicy{MaxAttempts: 1}, Outputs: []generation.AssetRequirement{{Role: "arbitrary-output", Variant: "3mf", Delivery: "required", Minimum: &one, MediaTypes: []string{"model/3mf"}, MaxBytes: 4 << 20, DeliveryProfile: schema.DeliveryProfile3MFPackageV1}}}
			adapter := &deliveryProfileAdapter{body: deliveryPackage(t), stored: tc.stored}
			registry := providers.NewRegistry()
			if err := registry.Register("profile", adapter); err != nil {
				t.Fatal(err)
			}
			clock := &fakeClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
			var repository *sqlite.Repository
			var module *generation.Module
			var store *profileReturnStore
			open := func() {
				var err error
				repository, err = sqlite.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				repository.SetClock(clock.Now)
				blobs, err := local.New(cas)
				if err != nil {
					t.Fatal(err)
				}
				store = &profileReturnStore{Store: blobs, mutate: tc.returned}
				adapter.store = store
				module, err = generation.New(generation.Dependencies{Repository: repository, Catalog: printReportCatalog{offering}, Providers: registry, Assets: store, Clock: clock})
				if err != nil {
					t.Fatal(err)
				}
			}
			open()
			defer func() { _ = module.Close() }()
			state := submitFixtureState{}
			accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "owner", Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			reconcile := func() {
				if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "profile-worker", LeaseFor: time.Minute, BatchSize: 1}); err != nil {
					t.Fatal(err)
				}
				clock.Advance(31 * time.Second)
			}
			reconcile()
			if err = module.Close(); err != nil {
				t.Fatal(err)
			}
			// A current Catalog with no profile cannot rewrite the frozen Task outputs.
			offering.Outputs = append([]generation.AssetRequirement(nil), offering.Outputs...)
			offering.Outputs[0].DeliveryProfile = ""
			open()
			var task generation.Task
			for i := 0; i < 6; i++ {
				reconcile()
				task, err = module.Get(ctx, "owner", accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if task.State.Terminal() {
					break
				}
			}
			if adapter.starts != 1 {
				t.Fatalf("restart reissued provider mutation %d", adapter.starts)
			}
			if !tc.success {
				if task.State != generation.TaskFailed || len(task.Assets) != 0 {
					t.Fatalf("mismatched profile published: state=%s assets=%+v", task.State, task.Assets)
				}
				if tc.stored != "" && store.opens != 0 {
					t.Fatal("mismatched StoredAsset was opened before contract rejection")
				}
				return
			}
			if task.State != generation.TaskSucceeded || len(task.Assets) != 1 || task.Assets[0].DeliveryProfile != schema.DeliveryProfile3MFPackageV1 || task.Offering.Outputs[0].DeliveryProfile != schema.DeliveryProfile3MFPackageV1 {
				t.Fatalf("frozen profile lost: task=%s assets=%+v", task.State, task.Assets)
			}
			for _, w := range store.writes {
				if w.DeliveryProfile != schema.DeliveryProfile3MFPackageV1 {
					t.Fatal("AssetWrite omitted frozen profile")
				}
			}
			if err = module.Close(); err != nil {
				t.Fatal(err)
			}
			open()
			restored, err := module.Get(ctx, "owner", task.ID)
			if err != nil || restored.Assets[0].DeliveryProfile != schema.DeliveryProfile3MFPackageV1 {
				t.Fatalf("SQLite restart lost profile: %+v %v", restored.Assets, err)
			}
			body, err := store.Open(ctx, restored.Assets[0])
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(body)
			_ = body.Close()
			if err != nil || !bytes.Equal(actual, adapter.body) {
				t.Fatal("changed restored delivery bytes")
			}
		})
	}
}
