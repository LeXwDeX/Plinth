package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type switchableConversionRule struct {
	base   generation.DeliveryConversionRule
	active bool
}

func (r *switchableConversionRule) IsConversion(offering generation.OfferingID, input json.RawMessage) bool {
	return r.active && r.base.IsConversion(offering, input)
}

func (r *switchableConversionRule) ConversionIntent(offering generation.Offering, input json.RawMessage) (generation.ConversionIntent, bool) {
	if !r.active {
		return generation.ConversionIntent{}, false
	}
	return r.base.ConversionIntent(offering, input)
}

// tamperStore lets the auto path stay disabled while a targeted later
// preparation sees bytes that no longer match the registered digest.
type tamperStore struct {
	generation.AssetStore
	keys   map[string]bool
	tamper bool
}

func (s *tamperStore) Open(ctx context.Context, asset generation.Asset) (io.ReadCloser, error) {
	if s.tamper && s.keys[asset.StorageKey] {
		return io.NopCloser(strings.NewReader("tampered")), nil
	}
	return s.AssetStore.Open(ctx, asset)
}

func newCompletionHarness(t *testing.T) (*harness, *switchableStore, *countingPort) {
	t.Helper()
	return newCompletionHarnessWithPort(t, &countingPort{ProviderPort: mock.New()})
}

func newCompletionHarnessWithPort(t *testing.T, port generation.ProviderPort) (*harness, *switchableStore, *countingPort) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	counting, _ := port.(*countingPort)
	var store *switchableStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		deps.Assets = store
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
	return h, store, counting
}

type corruptGrantRepository struct {
	*sqlite.Repository
	taskID generation.TaskID
}

func (r *corruptGrantRepository) BoundGrantForTask(ctx context.Context, owner generation.OwnerID, taskID generation.TaskID) (generation.TaskGrant, bool, error) {
	grant, ok, err := r.Repository.BoundGrantForTask(ctx, owner, taskID)
	if err == nil && ok && taskID == r.taskID {
		grant.PreparedInputHash = strings.Repeat("0", 64)
	}
	return grant, ok, err
}

type hookStore struct {
	generation.AssetStore
	mu         sync.Mutex
	beforeOpen func()
	fired      bool
}

func (s *hookStore) Open(ctx context.Context, asset generation.Asset) (io.ReadCloser, error) {
	s.mu.Lock()
	hook := s.beforeOpen
	fire := hook != nil && !s.fired
	if fire {
		s.fired = true
	}
	s.mu.Unlock()
	if fire {
		hook()
	}
	return s.AssetStore.Open(ctx, asset)
}

func (s *hookStore) tripped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fired
}

type flakyDeliveryStore struct {
	generation.AssetStore
	failNextOpen bool
}

func (s *flakyDeliveryStore) Open(ctx context.Context, asset generation.Asset) (io.ReadCloser, error) {
	if s.failNextOpen {
		s.failNextOpen = false
		return nil, errors.New("temporary CAS read failure")
	}
	return s.AssetStore.Open(ctx, asset)
}

// failFirstConversionStart fails the first conversion Start before any upstream
// result exists, as the real local conversion A did. The retry is untouched.
type failFirstConversionStart struct {
	generation.ProviderPort
	mu     sync.Mutex
	failed bool
}

func (a *failFirstConversionStart) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	a.mu.Lock()
	fail := !a.failed && request.Offering.Capability == generation.FormatConversion
	if fail {
		a.failed = true
	}
	a.mu.Unlock()
	if fail {
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{Class: generation.FailureTransport, Retryable: true, SafeDetail: "conversion runtime unavailable"},
		}
	}
	return a.ProviderPort.Start(ctx, request)
}

