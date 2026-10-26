package taskretry

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeKernel struct {
	mu        sync.Mutex
	tasks     map[generation.TaskID]generation.Task
	byKey     map[string]generation.Task
	prepared  map[generation.PreparedInputID]generation.PreparedInput
	prepares  []generation.PrepareInputCommand
	submits   []generation.SubmitPreparedCommand
	gate      chan struct{} // when set, PrepareInput blocks until closed
	entered   chan struct{}
	submitErr error
}

func newKernel(tasks ...generation.Task) *fakeKernel {
	kernel := &fakeKernel{
		tasks: map[generation.TaskID]generation.Task{}, byKey: map[string]generation.Task{},
		prepared: map[generation.PreparedInputID]generation.PreparedInput{},
	}
	for _, task := range tasks {
		kernel.tasks[task.ID] = task
	}
	return kernel
}

func notFound() error { return &generation.Error{Code: generation.ErrNotFound, Operation: "get"} }

func (k *fakeKernel) GetScoped(_ context.Context, scope generation.TaskListScope, id generation.TaskID) (generation.Task, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	task, ok := k.tasks[id]
	if !ok || task.OwnerID != scope.OwnerID() {
		return generation.Task{}, notFound()
	}
	return task, nil
}

func (k *fakeKernel) LookupByIdempotency(_ context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if task, ok := k.byKey[string(owner)+key]; ok {
		return task, nil
	}
	return generation.Task{}, notFound()
}

func (k *fakeKernel) PrepareInput(_ context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	if k.entered != nil {
		select {
		case k.entered <- struct{}{}:
		default:
		}
	}
	if k.gate != nil {
		<-k.gate
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.prepares = append(k.prepares, cmd)
	prepared := generation.PreparedInput{
		ID:      generation.PreparedInputID("prepared-" + string(rune('a'+len(k.prepares)))),
		OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: cmd.Capability, OfferingID: cmd.OfferingID,
		OfferingRevision: "rev-1", NormalizedInput: cmd.Input, InputHash: "hash-" + string(cmd.Input),
		SceneID: cmd.SceneID, ManifestID: cmd.ManifestID, ManifestHash: cmd.ManifestHash,
	}
	k.prepared[prepared.ID] = prepared
	return prepared, nil
}

func (k *fakeKernel) GetPreparedInput(_ context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	prepared, ok := k.prepared[id]
	if !ok || prepared.OwnerID != owner {
		return generation.PreparedInput{}, notFound()
	}
	return prepared, nil
}

func (k *fakeKernel) SubmitPrepared(_ context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.submits = append(k.submits, cmd)
	if k.submitErr != nil {
		return generation.Accepted{}, k.submitErr
	}
	if existing, ok := k.byKey[string(cmd.OwnerID)+cmd.IdempotencyKey]; ok {
		return generation.Accepted{TaskID: existing.ID, Replayed: true}, nil
	}
	task := generation.Task{ID: generation.TaskID("retry-task-" + string(rune('0'+len(k.submits)))), OwnerID: cmd.OwnerID}
	k.byKey[string(cmd.OwnerID)+cmd.IdempotencyKey] = task
	return generation.Accepted{TaskID: task.ID}, nil
}

// fakeGrants holds grants by key; autoApprove stands for a session mode that
// approves the step without a person.
type fakeGrants struct {
	mu          sync.Mutex
	autoApprove bool
	byKey       map[string]executiongrant.Grant
	prepares    int
}

func newGrants(auto bool) *fakeGrants {
	return &fakeGrants{autoApprove: auto, byKey: map[string]executiongrant.Grant{}}
}

func (g *fakeGrants) Prepare(_ context.Context, cmd executiongrant.PrepareCommand) (executiongrant.Grant, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, used := g.byKey[cmd.IdempotencyKey]; used {
		return executiongrant.Grant{}, executiongrant.ErrConflict
	}
	g.prepares++
	grant := executiongrant.Grant{
		RequestID: "req-" + cmd.PreparedInputID, Status: executiongrant.Pending,
		Identity: executiongrant.Identity{
			OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, PreparedInputID: cmd.PreparedInputID,
			IdempotencyKey: cmd.IdempotencyKey, OperationID: cmd.OperationID,
		},
	}
	g.byKey[cmd.IdempotencyKey] = grant
	return grant, nil
}

func (g *fakeGrants) ApplyPolicy(_ context.Context, grant executiongrant.Grant) (executiongrant.Grant, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if grant.Status == executiongrant.Pending && g.autoApprove {
		grant.Status = executiongrant.Approved
		grant.DecidedBy = executiongrant.PolicyActorPrefix + "auto"
		g.byKey[grant.IdempotencyKey] = grant
	}
	return grant, nil
}

type fakeOrigins struct {
	grants    *fakeGrants
	prepared  map[string]string
	planSteps map[string]string
}

func (o fakeOrigins) PreparedInputOf(_ context.Context, _ string, task string) (Origin, error) {
	return Origin{PreparedInputID: o.prepared[task], PlanStepID: o.planSteps[task]}, nil
}

func (o fakeOrigins) GrantByKey(_ context.Context, _ string, key string) (executiongrant.Grant, bool, error) {
	o.grants.mu.Lock()
	defer o.grants.mu.Unlock()
	grant, ok := o.grants.byKey[key]
	return grant, ok, nil
}

func failed(id, owner string) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: "session-1",
		Capability: "text-to-image", Offering: generation.Offering{ID: "image-offering"},
		Input: json.RawMessage(`{"prompt":"a fox"}`), State: generation.TaskFailed,
		Failure: &generation.Failure{Class: "upstream_unavailable", Retryable: true},
	}
}

