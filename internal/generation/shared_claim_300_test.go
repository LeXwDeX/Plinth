package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

// #300: a Provider that built two slots from one file says so on the claim
// (SameBytesAs) and materialization downloads the file once. The kernel never
// infers that from equal locators; each slot is still stored and checked on its
// own contract, so every slot keeps its own asset record.

// declaredClaimsAdapter completes at once with the claims a test names. Every
// locator serves the same GLB. Downloads are counted per locator, and the first
// failFirst of them fail the way a dropped connection does.
type declaredClaimsAdapter struct {
	claims    func(attempt generation.AttemptID) []generation.ArtifactClaim
	body      []byte
	failFirst int

	mu    sync.Mutex
	opens map[string]int
	calls int
}

func newDeclaredClaimsAdapter(body []byte, claims func(attempt generation.AttemptID) []generation.ArtifactClaim) *declaredClaimsAdapter {
	return &declaredClaimsAdapter{claims: claims, body: body, opens: map[string]int{}}
}

func (a *declaredClaimsAdapter) Start(_ context.Context, request generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: "declared-" + string(request.AttemptID)},
		Artifacts: a.claims(request.AttemptID),
	}, nil
}

func (*declaredClaimsAdapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (a *declaredClaimsAdapter) OpenAsset(_ context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	body := request.Claim.Inline
	if request.Claim.Locator != "" {
		a.mu.Lock()
		a.calls++
		a.opens[request.Claim.Locator]++
		fail := a.calls <= a.failFirst
		a.mu.Unlock()
		if fail {
			return nil, generation.AssetMetadata{}, errors.New("connection reset")
		}
		body = a.body
	}
	return io.NopCloser(bytes.NewReader(body)), generation.AssetMetadata{MediaType: request.Claim.MediaType, Size: int64(len(body))}, nil
}

// downloads is how many times a locator was opened; total is every download.
func (a *declaredClaimsAdapter) downloads(locator string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.opens[locator]
}

func (a *declaredClaimsAdapter) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func gltfLocator(attempt generation.AttemptID) string {
	return "https://assets.example.test/" + string(attempt) + "/gltf.glb"
}

// gltfSlots names the two claims of a Tripo GLTF export: the model slot and the
// converted.gltf slot, one file under one locator. The claims are loaded in claim
// ID order, so the IDs decide which slot is stored first. declaring names the
// claim that carries SameBytesAs ("model", "converted" or "" for none), pointing
// at the other.
func gltfSlots(modelID, convertedID, declaring string) func(attempt generation.AttemptID) []generation.ArtifactClaim {
	return func(attempt generation.AttemptID) []generation.ArtifactClaim {
		model := generation.ArtifactClaim{ID: string(attempt) + ":" + modelID, Role: "model", Locator: gltfLocator(attempt), MediaType: "model/gltf-binary"}
		converted := generation.ArtifactClaim{ID: string(attempt) + ":" + convertedID, Role: "converted_model", Variant: "converted.gltf", Locator: gltfLocator(attempt), MediaType: "model/gltf-binary"}
		switch declaring {
		case "model":
			model.SameBytesAs = converted.ID
		case "converted":
			converted.SameBytesAs = model.ID
		}
		return []generation.ArtifactClaim{model, converted}
	}
}

func submitTripoGLTF(t *testing.T, h *harness) generation.Accepted {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"a brass robot","authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func succeeded(t *testing.T, h *harness, accepted generation.Accepted) generation.Task {
	t.Helper()
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail + " / " + task.Failure.UpstreamExcerpt
		}
		events, _ := h.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
		t.Fatalf("state = %s, failure = %q, events = %v", task.State, detail, eventTypes(events))
	}
	return task
}

func readAsset(t *testing.T, h *harness, task generation.Task, asset generation.Asset) []byte {
	t.Helper()
	content, err := h.module.OpenAsset(context.Background(), "owner-a", task.ID, asset.ID)
	if err != nil {
		t.Fatalf("open %s/%s: %v", asset.Role, asset.Variant, err)
	}
	data, readErr := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if readErr != nil {
		t.Fatalf("read %s/%s: %v", asset.Role, asset.Variant, readErr)
	}
	return data
}