func runToTerminal(t *testing.T, h *harness, owner generation.OwnerID, id generation.TaskID, limit int) generation.Task {
	t.Helper()
	for range limit {
		h.reconcile(t)
		task, err := h.module.Get(context.Background(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return task
		}
	}
	t.Fatalf("task %s did not reach a terminal state", id)
	return generation.Task{}
}

func completeRefetchFixture(t *testing.T) (*harness, generation.Task, generation.Task) {
	t.Helper()
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	delivered := runToTerminal(t, h, "owner-a", refetch.TaskID, 30)
	completed, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || completed.State != generation.TaskSucceeded {
		t.Fatalf("source completion = %#v %v", completed, err)
	}
	return h, completed, delivered
}

func TestRefetchCompletesTheFailedConversionTask330(t *testing.T) {
	h, store, port := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	if source.State != generation.TaskSucceeded || len(source.Assets) == 0 {
		t.Fatalf("source = %s assets=%d", source.State, len(source.Assets))
	}
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	if failed.State != generation.TaskFailed || len(failed.Assets) != 0 {
		t.Fatalf("failed conversion = %s assets=%d failure=%#v", failed.State, len(failed.Assets), failed.Failure)
	}
	original, err := h.repository.Load(ctx, failed.ID)
	if err != nil || original.CurrentAttempt == nil || original.CurrentAttempt.State != generation.AttemptFailed {
		t.Fatalf("failed attempt = %#v %v", original.CurrentAttempt, err)
	}
	originalAttempt := *original.CurrentAttempt
	startsBefore, _ := port.counts()
	store.setFailing(false)

	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	delivered := runToTerminal(t, h, "owner-a", refetch.TaskID, 30)
	if delivered.State != generation.TaskSucceeded || len(delivered.Assets) == 0 {
		t.Fatalf("refetch = %s assets=%d failure=%#v", delivered.State, len(delivered.Assets), delivered.Failure)
	}

	completed, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != generation.TaskSucceeded || completed.Progress != 100 || completed.Failure != nil ||
		len(completed.Assets) != len(delivered.Assets) || completed.Version != failed.Version+1 {
		t.Fatalf("completed source = %s progress=%d assets=%d version=%d failure=%#v", completed.State, completed.Progress, len(completed.Assets), completed.Version, completed.Failure)
	}
	aggregate, err := h.repository.Load(ctx, failed.ID)
	if err != nil || aggregate.CurrentAttempt == nil {
		t.Fatal(err)
	}
	attempt := aggregate.CurrentAttempt
	if attempt.State != generation.AttemptDeliveryCompleted || attempt.DeliveryCompletion == nil ||
		attempt.DeliveryCompletion.DeliveryTaskID != delivered.ID || attempt.DeliveryCompletion.Kind != generation.DeliveryCompletionRefetch ||
		attempt.DeliveryCompletion.OriginalAttemptID != originalAttempt.ID || attempt.UpstreamJob != nil || len(attempt.Opaque) != 0 ||
		attempt.LastObservationPhase != "" {
		t.Fatalf("completion attempt = %#v", attempt)
	}
	donorByID := map[generation.AssetID]generation.Asset{}
	for _, asset := range delivered.Assets {
		donorByID[asset.ID] = asset
	}
	for _, asset := range completed.Assets {
		if asset.AttemptID != attempt.ID || asset.TaskID != completed.ID || asset.StorageKey == "" {
			t.Fatalf("asset %#v is not owned by the completion attempt", asset)
		}
	}
	for _, mapping := range attempt.DeliveryCompletion.Assets {
		donor, ok := donorByID[mapping.SourceAssetID]
		if !ok {
			t.Fatalf("mapping names an unknown donor: %#v", mapping)
		}
		var copied generation.Asset
		for _, asset := range completed.Assets {
			if asset.ID == mapping.AssetID {
				copied = asset
			}
		}
		if copied.StorageKey != donor.StorageKey || copied.SHA256 != donor.SHA256 || copied.Size != donor.Size {
			t.Fatalf("copied asset does not share the donor CAS bytes: %#v / %#v", copied, donor)
		}
	}
	if attempt.DeliveryCompletion.ChargedCredits != 1 || !attempt.DeliveryCompletion.CreditsMetered {
		t.Fatalf("charge facts = %d metered=%v", attempt.DeliveryCompletion.ChargedCredits, attempt.DeliveryCompletion.CreditsMetered)
	}
	oldAttempt, err := h.repository.LoadAttemptForTask(ctx, "owner-a", failed.ID, originalAttempt.ID)
	if err != nil || oldAttempt.State != generation.AttemptFailed || oldAttempt.DeliveryCompletion != nil ||
		oldAttempt.Failure == nil || oldAttempt.UpstreamJob == nil {
		t.Fatalf("original attempt changed = %#v, %v", oldAttempt, err)
	}
	if starts, _ := port.counts(); starts != startsBefore {
		t.Fatalf("completion dispatched an upstream request: starts=%d, want %d", starts, startsBefore)
	}
	eligibility, err := h.module.RefetchEligibility(ctx, "owner-a", failed.ID)
	if err != nil || eligibility.Eligible || eligibility.Reason != generation.RefetchDelivered || eligibility.RefetchTaskID != delivered.ID {
		t.Fatalf("eligibility after completion = %#v, %v", eligibility, err)
	}
	events, err := h.module.Events(ctx, "owner-a", failed.ID, 0, 100)
	if err != nil || countEvents(events, "task.succeeded") != 1 {
		t.Fatalf("completion events = %v, %v", eventTypes(events), err)
	}
}

func secondSchemaModule(t *testing.T, h *harness) *generation.Module {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	adapter := sixFormatFixtureAdapter{next: mock.New()}
	for _, key := range []string{"openai-images.v1", "gemini-images.v1", "meshy.v2", "tripo.v3", "matting.v1"} {
		if err := registry.Register(key, adapter); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{
		Repository: h.repository, Catalog: product, Providers: registry, Assets: h.assets,
		Inputs: h.inputs, Cache: h.cache, Clock: h.clock, SchemaVersion: "completion-second-schema",
		Conversions: modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return module
}

func submitWithPlanGrant(t *testing.T, h *harness, owner generation.OwnerID, capability generation.Capability, offering generation.OfferingID, input []byte, planID, planStep, key string) generation.TaskID {
	t.Helper()
	return submitWithPlanGrantOn(t, h, h.module, owner, capability, offering, input, planID, planStep, key)
}

func submitWithPlanGrantOn(t *testing.T, h *harness, module *generation.Module, owner generation.OwnerID, capability generation.Capability, offering generation.OfferingID, input []byte, planID, planStep, key string) generation.TaskID {
	t.Helper()
	ctx := context.Background()
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: owner, SessionID: "generation-test-session:" + string(owner),
		Capability: capability, OfferingID: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: string(owner), ActorID: "generation-test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: planID, PlanStepID: planStep,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: owner, SessionID: prepared.SessionID,
		PreparedInputID: prepared.ID, IdempotencyKey: key,
		OperationID: planID, PlanStepID: planStep, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func TestPlanRetryCompletesTheFailedConversionTask330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	input := []byte(`{"source_task":"` + string(source.ID) + `"}`)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-330", "0:0", "processing:plan-330:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	if failed.State != generation.TaskFailed || len(failed.Assets) != 0 {
		t.Fatalf("failed conversion = %s assets=%d", failed.State, len(failed.Assets))
	}
	store.setFailing(false)
	second := secondSchemaModule(t, h)
	deliveryID := submitWithPlanGrantOn(t, h, second, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-330", "0:1", "processing:plan-330:0:1")
	delivered := runToTerminal(t, h, "owner-a", deliveryID, 30)
	if delivered.State != generation.TaskSucceeded || len(delivered.Assets) == 0 {
		t.Fatalf("plan retry = %s assets=%d", delivered.State, len(delivered.Assets))
	}
	completed, err := h.module.Get(ctx, "owner-a", failedID)
	if err != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) != len(delivered.Assets) {
		t.Fatalf("completed failed task = %#v %v", completed, err)
	}
	aggregate, err := h.repository.Load(ctx, failedID)
	if err != nil || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.DeliveryCompletion == nil {
		t.Fatalf("completion attempt = %#v %v", aggregate.CurrentAttempt, err)
	}
	aGrant, ok, err := h.repository.BoundGrantForTask(ctx, "owner-a", failedID)
	if err != nil || !ok {
		t.Fatalf("A grant = %#v %v", aGrant, err)
	}
	bGrant, ok, err := h.repository.BoundGrantForTask(ctx, "owner-a", delivered.ID)
	if err != nil || !ok {
		t.Fatalf("B grant = %#v %v", bGrant, err)
	}
	aPrepared, err := h.module.GetPreparedInput(ctx, "owner-a", aGrant.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	bPrepared, err := second.GetPreparedInput(ctx, "owner-a", bGrant.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	if aPrepared.InputHash == bPrepared.InputHash || aPrepared.SchemaVersion == bPrepared.SchemaVersion {
		t.Fatalf("fixture must prepare under distinct Schema revisions: %s / %s", aPrepared.SchemaVersion, bPrepared.SchemaVersion)
	}
	if !reflect.DeepEqual(aPrepared.NormalizedInput, bPrepared.NormalizedInput) || aPrepared.OfferingID != bPrepared.OfferingID ||
		aPrepared.OfferingRevision != bPrepared.OfferingRevision {
		t.Fatalf("normalized contract differs: %#v / %#v", aPrepared, bPrepared)
	}
	provenance := aggregate.CurrentAttempt.DeliveryCompletion
	if provenance.Kind != generation.DeliveryCompletionPlanRetry || provenance.PlanRetry == nil ||
		provenance.PlanRetry.PlanID != "plan-330" || provenance.PlanRetry.StepIndex != 0 || provenance.PlanRetry.Retry != 1 ||
		provenance.PlanRetry.PreparedInputHash == "" || provenance.DeliveryTaskID != delivered.ID {
		t.Fatalf("plan retry provenance = %#v", provenance)
	}
}

func TestDeliveryCompletionRejectsTamperedBytesAndLeavesTheFailedTask330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rule := &switchableConversionRule{base: modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)}
	port := &countingPort{ProviderPort: mock.New()}
	var store *tamperStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		deps.Conversions = rule
		store = &tamperStore{AssetStore: deps.Assets}
		deps.Assets = store
	})
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	failing := &switchableStore{AssetStore: store.AssetStore}
	store.AssetStore = failing
	failing.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	failing.setFailing(false)
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	delivered := runToTerminal(t, h, "owner-a", refetch.TaskID, 30)
	if delivered.State != generation.TaskSucceeded || len(delivered.Assets) == 0 {
		t.Fatalf("delivery = %s assets=%d", delivered.State, len(delivered.Assets))
	}
	rule.active = true
	store.keys = map[string]bool{}
	for _, asset := range delivered.Assets {
		store.keys[asset.StorageKey] = true
	}
	store.tamper = true
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID); !errors.Is(err, generation.ErrDeliveryCompletionRejected) {
		t.Fatalf("tampered preparation = %v", err)
	}
	unchanged, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || unchanged.State != generation.TaskFailed || len(unchanged.Assets) != 0 {
		t.Fatalf("failed task changed while preparation was rejected: %#v %v", unchanged, err)
	}
	store.tamper = false
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.module.CompleteDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent completion = %v", err)
		}
	}
	completed, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) != len(delivered.Assets) {
		t.Fatalf("completed = %#v %v", completed, err)
	}
}

