package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type memoryStore struct{ plans map[string]Plan }

func (store *memoryStore) CreateProcessingPlan(_ context.Context, plan Plan) error {
	if _, exists := store.plans[plan.ID]; exists {
		return ErrConflict
	}
	store.plans[plan.ID] = plan
	return nil
}
func (store *memoryStore) GetProcessingPlan(_ context.Context, owner generation.OwnerID, id string) (Plan, error) {
	plan, exists := store.plans[id]
	if !exists || plan.OwnerID != owner {
		return Plan{}, ErrMissing
	}
	return plan, nil
}
func (store *memoryStore) ListProcessingPlans(_ context.Context, owner generation.OwnerID, session string) ([]Plan, error) {
	result := []Plan{}
	for _, plan := range store.plans {
		if plan.OwnerID == owner && plan.SessionID == session {
			result = append(result, plan)
		}
	}
	return result, nil
}
func (store *memoryStore) ListProcessingPlansBySource(_ context.Context, owner generation.OwnerID, session string, source generation.TaskID) ([]Plan, error) {
	result := []Plan{}
	for _, plan := range store.plans {
		if plan.OwnerID == owner && plan.SessionID == session && plan.SourceTaskID == source {
			result = append(result, plan)
		}
	}
	return result, nil
}
func (store *memoryStore) ListActiveProcessingPlans(_ context.Context, after string, limit int) ([]Plan, error) {
	result := []Plan{}
	for _, plan := range store.plans {
		if (plan.State == Pending || plan.State == Running) && plan.ID > after {
			result = append(result, plan)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (store *memoryStore) SaveProcessingPlan(_ context.Context, plan Plan) error {
	current, exists := store.plans[plan.ID]
	if !exists || current.Version != plan.Version {
		return ErrConflict
	}
	plan.Version++
	store.plans[plan.ID] = plan
	return nil
}

type memoryCatalog map[generation.OfferingID]generation.Offering

func (catalog memoryCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	offering, ok := catalog[id]
	return offering, ok
}

func (catalog memoryCatalog) OfferingAvailable(generation.OfferingID) bool {
	return true
}

// alwaysAvailableCatalog 包装真实 Product Catalog，让运行时可用性在
// 测试里恒为可用（不可用分支由 Resume 专项测试覆盖）。
type alwaysAvailableCatalog struct {
	*catalog.Catalog
}

func (alwaysAvailableCatalog) OfferingAvailable(generation.OfferingID) bool {
	return true
}

func (catalog memoryCatalog) Normalize(_ generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	offering, ok := catalog[id]
	if !ok {
		return generation.Offering{}, nil, ErrInvalid
	}
	return offering, input, nil
}

type gatewayFake struct {
	tasks        map[generation.TaskID]generation.Task
	commands     []generation.SubmitCommand
	byKey        map[string]generation.TaskID
	prepared     map[generation.PreparedInputID]generation.PreparedInput
	submitError  error
	prepareError error
	replayError  error
	lookupError  error
}

func (gateway *gatewayFake) VerifyPreparedTaskReplay(ctx context.Context, command generation.SubmitPreparedCommand) (generation.Task, error) {
	if gateway.replayError != nil {
		return generation.Task{}, gateway.replayError
	}
	task, err := gateway.LookupByIdempotency(ctx, command.OwnerID, command.IdempotencyKey)
	if err != nil {
		return generation.Task{}, err
	}
	prepared, ok := gateway.prepared[command.PreparedInputID]
	if !ok || string(task.Input) != string(prepared.NormalizedInput) || command.AuthorizationRef != "test-human-approved" {
		return generation.Task{}, &generation.Error{Code: generation.ErrConflict}
	}
	return task, nil
}

func (gateway *gatewayFake) GetPreparedInput(_ context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	prepared, ok := gateway.prepared[id]
	if !ok || prepared.OwnerID != owner {
		return generation.PreparedInput{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get_prepared_input"}
	}
	return prepared, nil
}

func (gateway *gatewayFake) PrepareInput(_ context.Context, command generation.PrepareInputCommand) (generation.PreparedInput, error) {
	if gateway.prepareError != nil {
		return generation.PreparedInput{}, gateway.prepareError
	}
	if gateway.prepared == nil {
		gateway.prepared = map[generation.PreparedInputID]generation.PreparedInput{}
	}
	id := generation.PreparedInputID(fmt.Sprintf("prepared-%d", len(gateway.prepared)+1))
	prepared := generation.PreparedInput{ID: id, OwnerID: command.OwnerID, SessionID: command.SessionID,
		Capability: command.Capability, OfferingID: command.OfferingID, OfferingRevision: "v1",
		NormalizedInput: command.Input, InputHash: string(id) + "-hash"}
	gateway.prepared[id] = prepared
	return prepared, nil
}

func (gateway *gatewayFake) SubmitPrepared(ctx context.Context, command generation.SubmitPreparedCommand) (generation.Accepted, error) {
	prepared, ok := gateway.prepared[command.PreparedInputID]
	if !ok || prepared.OwnerID != command.OwnerID || prepared.SessionID != command.SessionID || command.AuthorizationRef == "" {
		return generation.Accepted{}, &generation.Error{Code: generation.ErrForbidden, Operation: "submit_prepared"}
	}
	return gateway.Submit(ctx, generation.SubmitCommand{OwnerID: command.OwnerID, SessionID: command.SessionID,
		Capability: prepared.Capability, OfferingID: prepared.OfferingID, Input: prepared.NormalizedInput,
		IdempotencyKey: command.IdempotencyKey})
}

// Existing lifecycle tests exercise accepted steps. This helper supplies a
// deliberate test-human decision when a newly prepared step pauses; admission
// behavior itself is covered by a separate test that calls advanceAll directly.
func advanceApproved(ctx context.Context, service *Service) error {
	if err := service.advanceAll(ctx); err != nil {
		return err
	}
	store, ok := service.store.(*memoryStore)
	if !ok {
		return nil
	}
	resumed := false
	for _, plan := range store.plans {
		if plan.State != Paused || plan.Failure != ReasonConsentRequired {
			continue
		}
		for _, step := range plan.Steps {
			if step.Failure != ReasonConsentRequired {
				continue
			}
			if _, err := service.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID,
				ExpectedPlanVersion: plan.Version, ExpectedOfferingRevision: step.Revision,
				AuthorizationRef: "test-human-approved"}); err != nil {
				return err
			}
			resumed = true
			break
		}
	}
	if resumed {
		return service.advanceAll(ctx)
	}
	return nil
}

func (gateway *gatewayFake) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	task, exists := gateway.tasks[id]
	if !exists || task.OwnerID != owner {
		return generation.Task{}, errors.New("task missing")
	}
	return task, nil
}
func (gateway *gatewayFake) LookupByIdempotency(_ context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	if gateway.lookupError != nil {
		return generation.Task{}, gateway.lookupError
	}
	task, exists := gateway.tasks[gateway.byKey[key]]
	if !exists || task.OwnerID != owner {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "lookup_idempotency"}
	}
	return task, nil
}
func (gateway *gatewayFake) Submit(_ context.Context, command generation.SubmitCommand) (generation.Accepted, error) {
	if gateway.submitError != nil {
		return generation.Accepted{}, gateway.submitError
	}
	if existing := gateway.byKey[command.IdempotencyKey]; existing != "" {
		return generation.Accepted{TaskID: existing, Replayed: true}, nil
	}
	id := generation.TaskID(fmt.Sprintf("step-%d", len(gateway.commands)+1))
	gateway.byKey[command.IdempotencyKey] = id
	gateway.commands = append(gateway.commands, command)
	gateway.tasks[id] = generation.Task{ID: id, OwnerID: command.OwnerID, SessionID: command.SessionID, Capability: command.Capability,
		Offering: generation.Offering{ID: command.OfferingID}, State: generation.TaskRunning}
	return generation.Accepted{TaskID: id}, nil
}