func TestDeclaredIdenticalClaimsAreDownloadedOnce(t *testing.T) {
	for _, test := range []struct {
		name                        string
		modelID, convertedID, named string
	}{
		// Claims are stored in claim ID order. The declaration may sit on the claim
		// stored first or second, and the claim it names may come before or after.
		{"the declaring claim is stored second", "m-model", "c-convert", "model"},
		{"the declaring claim is stored first", "a-model", "z-convert", "model"},
		{"the other slot declares, stored first", "m-model", "c-convert", "converted"},
		{"the other slot declares, stored second", "a-model", "z-convert", "converted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			shared := validGLB("tripo gltf conversion")
			adapter := newDeclaredClaimsAdapter(shared, gltfSlots(test.modelID, test.convertedID, test.named))
			harness := newHarness(t, adapter, nil)
			accepted := submitTripoGLTF(t, harness)
			task := succeeded(t, harness, accepted)

			attempt := task.CurrentAttemptID
			if downloads := adapter.downloads(gltfLocator(attempt)); downloads != 1 {
				t.Fatalf("the shared file was downloaded %d times, want 1", downloads)
			}
			model := slotAsset(t, task.Assets, "model", "")
			converted := slotAsset(t, task.Assets, "converted_model", "converted.gltf")
			// One file, but each slot keeps its own record and downloads on its own.
			if model.ID == converted.ID || !model.Required || !converted.Required {
				t.Fatalf("slot records: model=%+v converted=%+v", model, converted)
			}
			if model.StorageKey != converted.StorageKey || model.SHA256 != converted.SHA256 || model.Size != int64(len(shared)) || converted.Size != int64(len(shared)) {
				t.Fatalf("the slots did not share one blob: model=%+v converted=%+v", model, converted)
			}
			for _, asset := range []generation.Asset{model, converted} {
				if data := readAsset(t, harness, task, asset); !bytes.Equal(data, shared) {
					t.Fatalf("%s/%s read %d bytes, want the shared file", asset.Role, asset.Variant, len(data))
				}
			}
			if copies := slices.Index(storedBlobFiles(t, harness), model.StorageKey); copies < 0 {
				t.Fatalf("shared blob %s is not stored: %v", model.StorageKey, storedBlobFiles(t, harness))
			}
			events, err := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(eventTypes(events), "materialization.artifact_skipped") {
				t.Fatalf("a slot was skipped: %v", eventTypes(events))
			}
		})
	}
}

// Equal locators are not a declaration: OpenAsset may depend on more than the
// locator (a role-specific check, a binding in the claim's metadata), so the
// kernel never merges downloads on its own.
func TestEqualLocatorsWithoutADeclarationAreDownloadedPerClaim(t *testing.T) {
	adapter := newDeclaredClaimsAdapter(validGLB("no declaration"), gltfSlots("m-model", "c-convert", ""))
	harness := newHarness(t, adapter, nil)
	task := succeeded(t, harness, submitTripoGLTF(t, harness))
	if downloads := adapter.downloads(gltfLocator(task.CurrentAttemptID)); downloads != 2 {
		t.Fatalf("downloads = %d, want one per claim", downloads)
	}
}

// A declaration that names nothing usable is ignored, not an error: the claims are
// downloaded as before.
func TestADeclarationNamingNoUsableClaimIsIgnored(t *testing.T) {
	for _, test := range []struct {
		name  string
		claim func(claims []generation.ArtifactClaim)
		// downloads is how many claims still have a locator to fetch.
		downloads int
	}{
		{"an ID the Observation does not have", func(claims []generation.ArtifactClaim) {
			claims[0].SameBytesAs = "no-such-claim"
		}, 2},
		{"the claim itself", func(claims []generation.ArtifactClaim) {
			claims[0].SameBytesAs = claims[0].ID
		}, 2},
		{"a claim that carries its own bytes", func(claims []generation.ArtifactClaim) {
			claims[1].Locator = ""
			claims[1].Inline = validGLB("inline")
			claims[0].SameBytesAs = claims[1].ID
		}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := newDeclaredClaimsAdapter(validGLB("ignored declaration"), func(attempt generation.AttemptID) []generation.ArtifactClaim {
				claims := gltfSlots("m-model", "c-convert", "")(attempt)
				test.claim(claims)
				return claims
			})
			harness := newHarness(t, adapter, nil)
			task := succeeded(t, harness, submitTripoGLTF(t, harness))
			if downloads := adapter.downloads(gltfLocator(task.CurrentAttemptID)); downloads != test.downloads {
				t.Fatalf("downloads = %d, want %d", downloads, test.downloads)
			}
		})
	}
}