func TestDeliveryCompletionIsIdempotent330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	delivered := runToTerminal(t, h, "owner-a", refetch.TaskID, 30)
	if delivered.State != generation.TaskSucceeded {
		t.Fatalf("delivery = %s", delivered.State)
	}
	// The auto path already completed the source; the exact same delivery is
	// idempotent and never writes another asset or Attempt.
	again, err := h.module.CompleteDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID)
	if err != nil || !again.AlreadyCompleted || again.DeliveryTaskID != delivered.ID {
		t.Fatalf("idempotent completion = %#v, %v", again, err)
	}
	aggregate, err := h.repository.Load(ctx, failed.ID)
	if err != nil || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptDeliveryCompleted {
		t.Fatalf("completed attempt = %#v, %v", aggregate.CurrentAttempt, err)
	}
	if aggregate.CurrentAttempt.FinishedAt == nil || aggregate.CurrentAttempt.DeliveryCompletion == nil {
		t.Fatalf("completion attempt lacks its terminal facts: %#v", aggregate.CurrentAttempt)
	}
}

func TestPlanRetryOfAnotherInputDoesNotCompleteTheFailedConversion330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	makeSource := func(label string) generation.Task {
		accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
			OwnerID: "owner-a", Capability: generation.TextTo3D,
			OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"` + label + `"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	}
	source1, source2 := makeSource("first source"), makeSource("second source")
	store.setFailing(true)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert",
		[]byte(`{"source_task":"`+string(source1.ID)+`"}`), "plan-331", "0:0", "processing:plan-331:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	store.setFailing(false)
	deliveryID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert",
		[]byte(`{"source_task":"`+string(source2.ID)+`"}`), "plan-331", "0:1", "processing:plan-331:0:1")
	delivered := runToTerminal(t, h, "owner-a", deliveryID, 30)
	if delivered.State != generation.TaskSucceeded {
		t.Fatalf("delivery = %s", delivered.State)
	}
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID); !errors.Is(err, generation.ErrDeliveryCompletionRejected) {
		t.Fatalf("different input was accepted: %v", err)
	}
	unchanged, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || unchanged.State != generation.TaskFailed || len(unchanged.Assets) != 0 {
		t.Fatalf("failed input was completed: %#v %v", unchanged, err)
	}
}

