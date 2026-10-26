package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
)

func TestAcceptanceMarkerConcurrentAndRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "markers"), 0700); err != nil {
		t.Fatal(err)
	}
	s := acceptanceScenario{ID: "one", OwnerID: "owner", SessionID: "session", PlanID: strings.Repeat("a", 64), OfferingID: "meshy/processing/rigging"}
	c := &acceptanceController{root: root}
	var wins atomic.Int32
	var wait sync.WaitGroup
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			claimed, err := c.claim(s, "start")
			if err != nil {
				t.Error(err)
			}
			if claimed {
				wins.Add(1)
			}
		}()
	}
	wait.Wait()
	if wins.Load() != 1 {
		t.Fatalf("one shot consumed %d times", wins.Load())
	}
	reopened := &acceptanceController{root: root}
	if claimed, err := reopened.claim(s, "start"); err != nil || claimed {
		t.Fatalf("restart reinjected: %v %v", claimed, err)
	}
	other := s
	other.OwnerID = "other-owner"
	if claimed, err := reopened.claim(other, "start"); err != nil || !claimed {
		t.Fatalf("owner scope collided: %v %v", claimed, err)
	}
}

func TestAcceptanceAssemblyDefaultsAndSingleProcessingService(t *testing.T) {
	root := t.TempDir()
	config := Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "test.db"), StorageRoot: filepath.Join(root, "assets"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner", RuntimeSettings: map[string]string{"llm_model": "test-model"}}
	registryCalls, gatewayCalls := 0, 0
	application, err := newWithDependencies(config, discardLogger(), assemblyOverrides{providers: func(p generation.ProviderRegistry) generation.ProviderRegistry { registryCalls++; return p }, processingGateway: func(g processing.Gateway) processing.Gateway { gatewayCalls++; return g }})
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	if registryCalls != 1 || gatewayCalls != 1 || application.processing == nil {
		t.Fatalf("assembly calls providers=%d processing=%d", registryCalls, gatewayCalls)
	}
	response := appChatRequest(application.server.Handler, http.MethodGet, "/api/v1/schema", "")
	if response.Code != http.StatusOK {
		t.Fatalf("identity wrappers changed HTTP: %d", response.Code)
	}
	ordinary, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "ordinary.db"), StorageRoot: filepath.Join(root, "ordinary-assets"), LegacyGeneratedImagesRoot: filepath.Join(root, "ordinary-generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner", RuntimeSettings: map[string]string{"llm_model": "test-model"}}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.generation.Close()
	if !reflect.DeepEqual(application.schema, ordinary.schema) {
		t.Fatal("nil assembly hooks changed ordinary Schema")
	}
}