// A failed first download promises nothing about the other claim: it downloads for
// itself, the missing slot fails the pass, and the retry stores both. The failure
// and the retry are the same as without a declaration; the retry just needs one
// download fewer.
func TestAFailedFirstDownloadLeavesTheOtherClaimToDownloadItself(t *testing.T) {
	shared := validGLB("retry")
	adapter := newDeclaredClaimsAdapter(shared, gltfSlots("m-model", "c-convert", "model"))
	adapter.failFirst = 1
	harness := newHarness(t, adapter, nil)
	accepted := submitTripoGLTF(t, harness)
	task := succeeded(t, harness, accepted)

	// Pass one: the first claim's download fails, the second downloads for itself.
	// Pass two: the first claim downloads, the second is copied from it.
	if total := adapter.total(); total != 3 {
		t.Fatalf("downloads = %d, want 3 (failed, second claim alone, retry of the first)", total)
	}
	events, err := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(eventTypes(events), "materialization.retry_scheduled") {
		t.Fatalf("no retry was scheduled: %v", eventTypes(events))
	}
	model := slotAsset(t, task.Assets, "model", "")
	converted := slotAsset(t, task.Assets, "converted_model", "converted.gltf")
	if model.StorageKey != converted.StorageKey || !bytes.Equal(readAsset(t, harness, task, model), shared) || !bytes.Equal(readAsset(t, harness, task, converted), shared) {
		t.Fatalf("the retried slots do not hold the shared file: model=%+v converted=%+v", model, converted)
	}
}

// writeRecordingStore keeps every write a slot asked for, over a real store.
type writeRecordingStore struct {
	generation.AssetStore
	mu     sync.Mutex
	writes []generation.AssetWrite
}

func (s *writeRecordingStore) Put(ctx context.Context, write generation.AssetWrite) (generation.Asset, error) {
	s.mu.Lock()
	recorded := write
	recorded.Body = nil
	s.writes = append(s.writes, recorded)
	s.mu.Unlock()
	return s.AssetStore.Put(ctx, write)
}

func (s *writeRecordingStore) Close() error {
	if closer, ok := s.AssetStore.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (s *writeRecordingStore) writesFor(variant string) []generation.AssetWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []generation.AssetWrite
	for _, write := range s.writes {
		if write.Variant == variant {
			result = append(result, write)
		}
	}
	return result
}

// narrowSlotOffering has two slots over one file whose limits differ: the model
// takes it, converted.gltf takes less than it weighs.
func narrowSlotOffering(narrow int64) generation.Offering {
	one := 1
	return generation.Offering{
		ID: "declared/test", Revision: "frozen", ProviderID: "test", ModelID: "model", Capability: generation.TextTo3D,
		Domain: generation.DomainModel3D, AdapterKey: "declared", Enabled: true, MaterializeAttempts: 1,
		Retry: generation.RetryPolicy{MaxAttempts: 1},
		Outputs: []generation.AssetRequirement{
			{Role: "model", Delivery: "required", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 1 << 20},
			{Role: "converted_model", Variant: "converted.gltf", Delivery: "required", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: narrow},
		},
	}
}