func TestDeliveryCompletionRejectsAnotherOwnerAndSelfPair330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"completion source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert", Input: []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-b", failed.ID, failed.ID); !errors.Is(err, generation.ErrDeliveryCompletionNotApplicable) {
		t.Fatalf("another owner = %v", err)
	}
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-a", failed.ID, failed.ID); !errors.Is(err, generation.ErrDeliveryCompletionNotApplicable) {
		t.Fatalf("self pair = %v", err)
	}
}

func TestDeliveryCompletionRejectsAMismatchedPlanStep330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"step source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	input := []byte(`{"source_task":"` + string(source.ID) + `"}`)
	store.setFailing(true)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-332", "0:0", "processing:plan-332:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	store.setFailing(false)
	deliveryID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-332", "1:1", "processing:plan-332:0:1")
	delivered := runToTerminal(t, h, "owner-a", deliveryID, 30)
	if delivered.State != generation.TaskSucceeded {
		t.Fatalf("delivery = %s", delivered.State)
	}
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID); !errors.Is(err, generation.ErrDeliveryCompletionRejected) {
		t.Fatalf("mismatched plan step was accepted: %v", err)
	}
	unchanged, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || unchanged.State != generation.TaskFailed {
		t.Fatalf("failed task changed: %#v %v", unchanged, err)
	}
}