func TestProcessingPlanWaitsForEachPublicTaskAndRetainsSuccessOnRetry(t *testing.T) {
	ctx := context.Background()
	catalog := memoryCatalog{}
	for _, entry := range []struct {
		id           generation.OfferingID
		capability   generation.Capability
		dependencies []generation.Capability
	}{
		{"remesh", "remesh", []generation.Capability{"text-to-3d"}},
		{"rigging", "rigging", []generation.Capability{"remesh"}},
		{"animation", "animation", []generation.Capability{"rigging"}},
	} {
		catalog[entry.id] = generation.Offering{ID: entry.id, Revision: "v1", ProviderID: "meshy", Capability: entry.capability,
			Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 5,
			Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: entry.dependencies}}}
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "same-request-key-1",
		Steps: []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}, {OfferingID: "rigging", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}, {OfferingID: "animation", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{"action_id":1}`)}}}
	plan, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Create(ctx, request)
	if err != nil || replayed.ID != plan.ID {
		t.Fatalf("idempotent create = %v, %v", replayed.ID, err)
	}
	request.Steps[2].Input = json.RawMessage(`{"action_id":2}`)
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request = %v", err)
	}
	request.Steps[2].Input = json.RawMessage(`{"action_id":1}`)
	if _, err := service.Get(ctx, "other", plan.ID); !errors.Is(err, ErrMissing) {
		t.Fatalf("foreign plan = %v", err)
	}
	for index := 0; index < 3; index++ {
		if err := advanceApproved(ctx, service); err != nil {
			t.Fatal(err)
		}
		if len(gateway.commands) != index+1 {
			t.Fatalf("premature submit: %d", len(gateway.commands))
		}
		if err := advanceApproved(ctx, service); err != nil {
			t.Fatal(err)
		}
		if len(gateway.commands) != index+1 {
			t.Fatalf("duplicate submit: %d", len(gateway.commands))
		}
		task := gateway.tasks[generation.TaskID(fmt.Sprintf("step-%d", index+1))]
		if index == 2 {
			task.State = generation.TaskFailed
			task.Failure = &generation.Failure{Class: generation.FailureRejected}
		} else {
			task.State = generation.TaskSucceeded
			task.Offering = catalog[generation.OfferingID([]string{"remesh", "rigging"}[index])]
			task.Assets = []generation.Asset{{ID: generation.AssetID(fmt.Sprintf("asset-%d", index)), Role: "model", StorageKey: "ready"}}
		}
		gateway.tasks[task.ID] = task
		if err := advanceApproved(ctx, service); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || failed.State != Failed || failed.Steps[0].State != Succeeded || failed.Steps[1].State != Succeeded {
		t.Fatalf("failed plan = %#v, %v", failed, err)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: failed.Version, ExpectedOfferingRevision: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 4 || gateway.commands[3].IdempotencyKey == gateway.commands[2].IdempotencyKey {
		t.Fatalf("retry commands = %#v", gateway.commands)
	}
	last := gateway.tasks["step-4"]
	last.State = generation.TaskSucceeded
	gateway.tasks[last.ID] = last
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	completed, _ := service.Get(ctx, "owner", plan.ID)
	if completed.State != Succeeded || completed.Steps[0].TaskID != "step-1" || completed.Steps[1].TaskID != "step-2" {
		t.Fatalf("completed plan = %#v", completed)
	}
}

func TestAmbiguousPaidStepCannotBeRetried(t *testing.T) {
	store := &memoryStore{plans: map[string]Plan{"plan": {ID: "plan", OwnerID: "owner", State: Failed, Version: 1,
		Steps: []Step{{State: Failed, TaskID: "paid"}}}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"paid": {ID: "paid", OwnerID: "owner", State: generation.TaskFailed,
		Failure: &generation.Failure{Ambiguous: true}}}}
	service, _ := New(store, memoryCatalog{}, gateway, nil)
	if _, err := service.Retry(context.Background(), RetryCommand{OwnerID: "owner", PlanID: "plan", ExpectedPlanVersion: 1, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous retry = %v", err)
	}
}

func TestProductCatalogProcessingChainIsAcceptedBeforeAnyPaidSubmit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready", MediaType: "model/gltf-binary", Size: 1024}}}
	revision := func(id generation.OfferingID) string {
		offering, _ := product.Get(id)
		return offering.Revision
	}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, alwaysAvailableCatalog{product}, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "real-catalog-plan-key",
		Steps: []StepRequest{{OfferingID: "meshy/processing/remesh", ExpectedOfferingRevision: revision("meshy/processing/remesh"), Input: json.RawMessage(`{"topology":"triangle","target_polycount":300000}`)},
			{OfferingID: "meshy/processing/rigging", ExpectedOfferingRevision: revision("meshy/processing/rigging"), Input: json.RawMessage(`{}`)},
			{OfferingID: "meshy/processing/animation", ExpectedOfferingRevision: revision("meshy/processing/animation"), Input: json.RawMessage(`{"action_id":1}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("create submitted %d paid tasks", len(gateway.commands))
	}
	_, err = service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "reserved-source-key",
		Steps:          []StepRequest{{OfferingID: "meshy/processing/remesh", ExpectedOfferingRevision: revision("meshy/processing/remesh"), Input: json.RawMessage(`{"source_url":"https://example.invalid/model.glb"}`)}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("client-supplied source = %v", err)
	}
}

