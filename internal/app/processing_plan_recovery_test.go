package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
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
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// The mock models the external Provider, so it survives the local store/module
// reopen. Tasks, Attempts, prepared inputs, grants and plans do not live here.
type recoveryProvider struct {
	*mock.Adapter
	starts map[generation.Capability]int
}

func (p *recoveryProvider) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	p.starts[req.Offering.Capability]++
	return p.Adapter.Start(ctx, req)
}

type recoveryGateway struct {
	*generation.Module
	afterAcceptance bool
	faulted         bool
	calls           []generation.SubmitPreparedCommand
	accepted        []generation.TaskID
}

func (g *recoveryGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.calls = append(g.calls, cmd)
	fault := cmd.PlanStepID == "1:0" && !g.faulted
	if fault {
		g.faulted = true
		if !g.afterAcceptance {
			return generation.Accepted{}, errors.New("synthetic failure before acceptance")
		}
	}
	accepted, err := g.Module.SubmitPrepared(ctx, cmd)
	if err == nil && !accepted.Replayed {
		g.accepted = append(g.accepted, accepted.TaskID)
	}
	if fault && err == nil {
		return generation.Accepted{}, errors.New("synthetic lost acceptance response")
	}
	return accepted, err
}

// Cancel only after a persisted state transition, not after an arbitrary sleep
// or the intermediate save of the grant before SubmitPrepared.
type recoveryPlanStore struct {
	*sqlite.Repository
	cancel context.CancelFunc
}

func (s *recoveryPlanStore) SaveProcessingPlan(ctx context.Context, plan processing.Plan) error {
	before, err := s.GetProcessingPlan(ctx, plan.OwnerID, plan.ID)
	if err != nil {
		return err
	}
	if err := s.Repository.SaveProcessingPlan(ctx, plan); err != nil {
		return err
	}
	if s.cancel != nil {
		for i, step := range plan.Steps {
			if step.State != before.Steps[i].State || step.TaskID != before.Steps[i].TaskID {
				s.cancel()
				break
			}
		}
	}
	return nil
}

type recoveryManualMode struct{}

func (recoveryManualMode) SessionApprovalMode(context.Context, string, string) (string, error) {
	return "manual", nil
}

type recoveryCatalog struct{ *catalog.Catalog }

func (recoveryCatalog) OfferingAvailable(generation.OfferingID) bool { return true }