func TestPlanRetryCompletesAConversionThatFailedBeforeAnyUpstreamResult330(t *testing.T) {
	port := &failFirstConversionStart{ProviderPort: mock.New()}
	h, _, _ := newCompletionHarnessWithPort(t, port)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"runtime failure source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	input := []byte(`{"source_task":"` + string(source.ID) + `"}`)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-333", "0:0", "processing:plan-333:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.Class != generation.FailureTransport || len(failed.Assets) != 0 {
		t.Fatalf("runtime failure = %s %#v assets=%d", failed.State, failed.Failure, len(failed.Assets))
	}
	aggregate, err := h.repository.Load(ctx, failedID)
	if err != nil || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.LastObservationPhase == generation.UpstreamCompleted {
		t.Fatalf("fixture must fail before an upstream result: %#v %v", aggregate.CurrentAttempt, err)
	}
	deliveryID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-333", "0:1", "processing:plan-333:0:1")
	delivered := runToTerminal(t, h, "owner-a", deliveryID, 30)
	if delivered.State != generation.TaskSucceeded || len(delivered.Assets) == 0 {
		t.Fatalf("retry = %s assets=%d", delivered.State, len(delivered.Assets))
	}
	completed, err := h.module.Get(ctx, "owner-a", failedID)
	if err != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) != len(delivered.Assets) {
		t.Fatalf("completed = %#v %v", completed, err)
	}
}

