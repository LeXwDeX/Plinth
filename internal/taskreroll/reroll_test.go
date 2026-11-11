package taskreroll

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeKernel struct {
	mu       sync.Mutex
	tasks    map[generation.TaskID]generation.Task
	byKey    map[string]generation.Task
	prepared map[generation.PreparedInputID]generation.PreparedInput
	prepares []generation.PrepareInputCommand
	submits  []generation.SubmitPreparedCommand
}

func newKernel(tasks ...generation.Task) *fakeKernel {
	kernel := &fakeKernel{tasks: map[generation.TaskID]generation.Task{},
		byKey: map[string]generation.Task{}, prepared: map[generation.PreparedInputID]generation.PreparedInput{}}
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
	k.mu.Lock()
	defer k.mu.Unlock()
	k.prepares = append(k.prepares, cmd)
	prepared := generation.PreparedInput{
		ID:      generation.PreparedInputID("prepared-" + string(rune('a'+len(k.prepares)))),
		OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: cmd.Capability, OfferingID: cmd.OfferingID,
		OfferingRevision: "rev-1", NormalizedInput: cmd.Input, InputHash: "hash",
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
	if existing, ok := k.byKey[string(cmd.OwnerID)+cmd.IdempotencyKey]; ok {
		return generation.Accepted{TaskID: existing.ID, Replayed: true}, nil
	}
	task := generation.Task{ID: generation.TaskID("reroll-task-" + string(rune('0'+len(k.submits)))), OwnerID: cmd.OwnerID}
	k.byKey[string(cmd.OwnerID)+cmd.IdempotencyKey] = task
	return generation.Accepted{TaskID: task.ID}, nil
}

type fakeGrants struct {
	mu          sync.Mutex
	autoApprove bool
	// budgetPending leaves the grant pending as a budget authorisation would:
	// a plan exists but the person has not approved this step yet.
	budgetPending bool
	byKey         map[string]executiongrant.Grant
	prepares      int
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
	if grant.Status != executiongrant.Pending {
		return grant, nil
	}
	if g.autoApprove {
		grant.Status = executiongrant.Approved
		grant.DecidedBy = executiongrant.PolicyActorPrefix + "auto"
		g.byKey[grant.IdempotencyKey] = grant
		return grant, nil
	}
	if g.budgetPending {
		// A budget authorisation is recorded but not decided yet.
		grant.BudgetEligible = true
		g.byKey[grant.IdempotencyKey] = grant
	}
	return grant, nil
}

type fakeOrigins struct {
	grants    *fakeGrants
	prepared  map[string]string
	planSteps map[string]string
	frozen    map[generation.PreparedInputID]generation.PreparedInput
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

var rerollSchema = catalog.ChatRerollSchema{
	Enabled: true, SeedParameters: []string{"model_seed", "texture_seed"},
	MinSeed: 100, MaxSeed: 200,
}

func succeeded(id, owner string) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: "session-1",
		Capability: "text-to-3d", Offering: generation.Offering{ID: "model-offering"},
		Input: json.RawMessage(`{"prompt":"a fox","model_seed":7}`), State: generation.TaskSucceeded,
	}
}

func human(owner string) executiongrant.Principal {
	return executiongrant.Principal{OwnerID: owner, ActorID: owner, Source: executiongrant.HumanBrowser}
}

