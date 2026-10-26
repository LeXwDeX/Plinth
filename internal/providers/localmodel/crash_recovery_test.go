package localmodel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
)

type localRecoveryClock struct{ now time.Time }

func (c *localRecoveryClock) Now() time.Time { return c.now }

type crashAfterLocalResult struct{ generation.ProviderPort }

func (a crashAfterLocalResult) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	if _, err := a.ProviderPort.Start(ctx, request); err != nil {
		return generation.Observation{}, err
	}
	// The real conversion has finished, but its Observation is not persisted.
	// ReconcileOnce catches the panic while the durable Attempt stays SUBMITTING.
	panic("simulated process exit before local observation commit")
}
func (a crashAfterLocalResult) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	return a.ProviderPort.(generation.ProviderInputValidator).ValidateInput(ctx, request)
}

func TestLocalSubmittingCrashSafelyRecomputesWithoutAnotherAttempt(t *testing.T) {
	for _, nonIdempotent := range []bool{false, true} {
		name := "declared-local-contract"
		if nonIdempotent {
			name = "non-idempotent-control-stays-unknown"
		}
		t.Run(name, func(t *testing.T) {
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
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			forward, ok := product.Get("local/static-glb/to-gltf")
			if !ok || !forward.IdempotentSubmission {
				t.Fatal("local forward must declare safe recomputation")
			}
			reverse, ok := product.Get("local/static-gltf/to-glb")
			if !ok || !reverse.IdempotentSubmission {
				t.Fatal("local reverse must declare safe recomputation")
			}
			remote, ok := product.Get("tripo/format-conversion/uploaded-glb-to-stl")
			if !ok || remote.IdempotentSubmission {
				t.Fatal("remote conversion must preserve ambiguous submission protection")
			}
			if nonIdempotent {
				forward.IdempotentSubmission = false
			}
			fixture := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward}}
			clock := &localRecoveryClock{now: time.Now()}
			var native *inputassets.Service
			newModule := func(crash bool) *generation.Module {
				repo.SetClock(clock.Now)
				native, err = inputassets.New(repo, blobs)
				if err != nil {
					t.Fatal(err)
				}
				adapter, err := localmodel.New(localmodel.Config{Inputs: native, Assets: blobs})
				if err != nil {
					t.Fatal(err)
				}
				var port generation.ProviderPort = adapter
				if crash {
					port = crashAfterLocalResult{adapter}
				}
				registry := providers.NewRegistry()
				if err = registry.Register(forward.AdapterKey, port); err != nil {
					t.Fatal(err)
				}
				module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: fixture, Providers: registry, Assets: blobs, Inputs: native, Clock: clock})
				if err != nil {
					t.Fatal(err)
				}
				return module
			}
			module := newModule(true)
			if _, err = repo.CreateSession(ctx, chat.Session{ID: "local-session", OwnerID: "owner", Title: "restart fixture", CreatedAt: clock.now, UpdatedAt: clock.now}); err != nil {
				t.Fatal(err)
			}
			raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
			uploaded, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "triangle.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20})
			if err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]string{"source_asset": string(uploaded.ID)})
			accepted := submitApprovedLocal(t, module, repo, forward, input, "local-crash")
			if _, err = module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "crashed", BatchSize: 1, LeaseFor: time.Minute}); err == nil {
				t.Fatal("expected simulated process exit")
			}
			before, err := repo.Load(ctx, accepted.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if before.CurrentAttempt == nil || before.CurrentAttempt.State != generation.AttemptSubmitting {
				t.Fatal("failure did not preserve the exact SUBMITTING crash window")
			}
			if err = repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			clock.now = clock.now.Add(61 * time.Second)
			module = newModule(false)
			for i := 0; i < 4; i++ {
				if _, err = module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "restarted", BatchSize: 1, LeaseFor: time.Minute}); err != nil {
					t.Fatal(err)
				}
			}
			after, err := repo.Load(ctx, accepted.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if after.CurrentAttempt == nil || after.CurrentAttempt.UpstreamJob != nil || after.CurrentAttempt.ID != before.CurrentAttempt.ID {
				t.Fatal("recovery fabricated a job or another Attempt")
			}
			if nonIdempotent {
				if after.Task.State != generation.TaskFailed || after.CurrentAttempt.State != generation.AttemptSubmissionUnknown || len(after.Task.Assets) != 0 {
					t.Fatalf("non-idempotent control bypassed M2: %s / %s", after.Task.State, after.CurrentAttempt.State)
				}
				return
			}
			if after.Task.State != generation.TaskSucceeded || len(after.Task.Assets) != 1 {
				t.Fatalf("local recovery failed: %s / %s, assets=%d", after.Task.State, after.CurrentAttempt.State, len(after.Task.Assets))
			}
		})
	}
}