func singleRemeshFixture(t *testing.T) (*Service, *memoryStore, *gatewayFake, memoryCatalog, Plan) {
	t.Helper()
	catalog := memoryCatalog{"remesh": {ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh", Domain: generation.DomainProcessing,
		Enabled: true, EstimatedCredits: 5, Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "single-remesh-plan-key", Steps: []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, gateway, catalog, plan
}

func TestProcessingStepWaitsForExactHumanGrantBeforeSubmit(t *testing.T) {
	service, _, gateway, _, plan := singleRemeshFixture(t)
	ctx := context.Background()
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || paused.State != Paused || paused.Failure != ReasonConsentRequired || len(gateway.commands) != 0 {
		t.Fatalf("step did not stop before paid submit: plan=%+v commands=%d err=%v", paused, len(gateway.commands), err)
	}
	step := paused.Steps[0]
	identity, err := service.ExecutionGrantIdentity(ctx, "owner", plan.ID, string(step.PreparedInputID), step.PlanStepID)
	if err != nil || identity.SessionID != "session" || identity.IdempotencyKey != step.IdempotencyKey || identity.OperationID != plan.ID {
		t.Fatalf("server grant identity = %+v, %v", identity, err)
	}
	if _, err := service.ExecutionGrantIdentity(ctx, "other", plan.ID, string(step.PreparedInputID), step.PlanStepID); !errors.Is(err, ErrMissing) {
		t.Fatalf("cross-owner grant identity = %v", err)
	}
	if _, err := service.ExecutionGrantIdentity(ctx, "owner", plan.ID, "swapped", step.PlanStepID); !errors.Is(err, ErrMissing) {
		t.Fatalf("swapped prepared identity = %v", err)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: step.Revision}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resume without grant = %v", err)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: step.Revision,
		AuthorizationRef: "human-approved-grant"}); err != nil {
		t.Fatal(err)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != step.IdempotencyKey {
		t.Fatalf("approved step did not use frozen key: %+v", gateway.commands)
	}
}

func TestCreateRequiresCurrentOfferingRevisionBeforeSubmitting(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	request := CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "revision-check-plan-key", Steps: []StepRequest{{OfferingID: "remesh", Input: json.RawMessage(`{}`)}}}
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing revision = %v", err)
	}
	request.Steps[0].ExpectedOfferingRevision = "stale"
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("create submitted %d paid tasks", len(gateway.commands))
	}
	if _, err := service.Get(ctx, "owner", plan.ID); err != nil {
		t.Fatalf("original plan changed: %v", err)
	}
}