func service(t *testing.T, kernel *fakeKernel, grants *fakeGrants, frozen map[generation.PreparedInputID]generation.PreparedInput) *Service {
	t.Helper()
	kernel.prepared = frozen
	originIDs := map[string]string{}
	for id := range kernel.tasks {
		originIDs[string(id)] = "frozen-" + string(id)
	}
	svc, err := New(Config{
		Kernel: kernel, Grants: grants,
		Origins: fakeOrigins{grants: grants, prepared: originIDs, frozen: frozen},
		Schema:  rerollSchema,
		OperationID: func(id generation.PreparedInputID) string {
			return "task:" + string(id)
		},
		Seed: func(min, max int) int { return max },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func frozenInput(id, input string) map[generation.PreparedInputID]generation.PreparedInput {
	return map[generation.PreparedInputID]generation.PreparedInput{
		generation.PreparedInputID(id): {
			ID: generation.PreparedInputID(id), OwnerID: "owner-a", SessionID: "session-1",
			Capability: "text-to-3d", OfferingID: "model-offering", OfferingRevision: "rev-1",
			NormalizedInput: json.RawMessage(input), InputHash: "hash",
		},
	}
}

func TestRerollReplaysTheFrozenInputWithFreshSeeds(t *testing.T) {
	kernel := newKernel(succeeded("t1", "owner-a"))
	svc := service(t, kernel, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox","model_seed":7,"texture_seed":9}`))
	first, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || first.TaskID == "" || first.Waiting != nil {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	var input map[string]any
	if err := json.Unmarshal(kernel.prepares[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["model_seed"] != float64(200) || input["texture_seed"] != float64(200) || input["prompt"] != "a fox" {
		t.Fatalf("replayed input = %#v", input)
	}
	if key := kernel.submits[0].IdempotencyKey; key != "reroll:t1:client-1" {
		t.Fatalf("idempotency key = %q", key)
	}
	// The same key returns the task and creates no second work.
	second, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || second.TaskID != first.TaskID || !second.Replayed {
		t.Fatalf("second = %+v err=%v", second, err)
	}
	if len(kernel.prepares) != 1 || len(kernel.submits) != 1 {
		t.Fatalf("a repeat created work: prepares=%d submits=%d", len(kernel.prepares), len(kernel.submits))
	}
}

func TestRerollKeepsAModelWithoutSeedsAsIs(t *testing.T) {
	kernel := newKernel(succeeded("t1", "owner-a"))
	svc := service(t, kernel, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox"}`))
	if _, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); err != nil {
		t.Fatal(err)
	}
	if string(kernel.prepares[0].Input) != `{"prompt":"a fox"}` {
		t.Fatalf("input = %s", kernel.prepares[0].Input)
	}
}

func TestRerollOnlyAcceptsASucceededStandaloneTask(t *testing.T) {
	cases := []struct {
		name string
		task generation.Task
		plan string
		want error
	}{
		{"failed", generation.Task{ID: "t1", OwnerID: "owner-a", SessionID: "s", State: generation.TaskFailed}, "", ErrNotSucceeded},
		{"plan step", succeeded("t1", "owner-a"), "step-1", ErrNotSucceeded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			kernel := newKernel(test.task)
			svc := service(t, kernel, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox"}`))
			if test.plan != "" {
				svc.config.Origins = fakeOrigins{grants: newGrants(true), prepared: map[string]string{"t1": "frozen-t1"},
					planSteps: map[string]string{"t1": test.plan}}
			}
			if _, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRerollWaitsForTheApprovalMode(t *testing.T) {
	kernel := newKernel(succeeded("t1", "owner-a"))
	grants := newGrants(false)
	svc := service(t, kernel, grants, frozenInput("frozen-t1", `{"prompt":"a fox","model_seed":7}`))
	first, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || first.Waiting == nil || first.TaskID != "" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	if len(kernel.submits) != 0 {
		t.Fatal("a task was submitted without approval")
	}
	grants.autoApprove = true
	done, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || done.TaskID == "" || len(kernel.submits) != 1 ||
		kernel.submits[0].AuthorizationRef != first.Waiting.RequestID {
		t.Fatalf("done = %+v err=%v submits=%+v", done, err, kernel.submits)
	}
}

func TestRerollInputIgnoresNonSeedAndNonIntegerValues(t *testing.T) {
	output, err := RerollInput(json.RawMessage(`{"prompt":"a fox","model_seed":"seven","texture_seed":3}`),
		rerollSchema, func(min, max int) int { return max })
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(output, &values); err != nil {
		t.Fatal(err)
	}
	if values["model_seed"] != "seven" || values["texture_seed"] != float64(200) {
		t.Fatalf("values = %#v", values)
	}
}

// #242: a foreign task is not the caller's to replay, and a task without a
// frozen input has nothing to replay.
func TestRerollRefusesAForeignTaskAndATaskWithoutFrozenInput(t *testing.T) {
	foreign := newKernel(succeeded("t1", "owner-b"))
	svc := service(t, foreign, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox"}`))
	if _, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner err = %v, want ErrNotFound", err)
	}

	noOrigin := newKernel(succeeded("t1", "owner-a"))
	noOriginService := service(t, noOrigin, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox"}`))
	noOriginService.config.Origins = fakeOrigins{grants: newGrants(true), prepared: map[string]string{"t1": ""}}
	if _, err := noOriginService.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); !errors.Is(err, ErrNoFrozenInput) {
		t.Fatalf("empty origin err = %v, want ErrNoFrozenInput", err)
	}

	missing := newKernel(succeeded("t1", "owner-a"))
	missingService := service(t, missing, newGrants(true), frozenInput("frozen-t1", `{"prompt":"a fox"}`))
	missingService.config.Origins = fakeOrigins{grants: newGrants(true), prepared: map[string]string{"t1": "gone"}}
	if _, err := missingService.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); !errors.Is(err, ErrNoFrozenInput) {
		t.Fatalf("missing origin err = %v, want ErrNoFrozenInput", err)
	}
	if len(noOrigin.prepares) != 0 || len(missing.prepares) != 0 {
		t.Fatal("a refused reroll prepared an input")
	}
}

// #242: the earlier admission under this key decides what a repeat does:
// declined stays declined, and a claimed/bound admission is still in flight.
func TestRerollReplaysTheEarlierGrantState(t *testing.T) {
	cases := []struct {
		name   string
		status executiongrant.Status
		want   error
	}{
		{"declined", executiongrant.Denied, ErrDeclined},
		{"claimed", executiongrant.Claimed, ErrInProgress},
		{"bound", executiongrant.Bound, ErrInProgress},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			kernel := newKernel(succeeded("t1", "owner-a"))
			grants := newGrants(true)
			key := IdempotencyKey("t1", "client-1")
			grants.byKey[key] = executiongrant.Grant{
				RequestID: "req-old", Status: test.status,
				Identity: executiongrant.Identity{OwnerID: "owner-a", PreparedInputID: "frozen-t1", IdempotencyKey: key},
			}
			svc := service(t, kernel, grants, frozenInput("frozen-t1", `{"prompt":"a fox"}`))
			if _, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1"); !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			if len(kernel.prepares) != 0 || len(kernel.submits) != 0 {
				t.Fatalf("a %s grant created work: prepares=%d submits=%d", test.name, len(kernel.prepares), len(kernel.submits))
			}
		})
	}
}

// #242: a budget plan can hold the reroll pending; it is the same paid step as
// any other, so the task appears only after the person approves the plan.
func TestRerollWaitsForABudgetApproval(t *testing.T) {
	kernel := newKernel(succeeded("t1", "owner-a"))
	grants := newGrants(false)
	grants.budgetPending = true
	svc := service(t, kernel, grants, frozenInput("frozen-t1", `{"prompt":"a fox","model_seed":7}`))
	first, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || first.Waiting == nil || first.TaskID != "" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	if len(kernel.submits) != 0 {
		t.Fatal("a task was submitted before the budget approval")
	}
	// Repeating the key while the budget waits reuses the same admission.
	again, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || again.Waiting == nil || again.Waiting.RequestID != first.Waiting.RequestID {
		t.Fatalf("again = %+v err=%v", again, err)
	}
	if len(kernel.prepares) != 1 || len(grants.byKey) != 1 {
		t.Fatalf("the repeat prepared another admission: prepares=%d grants=%d", len(kernel.prepares), len(grants.byKey))
	}
	grants.budgetPending = false
	grants.autoApprove = true
	done, err := svc.Reroll(context.Background(), human("owner-a"), "t1", "client-1")
	if err != nil || done.TaskID == "" || len(kernel.submits) != 1 ||
		kernel.submits[0].AuthorizationRef != first.Waiting.RequestID {
		t.Fatalf("done = %+v err=%v submits=%+v", done, err, kernel.submits)
	}
}