func TestProcessingPlanMiddleSubmissionRecoveryPersists(t *testing.T) {
	for _, afterAcceptance := range []bool{true, false} {
		name := "before_acceptance"
		if afterAcceptance {
			name = "lost_acceptance_response"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			dbPath := filepath.Join(root, "recovery.db")
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			clock := &fakeAcmeClock{now: time.Now().UTC()}
			provider := &recoveryProvider{Adapter: mock.New(), starts: map[generation.Capability]int{}}
			registry := providers.NewRegistry()
			if err := registry.Register("meshy.v2", provider); err != nil {
				t.Fatal(err)
			}
			gateway := &recoveryGateway{afterAcceptance: afterAcceptance}
			var repo *sqlite.Repository
			var plans *processing.Service
			var store *recoveryPlanStore
			open := func() {
				var err error
				repo, err = sqlite.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				repo.SetClock(clock.Now)
				assets, err := local.New(filepath.Join(root, "assets"))
				if err != nil {
					t.Fatal(err)
				}
				inputs, err := inputassets.NewWithConfig(repo, assets, inputassets.Config{ProviderAssetRoute: product.Application().Runtime.HTTP.Routes.ProviderAsset})
				if err != nil {
					t.Fatal(err)
				}
				gateway.Module, err = generation.New(generation.Dependencies{Repository: repo, Catalog: product, Providers: registry, Assets: assets, Inputs: inputs, Clock: clock})
				if err != nil {
					t.Fatal(err)
				}
				store = &recoveryPlanStore{Repository: repo}
				plans, err = processing.New(store, recoveryCatalog{product}, gateway, nil)
				if err != nil {
					t.Fatal(err)
				}
				app := product.Application()
				var known, manual []string
				for _, cap := range app.Capabilities {
					known = append(known, string(cap.ID))
				}
				for _, cap := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
					manual = append(manual, string(cap))
				}
				plans.WithGrants(&executiongrant.Service{Grants: repo, Prepared: repo,
					Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known), Modes: recoveryManualMode{}})
			}
			open()
			t.Cleanup(func() { _ = gateway.Module.Close() })
			if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", Title: "recovery", CreatedAt: clock.Now(), UpdatedAt: clock.Now()}); err != nil {
				t.Fatal(err)
			}
			getTask := func(id generation.TaskID) generation.Task {
				task, err := gateway.Get(ctx, "owner-a", id)
				if err != nil {
					t.Fatal(err)
				}
				return task
			}
			finish := func(id generation.TaskID) generation.Task {
				for range 30 {
					if _, err := gateway.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "recovery-worker", LeaseFor: time.Minute, BatchSize: 10}); err != nil {
						t.Fatal(err)
					}
					clock.Advance(time.Minute)
					task := getTask(id)
					if task.State == generation.TaskSucceeded {
						return task
					}
					if task.State.Terminal() {
						t.Fatalf("task failed: %+v", task.Failure)
					}
				}
				t.Fatalf("task %s did not finish", id)
				return generation.Task{}
			}
			// Seed only the source through the real kernel and mock materializer.
			sourceInput, err := gateway.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"synthetic recovery source"}`)})
			if err != nil {
				t.Fatal(err)
			}
			sourceGrants := executiongrant.Service{Grants: repo, Prepared: repo}
			human := executiongrant.Principal{OwnerID: "owner-a", ActorID: "synthetic-test-human", Source: executiongrant.HumanBrowser}
			sourceGrant, err := sourceGrants.Prepare(ctx, executiongrant.PrepareCommand{Principal: human, SessionID: "session-a", OfferingID: string(sourceInput.OfferingID), OfferingRevision: sourceInput.OfferingRevision, PreparedInputID: string(sourceInput.ID), PreparedInputHash: sourceInput.InputHash, IdempotencyKey: "recovery-source", OperationID: "recovery-source"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sourceGrants.Decide(ctx, human, sourceGrant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			sourceAccepted, err := gateway.Module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner-a", SessionID: "session-a", PreparedInputID: sourceInput.ID, AuthorizationRef: sourceGrant.RequestID, OperationID: "recovery-source", IdempotencyKey: "recovery-source"})
			if err != nil {
				t.Fatal(err)
			}
			source := finish(sourceAccepted.TaskID)
			var steps []processing.StepRequest
			for _, cap := range []string{"remesh", "rigging", "animation"} {
				offering, ok := product.Get(generation.OfferingID("meshy/processing/" + cap))
				if !ok {
					t.Fatalf("missing %s", cap)
				}
				input := json.RawMessage(`{}`)
				if cap == "remesh" {
					// The Remesh defaults (quad, 300000) would deliver about 600000
					// triangles, over Rigging's source limit, and the plan is refused
					// up front (#121); this is a compatible budget.
					input = json.RawMessage(`{"topology":"quad","target_polycount":160000}`)
				}
				if cap == "animation" {
					input = json.RawMessage(`{"action_id":10}`)
				}
				steps = append(steps, processing.StepRequest{OfferingID: offering.ID, ExpectedOfferingRevision: offering.Revision, Input: input})
			}
			var sourceAsset generation.AssetID
			for _, asset := range source.Assets {
				if asset.Role == "model" && asset.Variant == "glb" {
					sourceAsset = asset.ID
				}
			}
			plan, err := plans.Create(ctx, processing.CreateCommand{OwnerID: "owner-a", SessionID: "session-a", SourceTaskID: source.ID, SourceAssetID: sourceAsset, IdempotencyKey: "recovery-plan-three-steps", Steps: steps})
			if err != nil {
				t.Fatal(err)
			}
			getPlan := func() processing.Plan {
				p, err := plans.Get(ctx, "owner-a", plan.ID)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			tick := func() {
				// The deadline is a failure guard, never the sweep-completion signal.
				runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				store.cancel = cancel
				if err := plans.Run(runCtx); err != nil {
					t.Fatal(err)
				}
				store.cancel = nil
				if runCtx.Err() != context.Canceled {
					t.Fatal("no persisted Processing transition")
				}
			}
			tick()
			if got := getPlan(); got.Steps[0].TaskID == "" {
				t.Fatalf("Remesh not accepted: %+v", got)
			}
			upstream := finish(getPlan().Steps[0].TaskID)
			upstreamAggregate, err := repo.Load(ctx, upstream.ID)
			if err != nil {
				t.Fatal(err)
			}
			assetDigests := func(task generation.Task) map[generation.AssetID][32]byte {
				result := map[generation.AssetID][32]byte{}
				for _, asset := range task.Assets {
					content, err := gateway.OpenAsset(ctx, "owner-a", task.ID, asset.ID)
					if err != nil {
						t.Fatal(err)
					}
					data, readErr := io.ReadAll(content.Body)
					closeErr := content.Body.Close()
					if readErr != nil || closeErr != nil || len(data) == 0 {
						t.Fatalf("asset read: %v %v bytes=%d", readErr, closeErr, len(data))
					}
					result[asset.ID] = sha256.Sum256(data)
				}
				if len(result) == 0 {
					t.Fatal("no materialized assets")
				}
				return result
			}
			upstreamBytes := assetDigests(upstream)
			tick() // observe Remesh success
			tick() // Rigging hits the injected boundary failure
			paused := getPlan()
			middle := paused.Steps[1]
			if paused.State != processing.Paused || paused.Failure != processing.ReasonSubmitUnknown || !middle.SubmissionUncertain || middle.TaskID != "" || middle.PreparedInputID == "" || middle.AuthorizationRef == "" || len(gateway.calls) != 2 {
				t.Fatalf("expected uncertain middle step: %+v calls=%v", paused, gateway.calls)
			}
			prepared, err := repo.GetPreparedInput(ctx, "owner-a", middle.PreparedInputID)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := repo.GetExecutionGrant(ctx, "owner-a", middle.AuthorizationRef)
			if err != nil || grant.DecidedBy != "policy:manual" {
				t.Fatalf("grant=%+v err=%v", grant, err)
			}
			var recoveredID generation.TaskID
			if afterAcceptance {
				task, err := gateway.LookupByIdempotency(ctx, "owner-a", middle.IdempotencyKey)
				if err != nil {
					t.Fatal(err)
				}
				recoveredID = task.ID
			} else if _, err := gateway.LookupByIdempotency(ctx, "owner-a", middle.IdempotencyKey); !generation.IsCode(err, generation.ErrNotFound) {
				t.Fatalf("before-acceptance lookup: %v", err)
			}
			if provider.starts["rigging"] != 0 {
				t.Fatal("Rigging dispatched before reconciliation")
			}
			if err := gateway.Module.Close(); err != nil {
				t.Fatal(err)
			}
			open() // Reopen SQLite, asset store, Generation and Processing, not just the plan object.
			if got := getPlan(); !reflect.DeepEqual(paused, got) {
				t.Fatalf("plan changed on reopen: %+v", got)
			}
			persistedInput, err := repo.GetPreparedInput(ctx, "owner-a", middle.PreparedInputID)
			if err != nil || !reflect.DeepEqual(prepared, persistedInput) {
				t.Fatal("prepared input changed on reopen", err)
			}
			persistedGrant, err := repo.GetExecutionGrant(ctx, "owner-a", middle.AuthorizationRef)
			if err != nil || !reflect.DeepEqual(grant, persistedGrant) {
				t.Fatal("grant changed on reopen", err)
			}
			// Exercise the production HTTP Resume wiring against the same database.
			application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: dbPath,
				StorageRoot: filepath.Join(root, "assets"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
				ProviderMode: "mock", AgentMode: "mock", RuntimeSettings: map[string]string{"llm_model": "configured-test-model"}, AuthMode: "development", DevelopmentUser: "owner-a"}, discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			response := appChatRequest(application.server.Handler, http.MethodPost, "/api/v1/processing-plans/"+plan.ID+"/resume", fmt.Sprintf(`{"expected_plan_version":%d,"expected_offering_revision":%q}`, paused.Version, middle.Revision))
			if err := application.generation.Close(); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusAccepted {
				t.Fatalf("resume: %d %s", response.Code, response.Body)
			}
			resumed := getPlan().Steps[1]
			if resumed.PreparedInputID != middle.PreparedInputID || resumed.PreparedInputHash != middle.PreparedInputHash ||
				resumed.AuthorizationRef != middle.AuthorizationRef || resumed.IdempotencyKey != middle.IdempotencyKey ||
				resumed.OperationID != middle.OperationID || resumed.PlanStepID != middle.PlanStepID || resumed.Retry != middle.Retry {
				t.Fatalf("resume replaced identity: %+v", resumed)
			}
			if afterAcceptance {
				if resumed.TaskID != recoveredID || len(gateway.accepted) != 2 || len(gateway.calls) != 2 {
					t.Fatal("Resume did not rebind the original acceptance")
				}
			} else {
				tick()
				resumed = getPlan().Steps[1]
				if len(gateway.calls) != 3 || !reflect.DeepEqual(gateway.calls[1], gateway.calls[2]) || len(gateway.accepted) != 2 {
					t.Fatal("before-acceptance replay changed identity or acceptance count")
				}
			}
			rigging := finish(resumed.TaskID)
			tick()
			tick() // Animation must bind the recovered Rigging output.
			animation := finish(getPlan().Steps[2].TaskID)
			tick()
			if final := getPlan(); final.State != processing.Succeeded || final.Steps[0].TaskID != upstream.ID || final.Steps[1].TaskID != rigging.ID {
				t.Fatalf("final plan=%+v", final)
			}
			var input map[string]any
			if err := json.Unmarshal(animation.Input, &input); err != nil || input["source_task"] != string(rigging.ID) {
				t.Fatalf("Animation lineage=%s err=%v", animation.Input, err)
			}
			if got := getTask(upstream.ID); !reflect.DeepEqual(upstream, got) {
				t.Fatal("Remesh task/Attempt/assets changed during recovery")
			}
			finalAggregate, err := repo.Load(ctx, upstream.ID)
			if err != nil || !reflect.DeepEqual(upstreamAggregate.CurrentAttempt, finalAggregate.CurrentAttempt) {
				t.Fatal("Remesh Attempt changed", err)
			}
			if !reflect.DeepEqual(upstreamBytes, assetDigests(upstream)) {
				t.Fatal("Remesh asset bytes changed")
			}
			if len(gateway.accepted) != 3 {
				t.Fatalf("accepted=%v", gateway.accepted)
			}
			for _, step := range getPlan().Steps {
				grant, err := repo.GetExecutionGrant(ctx, "owner-a", step.AuthorizationRef)
				if err != nil || grant.DecidedBy != "policy:manual" {
					t.Fatalf("step %s grant=%+v err=%v", step.Capability, grant, err)
				}
			}
			for _, cap := range []generation.Capability{"remesh", "rigging", "animation"} {
				if provider.starts[cap] != 1 {
					t.Fatalf("%s dispatches=%d", cap, provider.starts[cap])
				}
			}
		})
	}
}