func runNarrowSlotTask(t *testing.T, adapter *declaredClaimsAdapter, narrow int64) (generation.Task, *writeRecordingStore) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	offering := narrowSlotOffering(narrow)
	registry := providers.NewRegistry()
	if err := registry.Register(offering.AdapterKey, adapter); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)}
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	repository.SetClock(clock.Now)
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	store := &writeRecordingStore{AssetStore: blobs}
	module, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repository, Catalog: printReportCatalog{offering}, Providers: registry, Assets: store, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	state := submitFixtureState{}
	accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "owner-a", Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var task generation.Task
	for range 12 {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "declared-worker", LeaseFor: time.Minute, BatchSize: 1}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(31 * time.Second)
		if task, err = module.Get(ctx, "owner-a", accepted.TaskID); err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return task, store
		}
	}
	t.Fatalf("task did not finish: %s", task.State)
	return task, store
}

// A copy is not a pass: the slot it fills is written with its own limit and
// refuses what its contract refuses, exactly as if its file had been downloaded.
func TestACopiedSlotIsCheckedAgainstItsOwnContract(t *testing.T) {
	shared := validGLB("narrow slot")
	adapter := newDeclaredClaimsAdapter(shared, gltfSlots("a-model", "z-convert", "converted"))
	task, store := runNarrowSlotTask(t, adapter, int64(len(shared))-1)

	if task.State != generation.TaskFailed || len(task.Assets) != 0 {
		t.Fatalf("a slot over its limit was published: state=%s assets=%+v", task.State, task.Assets)
	}
	if task.Failure == nil || task.Failure.Class != generation.FailureIntegrity ||
		!strings.Contains(task.Failure.UpstreamExcerpt, "format=converted.gltf") || !strings.Contains(task.Failure.UpstreamExcerpt, "reason=size_limit") {
		t.Fatalf("failure = %+v, want the narrow slot's size refusal", task.Failure)
	}
	if downloads := adapter.downloads(gltfLocator(task.CurrentAttemptID)); downloads != 1 {
		t.Fatalf("downloads = %d, want 1: the narrow slot is copied, not fetched", downloads)
	}
	// Both slots were written, each with its own limit and type.
	model, converted := store.writesFor(""), store.writesFor("converted.gltf")
	if len(model) != 1 || len(converted) != 1 {
		t.Fatalf("writes: model %d, converted %d; want one each (all: %+v)", len(model), len(converted), store.writes)
	}
	if model[0].MaxBytes != 1<<20 || converted[0].MaxBytes != int64(len(shared))-1 || converted[0].MediaType != "model/gltf-binary" {
		t.Fatalf("a slot was written with the other's contract: model %+v, converted %+v", model[0], converted[0])
	}
}

// What a failed store left no blob for is downloaded by the next claim: the narrow
// slot is stored first here, is refused, and so serves nobody.
func TestASlotThatWasNotStoredServesNoOtherClaim(t *testing.T) {
	shared := validGLB("narrow first")
	adapter := newDeclaredClaimsAdapter(shared, gltfSlots("z-model", "a-convert", "model"))
	task, _ := runNarrowSlotTask(t, adapter, int64(len(shared))-1)

	if task.State != generation.TaskFailed || len(task.Assets) != 0 {
		t.Fatalf("a slot over its limit was published: state=%s assets=%+v", task.State, task.Assets)
	}
	if downloads := adapter.downloads(gltfLocator(task.CurrentAttemptID)); downloads != 2 {
		t.Fatalf("downloads = %d, want 2: the refused first claim stored nothing to copy", downloads)
	}
}

// The same pair, when both slots accept the file, is one download and two records.
func TestCopiedSlotsOfAWiderContractAreStoredOnce(t *testing.T) {
	shared := validGLB("wide slots")
	adapter := newDeclaredClaimsAdapter(shared, gltfSlots("a-model", "z-convert", "converted"))
	task, store := runNarrowSlotTask(t, adapter, 1<<20)

	if task.State != generation.TaskSucceeded || len(task.Assets) != 2 {
		t.Fatalf("state=%s assets=%+v failure=%+v", task.State, task.Assets, task.Failure)
	}
	if downloads := adapter.downloads(gltfLocator(task.CurrentAttemptID)); downloads != 1 {
		t.Fatalf("downloads = %d, want 1", downloads)
	}
	if got := len(store.writes); got != 2 {
		t.Fatalf("writes = %d, want one per slot", got)
	}
}