func TestOldRetryRequestCannotPayAgainAfterNextFailure(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	first := gateway.tasks["step-1"]
	first.State = generation.TaskFailed
	gateway.tasks[first.ID] = first
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, "owner", plan.ID)
	oldRequest := RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: failed.Version, ExpectedOfferingRevision: "v1"}
	retried, err := service.Retry(ctx, oldRequest)
	if err != nil || retried.Version != failed.Version+1 {
		t.Fatalf("first retry: plan=%#v err=%v", retried, err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	second := gateway.tasks["step-2"]
	second.State = generation.TaskFailed
	gateway.tasks[second.ID] = second
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failedAgain, _ := service.Get(ctx, "owner", plan.ID)
	if _, err := service.Retry(ctx, oldRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("delayed old retry = %v", err)
	}
	if len(gateway.commands) != 2 {
		t.Fatalf("delayed old retry submitted %d tasks", len(gateway.commands))
	}
	newRequest := RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: failedAgain.Version, ExpectedOfferingRevision: "v1"}
	if _, err := service.Retry(ctx, newRequest); err != nil {
		t.Fatalf("fresh explicit retry = %v", err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 3 || gateway.commands[2].IdempotencyKey == gateway.commands[1].IdempotencyKey {
		t.Fatalf("fresh retry commands = %#v", gateway.commands)
	}
}

func TestUncertainSubmitPausesAndResumeReplaysSameIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	gateway.submitError = errors.New("uncertain")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused, _ := service.Get(ctx, "owner", plan.ID)
	if paused.State != Paused || paused.Failure != ReasonSubmitUnknown || paused.Steps[0].State != Paused || !paused.Steps[0].Resumable {
		t.Fatalf("paused = %#v", paused)
	}
	// 暂停计划不被后台推进，也不能走"失败重试"新扣费路径。
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("paused retry = %v", err)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resume without plan version = %v", err)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "other", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrMissing) {
		t.Fatalf("foreign resume = %v", err)
	}
	// 模拟首次提交实际已被任务内核接受：恢复时必须复用同一幂等键重放。
	gateway.submitError = nil
	landed := "processing:" + plan.ID + ":0:0"
	gateway.byKey[landed] = "landed"
	gateway.tasks["landed"] = generation.Task{ID: "landed", OwnerID: "owner", SessionID: "session", Capability: "remesh",
		Offering: generation.Offering{ID: "remesh", Revision: "v1"}, State: generation.TaskRunning,
		Input: gateway.prepared[paused.Steps[0].PreparedInputID].NormalizedInput}
	resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"})
	if err != nil || resumed.State != Running || resumed.Steps[0].TaskID != "landed" || resumed.Steps[0].Retry != 0 || resumed.Version != paused.Version+1 {
		t.Fatalf("resumed = %#v, %v", resumed, err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	running, _ := service.Get(ctx, "owner", plan.ID)
	if len(gateway.commands) != 0 || running.Steps[0].TaskID != "landed" || running.State != Running {
		t.Fatalf("resume must replay without a new paid submit: commands=%d plan=%#v", len(gateway.commands), running)
	}
}

func TestLegacyUnknownSubmissionWithoutFrozenInputFailsClosed(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(memoryCatalog)
	}{
		{"revision changed", func(offerings memoryCatalog) {
			changed := offerings["remesh"]
			changed.Revision = "v2"
			offerings["remesh"] = changed
		}},
		{"offering removed", func(offerings memoryCatalog) { delete(offerings, "remesh") }},
	} {
		t.Run(change.name, func(t *testing.T) {
			ctx := context.Background()
			service, store, gateway, offerings, plan := singleRemeshFixture(t)
			gateway.submitError = errors.New("response lost")
			if err := advanceApproved(ctx, service); err != nil {
				t.Fatal(err)
			}
			paused, _ := service.Get(ctx, "owner", plan.ID)
			// This is the old steps_json shape persisted before the flag existed.
			var oldSteps []Step
			if err := json.Unmarshal([]byte(fmt.Sprintf(`[{"capability":"remesh","offering_id":"remesh","input":{},"estimated_credits":5,"revision":"v1","state":"PAUSED","retry":0,"failure":"%s","resumable":true}]`, ReasonSubmitUnknown)), &oldSteps); err != nil {
				t.Fatal(err)
			}
			paused.Steps = oldSteps
			store.plans[plan.ID] = paused
			change.edit(offerings)
			stepKey := "processing:" + plan.ID + ":0:0"
			gateway.byKey[stepKey] = "landed"
			gateway.tasks["landed"] = generation.Task{ID: "landed", OwnerID: "owner", SessionID: "session", Capability: "remesh",
				Offering: generation.Offering{ID: "remesh", Revision: "v1"}, State: generation.TaskRunning}
			recovered, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
				ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"})
			if err != nil || recovered.State != Paused || recovered.Failure != ReasonSubmitUnreconciled || recovered.Steps[0].Resumable || recovered.Steps[0].TaskID != "" || len(gateway.commands) != 0 {
				t.Fatalf("old unknown step: plan=%#v commands=%d error=%v", recovered, len(gateway.commands), err)
			}
		})
	}
}

// A committed Task is recovered before the changed Catalog is consulted.
func TestLandedSubmitWithChangedRevisionRecoversOriginalTask(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, catalog, plan := singleRemeshFixture(t)
	gateway.submitError = errors.New("uncertain")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	offering := catalog["remesh"]
	offering.Revision = "remesh-v2"
	catalog["remesh"] = offering
	landed := "processing:" + plan.ID + ":0:0"
	gateway.byKey[landed] = "landed"
	gateway.tasks["landed"] = generation.Task{ID: "landed", OwnerID: "owner", SessionID: "session", Capability: "remesh",
		Offering: generation.Offering{ID: "remesh", Revision: "v1"}, State: generation.TaskRunning}
	before, _ := service.Get(ctx, "owner", plan.ID)
	landedTask := gateway.tasks["landed"]
	landedTask.Input = gateway.prepared[before.Steps[0].PreparedInputID].NormalizedInput
	gateway.tasks["landed"] = landedTask
	resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: before.Version, ExpectedOfferingRevision: "remesh-v2"})
	if err != nil || resumed.Steps[0].Revision != "v1" {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	recovered, _ := service.Get(ctx, "owner", plan.ID)
	if recovered.State != Running || recovered.Steps[0].TaskID != "landed" || recovered.Steps[0].SubmissionUncertain {
		t.Fatalf("recovered = %#v", recovered)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: resumed.Version, ExpectedOfferingRevision: "remesh-v2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("recovered resume = %v", err)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: resumed.Version, ExpectedOfferingRevision: "remesh-v2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("recovered retry = %v", err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("no new paid submit expected, got %d", len(gateway.commands))
	}
}

func TestUnknownSubmissionWithChangedCatalogCannotReplay(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, offerings, plan := singleRemeshFixture(t)
	gateway.submitError = errors.New("response lost")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused, _ := service.Get(ctx, "owner", plan.ID)
	changed := offerings["remesh"]
	changed.Revision, changed.EstimatedCredits = "v2", 7
	offerings["remesh"] = changed
	gateway.submitError = nil
	resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
		ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v2"})
	if err != nil || !resumed.Steps[0].SubmissionUncertain || resumed.Steps[0].Revision != "v1" {
		t.Fatalf("original request was not frozen: plan=%#v err=%v", resumed, err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	unreconciled, _ := service.Get(ctx, "owner", plan.ID)
	if unreconciled.State != Paused || unreconciled.Failure != ReasonSubmitUnreconciled ||
		unreconciled.Steps[0].Resumable || len(gateway.commands) != 0 {
		t.Fatalf("changed unknown submission: plan=%#v commands=%d", unreconciled, len(gateway.commands))
	}
}

func TestRecoveredTaskIdentityMustMatchFrozenStep(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*generation.Task)
	}{
		{"session", func(task *generation.Task) { task.SessionID = "another-session" }},
		{"capability", func(task *generation.Task) { task.Capability = "rigging" }},
		{"offering", func(task *generation.Task) { task.Offering.ID = "another-offering" }},
		{"revision", func(task *generation.Task) { task.Offering.Revision = "v2" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			ctx := context.Background()
			service, _, gateway, _, plan := singleRemeshFixture(t)
			stepKey := "processing:" + plan.ID + ":0:0"
			task := generation.Task{ID: "landed", OwnerID: "owner", SessionID: "session", Capability: "remesh",
				Offering: generation.Offering{ID: "remesh", Revision: "v1"}, State: generation.TaskRunning}
			change.edit(&task)
			gateway.tasks[task.ID] = task
			gateway.byKey[stepKey] = task.ID
			if err := advanceApproved(ctx, service); err != nil {
				t.Fatal(err)
			}
			stored, _ := service.Get(ctx, "owner", plan.ID)
			if stored.State != Paused || stored.Failure != ReasonSubmitUnreconciled || stored.Steps[0].TaskID != "" || len(gateway.commands) != 0 {
				t.Fatalf("mismatched task was adopted: %#v, commands=%d", stored, len(gateway.commands))
			}
		})
	}
}

func TestCanceledStepPausesAndResumeIsExplicitNewAttempt(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	task := gateway.tasks["step-1"]
	task.State = generation.TaskCanceled
	gateway.tasks[task.ID] = task
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused, _ := service.Get(ctx, "owner", plan.ID)
	if paused.State != Paused || paused.Failure != ReasonTaskCanceled || !paused.Steps[0].Resumable {
		t.Fatalf("canceled step = %#v", paused)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 2 || gateway.commands[1].IdempotencyKey == gateway.commands[0].IdempotencyKey {
		t.Fatalf("resume after cancel must use a new key: %#v", gateway.commands)
	}
}

func TestOfferingChangePausesUntilNewCostIsConfirmed(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, catalog, plan := singleRemeshFixture(t)
	changed := catalog["remesh"]
	changed.Revision, changed.EstimatedCredits = "v2", 7
	catalog["remesh"] = changed
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	paused, _ := service.Get(ctx, "owner", plan.ID)
	if paused.State != Paused || paused.Failure != ReasonOfferingChanged || len(gateway.commands) != 0 {
		t.Fatalf("changed offering = %#v, commands=%d", paused, len(gateway.commands))
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume with stale offering revision = %v", err)
	}
	resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v2"})
	if err != nil || resumed.Steps[0].EstimatedCredits != 7 || resumed.Steps[0].Revision != "v2" {
		t.Fatalf("resumed = %#v, %v", resumed, err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("commands after confirmed resume = %d", len(gateway.commands))
	}

	second, _, _, secondCatalog, secondPlan := singleRemeshFixture(t)
	off := secondCatalog["remesh"]
	off.Enabled = false
	secondCatalog["remesh"] = off
	if err := advanceApproved(ctx, second); err != nil {
		t.Fatal(err)
	}
	unavailable, _ := second.Get(ctx, "owner", secondPlan.ID)
	if unavailable.State != Paused || unavailable.Failure != ReasonOfferingUnavailable {
		t.Fatalf("unavailable = %#v", unavailable)
	}
	if _, err := second.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: secondPlan.ID, ExpectedPlanVersion: unavailable.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume while still unavailable = %v", err)
	}
}

func TestUnitPricedResumeUsesCurrentOfferingRevisionAndEstimate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reprice bool
		want    int
	}{
		{name: "unknown submit at current price", want: 15},
		{name: "catalog revision changes unit price", reprice: true, want: 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			offerings := memoryCatalog{"remesh": {
				ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh", Domain: generation.DomainProcessing,
				Enabled: true, EstimatedCredits: 5, EstimatedCreditsUnitParameter: "n",
				Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}, {Name: "n", Type: "integer"}},
			}}
			source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
				Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
			gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
			store := &memoryStore{plans: map[string]Plan{}}
			service, err := New(store, offerings, gateway, nil)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
				IdempotencyKey: "unit-priced-remesh-key",
				Steps:          []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{"n":3}`)}}})
			if err != nil || plan.Steps[0].EstimatedCredits != 15 {
				t.Fatalf("initial unit estimate: plan=%#v err=%v", plan, err)
			}
			if tc.reprice {
				changed := offerings["remesh"]
				changed.Revision, changed.EstimatedCredits = "v2", 7
				offerings["remesh"] = changed
			} else {
				gateway.submitError = errors.New("uncertain")
			}
			if err := advanceApproved(ctx, service); err != nil {
				t.Fatal(err)
			}
			paused, err := service.Get(ctx, "owner", plan.ID)
			if err != nil || paused.State != Paused || len(gateway.commands) != 0 {
				t.Fatalf("expected pause before paid submit: plan=%#v commands=%d err=%v", paused, len(gateway.commands), err)
			}
			staleRevision := "stale"
			if tc.reprice {
				staleRevision = "v1"
			}
			if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: staleRevision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("resume with stale revision %q: %v", staleRevision, err)
			}
			if err := advanceApproved(ctx, service); err != nil {
				t.Fatal(err)
			}
			stillPaused, err := service.Get(ctx, "owner", plan.ID)
			if err != nil || stillPaused.State != Paused || stillPaused.Steps[0].Revision != "v1" || len(gateway.commands) != 0 {
				t.Fatalf("rejected revision changed plan or submitted task: plan=%#v commands=%d err=%v", stillPaused, len(gateway.commands), err)
			}
			currentRevision := "v1"
			if tc.reprice {
				currentRevision = "v2"
			}
			resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: currentRevision})
			if err != nil || resumed.State != Pending || resumed.Steps[0].EstimatedCredits != tc.want {
				t.Fatalf("resume with current total: plan=%#v err=%v", resumed, err)
			}
			if tc.reprice && resumed.Steps[0].Revision != "v2" {
				t.Fatalf("resume kept old revision: %#v", resumed.Steps[0])
			}
			gateway.submitError = nil
			if err := advanceApproved(ctx, service); err != nil {
				t.Fatal(err)
			}
			if len(gateway.commands) != 1 {
				t.Fatalf("resumed plan submitted %d tasks, want 1", len(gateway.commands))
			}
		})
	}
}

func TestDeterministicSubmitRejectionFailsWithoutRetry(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	gateway.submitError = &generation.Error{Code: generation.ErrInvalidInput, Operation: "submit"}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, "owner", plan.ID)
	if failed.State != Failed || failed.Failure != ReasonSubmitRejected || failed.Steps[0].Retryable {
		t.Fatalf("rejected = %#v", failed)
	}
	if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: failed.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume failed plan = %v", err)
	}
}

func TestAdvanceAllVisitsEveryActivePlanBeyondOnePage(t *testing.T) {
	ctx := context.Background()
	catalog := memoryCatalog{"remesh": {ID: "remesh", Revision: "v1", ProviderID: "meshy", Capability: "remesh", Domain: generation.DomainProcessing,
		Enabled: true, EstimatedCredits: 5, Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, _ := New(store, catalog, gateway, nil)
	total := activePlanPageSize*2 + 5
	for index := 0; index < total; index++ {
		if _, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
			IdempotencyKey: fmt.Sprintf("bulk-plan-key-%04d", index),
			Steps:          []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != total {
		t.Fatalf("submitted %d of %d active plans", len(gateway.commands), total)
	}
}

func TestProcessingPlanAcceptsFormatConversionAndShowsItsExtraCredits(t *testing.T) {
	// Shipped disabled (GLB-only stage); the plan contract is checked enabled.
	product := catalogtest.LoadDefaultWithTripoConversion(t)
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "image-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "tripo"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready", MediaType: "model/gltf-binary", Size: 1024}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, alwaysAvailableCatalog{product}, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	create := func(key, input string) (Plan, error) {
		return service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
			IdempotencyKey: key, Steps: []StepRequest{{OfferingID: "tripo/tripo-v3.1/format-conversion",
				ExpectedOfferingRevision: "2026-09-18.1", Input: json.RawMessage(input)}}})
	}
	plain, err := create("conversion-plain-key", `{"format":"FBX"}`)
	if err != nil {
		t.Fatalf("plain conversion rejected: %v", err)
	}
	billed, err := create("conversion-billed-key", `{"format":"FBX","texture_format":"PNG","authorize_extra_credits":true}`)
	if err != nil {
		t.Fatalf("billed conversion rejected: %v", err)
	}
	if plain.Steps[0].EstimatedCredits != 5 || billed.Steps[0].EstimatedCredits != 10 {
		t.Fatalf("step credits = %d / %d; want 5 / 10 (extras included)", plain.Steps[0].EstimatedCredits, billed.Steps[0].EstimatedCredits)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("create submitted %d paid tasks", len(gateway.commands))
	}
	if _, err := create("conversion-unconsented-key", `{"format":"FBX","texture_format":"PNG"}`); err == nil {
		t.Fatal("extra credits without the declared consent were accepted")
	}
	// A foreign-provider source cannot feed the conversion.
	gateway.tasks["source"] = func() generation.Task { s := source; s.Offering.ProviderID = "meshy"; return s }()
	if _, err := create("conversion-foreign-key", `{"format":"FBX"}`); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign source = %v", err)
	}
}