func TestDeliveryCompletionRejectsAGrantWhoseOwnPreparedHashNoLongerMatches330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	port := &countingPort{ProviderPort: mock.New()}
	var base *sqlite.Repository
	var store *switchableStore
	var corrupt *corruptGrantRepository
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		base = deps.Repository.(*sqlite.Repository)
		corrupt = &corruptGrantRepository{Repository: base}
		deps.Repository = corrupt
		store = &switchableStore{AssetStore: deps.Assets}
		deps.Assets = store
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"grant hash source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	input := []byte(`{"source_task":"` + string(source.ID) + `"}`)
	store.setFailing(true)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-334", "0:0", "processing:plan-334:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	store.setFailing(false)
	deliveryID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-334", "0:1", "processing:plan-334:0:1")
	corrupt.taskID = deliveryID
	delivered := runToTerminal(t, h, "owner-a", deliveryID, 30)
	if delivered.State != generation.TaskSucceeded {
		t.Fatalf("delivery = %s", delivered.State)
	}
	if _, err := h.module.PrepareDeliveryCompletion(ctx, "owner-a", failed.ID, delivered.ID); !errors.Is(err, generation.ErrDeliveryCompletionRejected) {
		t.Fatalf("corrupt grant hash accepted: %v", err)
	}
	unchanged, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || unchanged.State != generation.TaskFailed || len(unchanged.Assets) != 0 {
		t.Fatalf("failed task changed: %#v %v", unchanged, err)
	}
}

func reconcileTolerantly(h *harness) {
	_, _ = h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID: "test-worker", LeaseFor: time.Minute, BatchSize: 10,
	})
	h.clock.Advance(31 * time.Second)
}

func TestDeliveryCompletionWaitsForTemporaryCASReadFailure330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	port := &countingPort{ProviderPort: mock.New()}
	var store *switchableStore
	var flaky *flakyDeliveryStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		flaky = &flakyDeliveryStore{AssetStore: store}
		deps.Assets = flaky
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"temporary CAS source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	flaky.failNextOpen = true
	for range 40 {
		reconcileTolerantly(h)
		task, getErr := h.module.Get(ctx, "owner-a", refetch.TaskID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.State == generation.TaskSucceeded {
			completed, getErr := h.module.Get(ctx, "owner-a", failed.ID)
			if getErr != nil || completed.State != generation.TaskSucceeded {
				t.Fatalf("delivery succeeded but source did not: %#v %v", completed, getErr)
			}
			return
		}
	}
	t.Fatal("delivery did not recover after the temporary CAS read failure")
}

func TestDeliveryCompletionRollsBackWithTheDeliveryCommit330(t *testing.T) {
	h, store, _ := newCompletionHarness(t)
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"rollback source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	raw, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	trigger := `CREATE TRIGGER fail_completion_asset BEFORE INSERT ON generation_assets
		WHEN NEW.task_id = '` + string(failed.ID) + `'
		BEGIN SELECT RAISE(ABORT, 'injected completion asset failure'); END`
	if _, err := raw.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		reconcileTolerantly(h)
	}
	deliveryTask, err := h.module.Get(ctx, "owner-a", refetch.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if deliveryTask.State != generation.TaskMaterializing {
		t.Fatalf("the delivery must stay materializing after a rolled-back completion: %s", deliveryTask.State)
	}
	unchanged, err := h.module.Get(ctx, "owner-a", failed.ID)
	if err != nil || unchanged.State != generation.TaskFailed || len(unchanged.Assets) != 0 {
		t.Fatalf("failed task changed despite rollback: %#v %v", unchanged, err)
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_completion_asset`); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		reconcileTolerantly(h)
		task, getErr := h.module.Get(ctx, "owner-a", refetch.TaskID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.State == generation.TaskSucceeded {
			completed, getErr := h.module.Get(ctx, "owner-a", failed.ID)
			if getErr != nil || completed.State != generation.TaskSucceeded || len(completed.Assets) == 0 {
				t.Fatalf("source completion = %#v %v", completed, getErr)
			}
			return
		}
	}
	t.Fatal("delivery did not recover after the injected rollback")
}