func TestAcceptanceMeshyRetainsDependencyResolver(t *testing.T) {
	adapter, err := meshy.New(meshy.Config{BaseURL: "https://example.invalid", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", adapter); err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{AdapterKey: "meshy.v2"}
	wrapped, ok := (acceptanceRegistry{registry, &acceptanceController{}}).Provider(offering)
	if !ok {
		t.Fatal("adapter missing")
	}
	resolver, ok := wrapped.(generation.DependencyJobResolver)
	if !ok {
		t.Fatal("dependency resolver lost")
	}
	request := generation.DependencyJobRequest{Offering: offering, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "source"}}
	want, wantErr := adapter.ResolveDependencyJob(request)
	got, gotErr := resolver.ResolveDependencyJob(request)
	if !reflect.DeepEqual(want, got) || (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("resolver changed: %+v %v vs %+v %v", want, wantErr, got, gotErr)
	}
	if _, ok := wrapped.(generation.ProviderWebhookDecoder); !ok {
		t.Fatal("webhook decoder lost")
	}
	if _, ok := wrapped.(generation.ProviderTaskDeleter); !ok {
		t.Fatal("task deleter lost")
	}
}

func TestAcceptanceBalanceUsesActualImageWireMapping(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var offering generation.Offering
	var mapping *generation.ErrorMapping
	for _, c := range product.Application().Runtime.ProviderConnections {
		if c.ID == "openai-images" {
			mapping = c.ErrorMapping
			for _, o := range product.List(generation.TextToImage) {
				if o.AdapterKey == c.AdapterKey {
					offering = o
					break
				}
			}
		}
	}
	if offering.ID == "" || mapping == nil {
		t.Fatal("OpenAI image contract missing")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`))
	}))
	defer server.Close()
	adapter, err := schemaimages.New(schemaimages.Config{BaseURL: server.URL, APIKey: "test", ErrorMapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	normalizedOffering, input, err := product.Normalize(generation.TextToImage, offering.ID, json.RawMessage(`{"prompt":"acceptance image"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Start(context.Background(), generation.StartRequest{TaskID: "wire-task", AttemptID: "wire-attempt", SubmissionKey: "wire-submission", Offering: normalizedOffering, Input: input})
	var failure *generation.ProviderError
	if !errors.As(err, &failure) || failure.Failure.Class != generation.FailureInsufficientBalance || failure.Failure.Retryable || failure.Failure.Ambiguous || calls.Load() != 1 {
		t.Fatalf("actual wire mapping failed: %v requests=%d", err, calls.Load())
	}
}

type acceptanceTestRig struct {
	ctx     context.Context
	root    string
	product *catalog.Catalog
	repo    *sqlite.Repository
	clock   *fakeAcmeClock
	module  *generation.Module
	control *acceptanceController
	plans   *processing.Service
	store   *recoveryPlanStore
	source  generation.Task
	grants  executiongrant.Service
}

func newAcceptanceTestRig(t *testing.T) *acceptanceTestRig {
	t.Helper()
	r := &acceptanceTestRig{ctx: context.Background(), root: t.TempDir(), clock: &fakeAcmeClock{now: time.Now().UTC()}}
	var err error
	r.product, err = catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	r.repo, err = sqlite.Open(filepath.Join(r.root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	r.repo.SetClock(r.clock.Now)
	assets, err := local.New(filepath.Join(r.root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.NewWithConfig(r.repo, assets, inputassets.Config{ProviderAssetRoute: r.product.Application().Runtime.HTTP.Routes.ProviderAsset})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", mock.New()); err != nil {
		t.Fatal(err)
	}
	r.module, err = generation.New(generation.Dependencies{Repository: r.repo, Catalog: r.product, Providers: registry, Assets: assets, Inputs: inputs, Clock: r.clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.module.Close() })
	if _, err := r.repo.CreateSession(r.ctx, chat.Session{ID: "session", OwnerID: "owner", Title: "acceptance", CreatedAt: r.clock.Now(), UpdatedAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	r.grants = executiongrant.Service{Grants: r.repo, Prepared: r.repo}
	prepared, err := r.module.PrepareInput(r.ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"offline acceptance source"}`)})
	if err != nil {
		t.Fatal(err)
	}
	human := executiongrant.Principal{OwnerID: "owner", ActorID: "test-human", Source: executiongrant.HumanBrowser}
	grant, err := r.grants.Prepare(r.ctx, executiongrant.PrepareCommand{Principal: human, SessionID: "session", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "acceptance-source", OperationID: "acceptance-source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.grants.Decide(r.ctx, human, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := r.module.SubmitPrepared(r.ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID, AuthorizationRef: grant.RequestID, OperationID: "acceptance-source", IdempotencyKey: "acceptance-source"})
	if err != nil {
		t.Fatal(err)
	}
	r.source = r.finish(t, accepted.TaskID)
	r.control = &acceptanceController{root: r.root, manifestPath: filepath.Join(r.root, "manifest.json"), module: r.module, admissions: r.repo, manifest: acceptanceManifest{OwnerID: "owner", SourceSHA: strings.Repeat("a", 40), Address: "127.0.0.1:24567", CatalogFile: "catalog", WebAssetsRoot: "web"}}
	if err := os.Mkdir(filepath.Join(r.root, "markers"), 0700); err != nil {
		t.Fatal(err)
	}
	gateway := &acceptanceGateway{Gateway: r.module, control: r.control}
	r.store = &recoveryPlanStore{Repository: r.repo}
	r.plans, err = processing.New(r.store, recoveryCatalog{r.product}, gateway, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	r.control.plans = r.plans
	app := r.product.Application()
	var known, manual []string
	for _, cap := range app.Capabilities {
		known = append(known, string(cap.ID))
	}
	for _, cap := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(cap))
	}
	r.plans.WithGrants(&executiongrant.Service{Grants: r.repo, Prepared: r.repo, Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known), Modes: recoveryManualMode{}})
	return r
}

func (r *acceptanceTestRig) finish(t *testing.T, id generation.TaskID) generation.Task {
	t.Helper()
	for range 30 {
		if _, err := r.module.ReconcileOnce(r.ctx, generation.CoordinatorConfig{WorkerID: "acceptance-worker", LeaseFor: time.Minute, BatchSize: 10}); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(time.Minute)
		task, err := r.module.Get(r.ctx, "owner", id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			t.Fatalf("task failed: %+v", task.Failure)
		}
	}
	t.Fatal("task did not finish")
	return generation.Task{}
}
func (r *acceptanceTestRig) tick(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	r.store.cancel = cancel
	defer func() { r.store.cancel = nil }()
	if err := r.plans.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("processing did not persist a transition")
	}
}
func (r *acceptanceTestRig) create(t *testing.T, fault string) processing.Plan {
	t.Helper()
	offering, _ := r.product.Get("meshy/processing/rigging")
	var asset generation.AssetID
	for _, a := range r.source.Assets {
		if a.Role == "model" && a.Variant == "glb" {
			asset = a.ID
		}
	}
	key := "acceptance-plan-" + fault
	s := acceptanceScenario{ID: fault, OwnerID: "owner", SessionID: "session", PlanID: processing.PlanID("owner", key), StepIndex: 0, OfferingID: offering.ID, Capability: offering.Capability, Fault: fault, MaxProviderStarts: 2}
	r.control.manifest.Scenarios = []acceptanceScenario{s}
	if err := acceptanceWriteJSON(r.control.manifestPath, r.control.manifest); err != nil {
		t.Fatal(err)
	}
	p, err := r.plans.Create(r.ctx, processing.CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: r.source.ID, SourceAssetID: asset, IdempotencyKey: key, Steps: []processing.StepRequest{{OfferingID: offering.ID, ExpectedOfferingRevision: offering.Revision, Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (r *acceptanceTestRig) get(t *testing.T, id string) processing.Plan {
	t.Helper()
	p, err := r.plans.Get(r.ctx, "owner", id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAcceptanceRealGatewayRecoveryTransitions(t *testing.T) {
	for _, fault := range []string{"submit_response_lost", "submit_before_acceptance", "prepare_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			r := newAcceptanceTestRig(t)
			p := r.create(t, fault)
			r.tick(t)
			paused := r.get(t, p.ID)
			want := processing.ReasonSubmitUnknown
			if fault == "prepare_unavailable" {
				want = processing.ReasonPrepareUnavailable
			}
			if paused.State != processing.Paused || paused.Failure != want {
				t.Fatalf("unexpected pause: %+v", paused)
			}
			frozen := paused.Steps[0]
			if fault == "prepare_unavailable" && (frozen.PreparedInputID != "" || frozen.AuthorizationRef != "") {
				t.Fatal("transient prepare manufactured frozen grant")
			}
			if _, err := r.plans.Resume(r.ctx, processing.ResumeCommand{OwnerID: "owner", PlanID: p.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: frozen.Revision}); err != nil {
				t.Fatal(err)
			}
			resumed := r.get(t, p.ID)
			if fault != "prepare_unavailable" {
				if resumed.Failure != processing.ReasonSubmitUnreconciled {
					t.Fatalf("expected unreconciled: %+v", resumed)
				}
				if _, err := r.plans.Reconcile(r.ctx, processing.ReconcileCommand{OwnerID: "other", PlanID: p.ID, ExpectedPlanVersion: resumed.Version}); !errors.Is(err, processing.ErrMissing) {
					t.Fatalf("cross-owner reconcile: %v", err)
				}
				reconciled, err := r.plans.Reconcile(r.ctx, processing.ReconcileCommand{OwnerID: "owner", PlanID: p.ID, ExpectedPlanVersion: resumed.Version})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.plans.Reconcile(r.ctx, processing.ReconcileCommand{OwnerID: "owner", PlanID: p.ID, ExpectedPlanVersion: resumed.Version}); !errors.Is(err, processing.ErrConflict) {
					t.Fatalf("stale reconciliation: %v", err)
				}
				same, err := r.plans.Reconcile(r.ctx, processing.ReconcileCommand{OwnerID: "owner", PlanID: p.ID, ExpectedPlanVersion: reconciled.Version})
				if err != nil || !reflect.DeepEqual(same, reconciled) {
					t.Fatal("repeated reconciliation mutated plan", err)
				}
				if reconciled.Steps[0].PreparedInputID != frozen.PreparedInputID || reconciled.Steps[0].AuthorizationRef != frozen.AuthorizationRef || reconciled.Steps[0].IdempotencyKey != frozen.IdempotencyKey {
					t.Fatal("reconciliation replaced frozen identity")
				}
				if fault == "submit_before_acceptance" {
					if reconciled.Failure != processing.ReasonReconciledAbsent || reconciled.Steps[0].TaskID != "" {
						t.Fatalf("absence not proven: %+v", reconciled)
					}
					if _, err := r.plans.Resume(r.ctx, processing.ResumeCommand{OwnerID: "owner", PlanID: p.ID, ExpectedPlanVersion: reconciled.Version, ExpectedOfferingRevision: frozen.Revision}); err != nil {
						t.Fatal(err)
					}
					r.tick(t)
				}
			} else {
				r.tick(t)
			}
			taskID := r.get(t, p.ID).Steps[0].TaskID
			if taskID == "" {
				t.Fatal("resume did not preserve or create actual Task")
			}
			r.finish(t, taskID)
			r.tick(t)
			if final := r.get(t, p.ID); final.State != processing.Succeeded {
				t.Fatalf("recovery did not finish: %+v", final)
			}
			source, err := r.module.Get(r.ctx, "owner", r.source.ID)
			if err != nil || !reflect.DeepEqual(source, r.source) {
				t.Fatal("source Task/asset projection changed", err)
			}
		})
	}
}

func TestAcceptanceDefinitiveStartRetryAndScope(t *testing.T) {
	r := newAcceptanceTestRig(t)
	p := r.create(t, "rigging_start_failed")
	r.tick(t)
	before, err := r.repo.Load(r.ctx, r.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID := r.get(t, p.ID).Steps[0].TaskID
	task, err := r.module.Get(r.ctx, "owner", taskID)
	if err != nil {
		t.Fatal(err)
	}
	req := generation.StartRequest{TaskID: taskID, AttemptID: task.CurrentAttemptID, Offering: task.Offering, Input: task.Input}
	forwarded := 0
	forward := func(context.Context, generation.StartRequest) (generation.Observation, error) {
		forwarded++
		return generation.Observation{Phase: generation.UpstreamAccepted}, nil
	}
	wrong := req
	wrong.AttemptID = "not-the-attempt"
	if _, err := r.control.start(r.ctx, wrong, forward); err == nil || forwarded != 0 {
		t.Fatal("attempt mismatch reached upstream")
	}
	originalManifest := r.control.manifest
	wrongManifest := originalManifest
	wrongManifest.Scenarios = append([]acceptanceScenario(nil), originalManifest.Scenarios...)
	wrongManifest.Scenarios[0].SessionID = "other-session"
	if err := acceptanceWriteJSON(r.control.manifestPath, wrongManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := r.control.start(r.ctx, req, forward); err == nil || forwarded != 0 {
		t.Fatal("session mismatch reached upstream")
	}
	if err := acceptanceWriteJSON(r.control.manifestPath, originalManifest); err != nil {
		t.Fatal(err)
	}
	observation, err := r.control.start(r.ctx, req, forward)
	if err != nil || observation.Phase != generation.UpstreamFailed || observation.Failure.Ambiguous || forwarded != 0 {
		t.Fatalf("definitive injection: %+v %v forwards=%d", observation, err, forwarded)
	}
	// A restarted controller shares only durable files and assembled ports.
	restarted := &acceptanceController{root: r.root, manifestPath: r.control.manifestPath, manifest: originalManifest, module: r.module, plans: r.plans}
	if _, err := restarted.start(r.ctx, req, forward); err != nil || forwarded != 1 {
		t.Fatal("restart failed to resume real adapter", err)
	}
	if _, err := restarted.start(r.ctx, req, forward); err != nil || forwarded != 2 {
		t.Fatal("second allowed outbound missing", err)
	}
	if _, err := restarted.start(r.ctx, req, forward); err == nil || forwarded != 2 {
		t.Fatal("persistent start budget exceeded")
	}
	// Exercise the real kernel failure path on a second isolated rig, so the
	// direct port assertions above never manufacture or update a public Task.
	r2 := newAcceptanceTestRig(t)
	p2 := r2.create(t, "rigging_start_failed")
	r2.tick(t)
	// Reassemble Generation around the real Repository/Assets while preserving
	// the mock wire adapter's pending jobs. A generation Provider decorator is
	// the same port used by Application.Run, with no direct lifecycle writes.
	// The original module owns repository closure, so this instance remains
	// open until its cleanup and supplies the immutable source snapshot.
	assets, err := local.New(filepath.Join(r2.root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.NewWithConfig(r2.repo, assets, inputassets.Config{})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", mock.New()); err != nil {
		t.Fatal(err)
	}
	controlled, err := generation.New(generation.Dependencies{Repository: r2.repo, Catalog: r2.product, Providers: acceptanceRegistry{registry, r2.control}, Assets: assets, Inputs: inputs, Clock: r2.clock})
	if err != nil {
		t.Fatal(err)
	}
	r2.module = controlled
	r2.control.module = controlled
	failedID := r2.get(t, p2.ID).Steps[0].TaskID
	for range 3 {
		if _, err := controlled.ReconcileOnce(r2.ctx, generation.CoordinatorConfig{WorkerID: "failure-worker", LeaseFor: time.Minute, BatchSize: 10}); err != nil {
			t.Fatal(err)
		}
		r2.clock.Advance(time.Minute)
	}
	failed, err := controlled.Get(r2.ctx, "owner", failedID)
	if err != nil || failed.State != generation.TaskFailed || failed.Failure.Ambiguous {
		t.Fatalf("kernel did not persist definitive failure: %+v %v", failed, err)
	}
	r2.tick(t)
	failedPlan := r2.get(t, p2.ID)
	if failedPlan.State != processing.Failed || failedPlan.Steps[0].TaskID != failedID {
		t.Fatalf("plan did not observe real failure: %+v", failedPlan)
	}
	if _, err := r2.plans.Retry(r2.ctx, processing.RetryCommand{OwnerID: "owner", PlanID: p2.ID, ExpectedPlanVersion: failedPlan.Version, ExpectedOfferingRevision: failedPlan.Steps[0].Revision}); err != nil {
		t.Fatal(err)
	}
	r2.tick(t)
	retry := r2.get(t, p2.ID).Steps[0]
	if retry.Retry != 1 || retry.TaskID == failedID {
		t.Fatal("explicit retry did not get a distinct step Task")
	}
	r2.finish(t, retry.TaskID)
	r2.tick(t)
	if r2.get(t, p2.ID).State != processing.Succeeded {
		t.Fatal("normal adapter retry did not finish")
	}
	after, err := r.repo.Load(r.ctx, r.source.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("port scope assertions mutated source aggregate", err)
	}
}

func TestAcceptanceSeedGuard(t *testing.T) {
	root := t.TempDir()
	if acceptanceSeedGuard(root, "live", "") == nil {
		t.Fatal("live empty seed accepted")
	}
	if acceptanceSeedGuard(root, "mock", "") == nil {
		t.Fatal("implicit empty mock accepted")
	}
	if err := acceptanceSeedGuard(root, "mock", "offline-mock-only"); err != nil {
		t.Fatal(err)
	}
	if err := acceptanceWriteJSON(filepath.Join(root, "seed-report.json"), map[string]any{"mode": "dry-run", "database_is_private": true, "lifecycle_rows_modified": false}); err != nil {
		t.Fatal(err)
	}
	if acceptanceSeedGuard(root, "live", "") == nil {
		t.Fatal("dry-run seed accepted")
	}
	if err := acceptanceWriteJSON(filepath.Join(root, "seed-report.json"), map[string]any{"mode": "applied", "database_is_private": true, "lifecycle_rows_modified": false}); err != nil {
		t.Fatal(err)
	}
	if err := acceptanceSeedGuard(root, "live", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SEED_FAILED"), []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if acceptanceSeedGuard(root, "live", "") == nil {
		t.Fatal("failed seed accepted")
	}
}

// Explicit offline helper: create both source Tasks through the real kernel and
// mock Provider, then copy their quiescent database and actual saved bytes.
func TestAcceptanceOfflineSeed(t *testing.T) {
	target := os.Getenv("ART_AI_ACCEPTANCE_OFFLINE_SEED")
	if target == "" {
		t.Skip("offline seed creation requires explicit destination")
	}
	if !filepath.IsAbs(target) {
		t.Fatal("offline seed destination must be absolute")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("offline seed destination must be new")
	}
	r := newAcceptanceTestRig(t)
	if _, err := r.repo.CreateSession(r.ctx, chat.Session{ID: "session-format", OwnerID: "owner", Title: "format acceptance", CreatedAt: r.clock.Now(), UpdatedAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	prepared, err := r.module.PrepareInput(r.ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session-format", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"second offline acceptance source"}`)})
	if err != nil {
		t.Fatal(err)
	}
	human := executiongrant.Principal{OwnerID: "owner", ActorID: "test-human", Source: executiongrant.HumanBrowser}
	grant, err := r.grants.Prepare(r.ctx, executiongrant.PrepareCommand{Principal: human, SessionID: "session-format", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "acceptance-format-source", OperationID: "acceptance-format-source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.grants.Decide(r.ctx, human, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := r.module.SubmitPrepared(r.ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session-format", PreparedInputID: prepared.ID, AuthorizationRef: grant.RequestID, OperationID: "acceptance-format-source", IdempotencyKey: "acceptance-format-source"})
	if err != nil {
		t.Fatal(err)
	}
	second := r.finish(t, accepted.TaskID)
	if err := r.module.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	copyFile := func(from, to string) {
		t.Helper()
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(filepath.Join(r.root, "test.db"), filepath.Join(target, "acceptance.db"))
	err = filepath.WalkDir(filepath.Join(r.root, "assets"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(filepath.Join(r.root, "assets"), path)
		if err != nil {
			return err
		}
		copyFile(path, filepath.Join(target, "assets", relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := acceptanceWriteJSON(filepath.Join(target, "seed-report.json"), map[string]any{"mode": "applied", "database_is_private": true, "lifecycle_rows_modified": false, "offline_mock": true, "sources": []map[string]any{{"task_id": r.source.ID, "session_id": r.source.SessionID, "owner_id": r.source.OwnerID}, {"task_id": second.ID, "session_id": second.SessionID, "owner_id": second.OwnerID}}}); err != nil {
		t.Fatal(err)
	}
	t.Log("offline seed created", target)
}