func human(owner string) executiongrant.Principal {
	return executiongrant.Principal{OwnerID: owner, ActorID: owner, Source: executiongrant.HumanBrowser}
}

func service(t *testing.T, kernel *fakeKernel, grants *fakeGrants, origins map[string]string, planSteps ...map[string]string) *Service {
	t.Helper()
	fake := fakeOrigins{grants: grants, prepared: origins}
	if len(planSteps) > 0 {
		fake.planSteps = planSteps[0]
	}
	svc, err := New(Config{
		Kernel: kernel, Grants: grants, Origins: fake,
		OperationID: func(id generation.PreparedInputID) string { return "task:" + string(id) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRetryRunsTheKernelPathAndSubmitsOnceWithAKeyDerivedFromTheFailedTask(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	svc := service(t, kernel, newGrants(true), nil)
	first, err := svc.Retry(context.Background(), human("owner-a"), "t1")
	if err != nil || first.TaskID == "" || first.Replayed || first.Waiting != nil {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	if len(kernel.prepares) != 1 || kernel.prepares[0].SessionID != "session-1" ||
		kernel.prepares[0].OfferingID != "image-offering" || string(kernel.prepares[0].Input) != `{"prompt":"a fox"}` {
		t.Fatalf("prepare = %+v", kernel.prepares)
	}
	want := IdempotencyKey("t1")
	if len(kernel.submits) != 1 || kernel.submits[0].IdempotencyKey != want ||
		kernel.submits[0].AuthorizationRef != "req-prepared-b" || kernel.submits[0].OperationID != "task:prepared-b" {
		t.Fatalf("submit = %+v", kernel.submits)
	}
	// Another click, or the same request again, finds the task: nothing new is frozen or paid.
	second, err := svc.Retry(context.Background(), human("owner-a"), "t1")
	if err != nil || second.TaskID != first.TaskID || !second.Replayed {
		t.Fatalf("second = %+v err=%v", second, err)
	}
	if len(kernel.prepares) != 1 || len(kernel.submits) != 1 {
		t.Fatalf("a repeat created work: prepares=%d submits=%d", len(kernel.prepares), len(kernel.submits))
	}
	if IdempotencyKey("t2") == want || len(want) > 200 {
		t.Fatal("key must be per original task and bounded")
	}
}

func TestRetryNeverSubmitsWhileThePolicyDemandsAPerson(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	grants := newGrants(false)
	svc := service(t, kernel, grants, nil)
	first, err := svc.Retry(context.Background(), human("owner-a"), "t1")
	if err != nil || first.TaskID != "" || first.Waiting == nil {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	if first.Waiting.IdempotencyKey != IdempotencyKey("t1") || first.Waiting.RequestID == "" || first.Waiting.PreparedInputID == "" {
		t.Fatalf("waiting = %+v", first.Waiting)
	}
	if len(kernel.submits) != 0 {
		t.Fatal("a task was submitted without the person's decision")
	}
	// Clicking again resumes the same pending request instead of freezing another input.
	again, err := svc.Retry(context.Background(), human("owner-a"), "t1")
	if err != nil || again.Waiting == nil || *again.Waiting != *first.Waiting {
		t.Fatalf("again = %+v err=%v", again, err)
	}
	if len(kernel.prepares) != 1 || grants.prepares != 1 || len(kernel.submits) != 0 {
		t.Fatalf("prepares=%d grants=%d submits=%d", len(kernel.prepares), grants.prepares, len(kernel.submits))
	}
	// The session switches to a mode that approves the step: the pending
	// request is approved by policy and the same task is submitted.
	grants.autoApprove = true
	done, err := svc.Retry(context.Background(), human("owner-a"), "t1")
	if err != nil || done.TaskID == "" || len(kernel.submits) != 1 || kernel.submits[0].AuthorizationRef != first.Waiting.RequestID {
		t.Fatalf("done = %+v err=%v submits=%+v", done, err, kernel.submits)
	}
}

func TestRetryAfterADeclinedDecisionIsRefused(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	grants := newGrants(false)
	svc := service(t, kernel, grants, nil)
	if _, err := svc.Retry(context.Background(), human("owner-a"), "t1"); err != nil {
		t.Fatal(err)
	}
	grant := grants.byKey[IdempotencyKey("t1")]
	grant.Status = executiongrant.Denied
	grants.byKey[IdempotencyKey("t1")] = grant
	if _, err := svc.Retry(context.Background(), human("owner-a"), "t1"); !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if len(kernel.submits) != 0 {
		t.Fatal("declined retry was submitted")
	}
}

func TestRetryOnlyForTheOwnersFailedRetryableTasks(t *testing.T) {
	running := failed("running", "owner-a")
	running.State = generation.TaskRunning
	succeeded := failed("succeeded", "owner-a")
	succeeded.State = generation.TaskSucceeded
	permanent := failed("permanent", "owner-a")
	permanent.Failure = &generation.Failure{Class: "input_rejected", Retryable: false}
	ambiguous := failed("ambiguous", "owner-a")
	ambiguous.Failure = &generation.Failure{Class: "submission_uncertain", Retryable: true, Ambiguous: true}
	noFailure := failed("nofailure", "owner-a")
	noFailure.Failure = nil
	sessionless := failed("sessionless", "owner-a")
	sessionless.SessionID = ""
	foreign := failed("foreign", "owner-b")
	kernel := newKernel(running, succeeded, permanent, ambiguous, noFailure, sessionless, foreign)
	svc := service(t, kernel, newGrants(true), nil)
	for _, id := range []string{"running", "succeeded", "permanent", "ambiguous", "nofailure", "sessionless"} {
		if _, err := svc.Retry(context.Background(), human("owner-a"), id); !errors.Is(err, ErrNotRetryable) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
	for _, id := range []string{"foreign", "missing"} {
		if _, err := svc.Retry(context.Background(), human("owner-a"), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
	if len(kernel.prepares) != 0 || len(kernel.submits) != 0 {
		t.Fatal("a refused retry still froze or submitted something")
	}
	// Only a real browser principal may retry (it is a paid step).
	for _, principal := range []executiongrant.Principal{
		{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.AgentPending},
		{OwnerID: "", ActorID: "x", Source: executiongrant.HumanBrowser},
	} {
		if _, err := svc.Retry(context.Background(), principal, "t1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("principal %+v: err = %v", principal, err)
		}
	}
}

func TestRetryKeepsTheSceneContextOfTheOriginalFrozenInput(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	kernel.prepared["origin"] = generation.PreparedInput{
		ID: "origin", OwnerID: "owner-a", SessionID: "session-1", Capability: "image-to-image", OfferingID: "scene-offering",
		NormalizedInput: json.RawMessage(`{"prompt":"x","scene":1}`), SceneID: "scene-1", ManifestID: "manifest-1", ManifestHash: "digest",
		SourceSceneObjectID: "object-1", SourceSceneRevision: 7,
		ParameterAdjustments: []generation.ParameterAdjustment{{Name: "quality", Value: json.RawMessage(`"low"`), Reason: "draft"}},
	}
	svc := service(t, kernel, newGrants(true), map[string]string{"t1": "origin"})
	if _, err := svc.Retry(context.Background(), human("owner-a"), "t1"); err != nil {
		t.Fatal(err)
	}
	got := kernel.prepares[0]
	if got.Capability != "image-to-image" || got.OfferingID != "scene-offering" || string(got.Input) != `{"prompt":"x","scene":1}` ||
		got.SceneID != "scene-1" || got.ManifestID != "manifest-1" || got.ManifestHash != "digest" ||
		got.SourceSceneObjectID != "object-1" || got.SourceSceneRevision != 7 || got.ParameterReasons["quality"] != "draft" {
		t.Fatalf("prepare = %+v", got)
	}
}

func TestConcurrentRetriesOfOneFailureCreateOneTask(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	kernel.gate = make(chan struct{})
	kernel.entered = make(chan struct{}, 1)
	svc := service(t, kernel, newGrants(true), nil)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Retry(context.Background(), human("owner-a"), "t1")
		done <- err
	}()
	<-kernel.entered
	if _, err := svc.Retry(context.Background(), human("owner-a"), "t1"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second concurrent retry: %v", err)
	}
	close(kernel.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(kernel.submits) != 1 {
		t.Fatalf("submits = %d", len(kernel.submits))
	}
}

func TestNewRequiresTheKernelPieces(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// A failed plan or skill-run step is retried through its plan or run, never
// as a standalone task: nothing is prepared, granted or submitted for it.
func TestRetryRefusesATaskThatWasAPlanStep(t *testing.T) {
	kernel := newKernel(failed("t1", "owner-a"))
	grants := newGrants(true)
	svc := service(t, kernel, grants, map[string]string{"t1": "origin"}, map[string]string{"t1": "plan-1:step-1"})
	if _, err := svc.Retry(context.Background(), human("owner-a"), "t1"); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("err = %v", err)
	}
	if len(kernel.prepares) != 0 || len(kernel.submits) != 0 || len(grants.byKey) != 0 {
		t.Fatalf("a plan step was admitted standalone: prepares=%d submits=%d grants=%d", len(kernel.prepares), len(kernel.submits), len(grants.byKey))
	}
}