func assetBody(t *testing.T, h *harness, task generation.Task, asset generation.Asset) []byte {
	t.Helper()
	content, err := h.module.OpenAssetScoped(context.Background(), generation.OwnTasks(task.OwnerID), task.ID, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCompletedConversionKeepsItsAssetsWhenEitherTaskIsDeleted330(t *testing.T) {
	h, completed, delivered := completeRefetchFixture(t)
	ctx := context.Background()
	// The original failed Task exposes its real success and its own bytes to
	// the owner, and refuses another owner.
	refreshed, err := h.module.Get(ctx, "owner-a", completed.ID)
	if err != nil || refreshed.State != generation.TaskSucceeded || len(refreshed.Assets) == 0 {
		t.Fatalf("completed task API = %#v %v", refreshed, err)
	}
	for _, asset := range refreshed.Assets {
		body := assetBody(t, h, refreshed, asset)
		if len(body) != int(asset.Size) {
			t.Fatalf("asset %s bytes = %d, recorded %d", asset.ID, len(body), asset.Size)
		}
	}
	if _, err := h.module.OpenAssetScoped(ctx, generation.OwnTasks("owner-b"), refreshed.ID, refreshed.Assets[0].ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("cross-owner read = %v", err)
	}
	policy := generation.TaskDeletionPolicy{
		LocalRecordPolicy: "tombstone", UpstreamPolicy: "when-supported",
		UpstreamFailurePolicy: "abort", GeneratedAssetPolicy: "retain", AuditEventType: "task.deleted",
	}
	if err := h.module.Delete(ctx, generation.DeleteCommand{
		Scope: generation.OwnTasks("owner-a"), ActorID: "owner-a", TaskID: delivered.ID,
		ExpectedCapability: generation.FormatConversion, Policy: policy,
	}); err != nil {
		t.Fatal(err)
	}
	afterDeleteDelivery, err := h.module.Get(ctx, "owner-a", completed.ID)
	if err != nil || len(afterDeleteDelivery.Assets) != len(refreshed.Assets) {
		t.Fatalf("source assets after deleting delivery = %#v %v", afterDeleteDelivery, err)
	}
	for _, asset := range afterDeleteDelivery.Assets {
		if len(assetBody(t, h, afterDeleteDelivery, asset)) == 0 {
			t.Fatalf("source asset %s is unreadable after deleting the delivery task", asset.ID)
		}
	}

	h2, completed2, delivered2 := completeRefetchFixture(t)
	if err := h2.module.Delete(context.Background(), generation.DeleteCommand{
		Scope: generation.OwnTasks("owner-a"), ActorID: "owner-a", TaskID: completed2.ID,
		ExpectedCapability: generation.FormatConversion, Policy: policy,
	}); err != nil {
		t.Fatal(err)
	}
	afterDeleteSource, err := h2.module.Get(context.Background(), "owner-a", delivered2.ID)
	if err != nil || len(afterDeleteSource.Assets) != len(delivered2.Assets) {
		t.Fatalf("delivery assets after deleting source = %#v %v", afterDeleteSource, err)
	}
	for _, asset := range afterDeleteSource.Assets {
		if len(assetBody(t, h2, afterDeleteSource, asset)) == 0 {
			t.Fatalf("delivery asset %s is unreadable after deleting the source task", asset.ID)
		}
	}
}

func tombstoneTask(t *testing.T, h *harness, id generation.TaskID) {
	t.Helper()
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repository.Tombstone(context.Background(), generation.Tombstone{
		TaskID: id, ActorID: "owner-a", ExpectedVersion: aggregate.Task.Version,
		DeletedAt: h.clock.Now(),
		Event:     generation.Event{TaskID: id, Type: "task.deleted", Data: json.RawMessage(`{}`), CreatedAt: h.clock.Now()},
	}); err != nil {
		t.Fatal(err)
	}
}

func tombstonedTaskAttemptsAndAssets(t *testing.T, h *harness, id generation.TaskID) (int, int) {
	t.Helper()
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var attempts, assets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_attempts WHERE task_id=?`, id).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_assets WHERE task_id=?`, id).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	return attempts, assets
}

func TestRefetchSourceDeletedBeforeCompletionCommitSkipsAndSucceeds330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	port := &countingPort{ProviderPort: mock.New()}
	var store *switchableStore
	var hook *hookStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		hook = &hookStore{AssetStore: store}
		deps.Assets = hook
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"deleted source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	store.setFailing(true)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert",
		Input:      []byte(`{"source_task":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := runToTerminal(t, h, "owner-a", accepted.TaskID, 30)
	store.setFailing(false)
	hook.beforeOpen = func() { tombstoneTask(t, h, failed.ID) }
	refetch, err := h.module.RefetchUpstreamResult(ctx, "owner-a", failed.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	conflictObserved := false
	for range 40 {
		reconcileTolerantly(h)
		delivery, getErr := h.module.Get(ctx, "owner-a", refetch.TaskID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if hook.tripped() && !conflictObserved {
			conflictObserved = true
			// The asset open hook deleted A after preparation. This pass must
			// fail its completion update and roll back B's success commit.
			if delivery.State == generation.TaskSucceeded {
				t.Fatal("the delivery committed in the pass that deleted its source")
			}
		}
		if hook.tripped() && delivery.State == generation.TaskSucceeded {
			break
		}
	}
	delivery, err := h.module.Get(ctx, "owner-a", refetch.TaskID)
	if err != nil || delivery.State != generation.TaskSucceeded {
		t.Fatalf("delivery after source deletion = %#v %v", delivery, err)
	}
	if _, err := h.module.Get(ctx, "owner-a", failed.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("tombstoned source is visible: %v", err)
	}
	if tombstoned, err := h.repository.TaskTombstoned(ctx, failed.ID); err != nil || !tombstoned {
		t.Fatalf("source tombstone = %v %v", tombstoned, err)
	}
	if !hook.tripped() {
		t.Fatal("the deletion hook never fired")
	}
	if attempts, assets := tombstonedTaskAttemptsAndAssets(t, h, failed.ID); attempts != 1 || assets != 0 {
		t.Fatalf("completion wrote rows for a tombstoned source: attempts=%d assets=%d", attempts, assets)
	}
}

func TestPlanRetrySourceDeletedBeforeCompletionCommitSkipsAndSucceeds330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	port := &countingPort{ProviderPort: mock.New()}
	var store *switchableStore
	var hook *hookStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		hook = &hookStore{AssetStore: store}
		deps.Assets = hook
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
	ctx := context.Background()
	sourceAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"deleted plan source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := runToTerminal(t, h, "owner-a", sourceAccepted.TaskID, 30)
	input := []byte(`{"source_task":"` + string(source.ID) + `"}`)
	store.setFailing(true)
	failedID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-335", "0:0", "processing:plan-335:0:0")
	failed := runToTerminal(t, h, "owner-a", failedID, 30)
	store.setFailing(false)
	deliveryID := submitWithPlanGrant(t, h, "owner-a", generation.FormatConversion, "meshy/processing/convert", input,
		"plan-335", "0:1", "processing:plan-335:0:1")
	hook.beforeOpen = func() { tombstoneTask(t, h, failed.ID) }
	conflictObserved := false
	for range 40 {
		reconcileTolerantly(h)
		delivery, getErr := h.module.Get(ctx, "owner-a", deliveryID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if hook.tripped() && !conflictObserved {
			conflictObserved = true
			if delivery.State == generation.TaskSucceeded {
				t.Fatal("the plan retry committed in the pass that deleted its source")
			}
		}
		if hook.tripped() && delivery.State == generation.TaskSucceeded {
			break
		}
	}
	delivery, err := h.module.Get(ctx, "owner-a", deliveryID)
	if err != nil || delivery.State != generation.TaskSucceeded {
		t.Fatalf("plan retry after source deletion = %#v %v", delivery, err)
	}
	if _, err := h.module.Get(ctx, "owner-a", failed.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("tombstoned source is visible: %v", err)
	}
	if tombstoned, err := h.repository.TaskTombstoned(ctx, failed.ID); err != nil || !tombstoned {
		t.Fatalf("source tombstone = %v %v", tombstoned, err)
	}
	if !hook.tripped() {
		t.Fatal("the deletion hook never fired")
	}
	if attempts, assets := tombstonedTaskAttemptsAndAssets(t, h, failed.ID); attempts != 1 || assets != 0 {
		t.Fatalf("completion wrote rows for a tombstoned source: attempts=%d assets=%d", attempts, assets)
	}
}
